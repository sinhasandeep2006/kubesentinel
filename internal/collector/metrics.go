package collector

import (
	"context"
	"fmt"

	"github.com/sinhasandeep2006/kubesentinel/internal/detector"
	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
)

// metrics records memory and CPU usage over the detection window. Every value
// Prometheus cannot provide is JSON null in the payload, never 0.
func (c *Collector) metrics(ctx context.Context, sig detector.Signal) incident.Evidence {
	ev := incident.Evidence{
		Type: incident.EvidenceMetrics, Source: "prometheus", Resource: resource(sig), ObservedAt: sig.ObservedAt,
		Summary: "memory and CPU usage over the detection window", Payload: map[string]any{},
	}
	if c.prom == nil {
		ev.Availability, ev.Detail = incident.Unavailable, "Prometheus is not configured"
		return ev
	}
	u, err := c.prom.ContainerUsage(ctx, sig.Namespace, sig.Pod, sig.Container, c.window)
	if err != nil {
		ev.Availability, ev.Detail = incident.Error, err.Error()
		return ev
	}
	if u == nil {
		ev.Availability, ev.Detail = incident.Unavailable, "Prometheus has no series for this pod"
		return ev
	}

	val := func(p *float64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	var ratio *float64
	if u.MemoryMaxBytes != nil && u.MemoryLimitBytes != nil {
		r := *u.MemoryMaxBytes / *u.MemoryLimitBytes
		ratio = &r
	}
	ev.Availability = incident.Available
	ev.Payload = map[string]any{
		"scope":               u.Scope,
		"window_seconds":      int(c.window.Seconds()),
		"memory_max_bytes":    val(u.MemoryMaxBytes),
		"memory_limit_bytes":  val(u.MemoryLimitBytes),
		"memory_ratio":        val(ratio),
		"cpu_throttled_ratio": val(u.ThrottledRatio),
		"cpu_periods":         val(u.CPUPeriods),
	}
	ev.Summary = fmt.Sprintf("%s-level usage over %s: peak memory %s, CPU throttled %s",
		u.Scope, c.window, pct(ratio, "of limit"), pct(u.ThrottledRatio, "of periods"))
	if u.Scope == "pod" && sig.Container != "" {
		ev.Detail = "the container runtime exposes only pod-level series; values cover the whole pod"
	}
	return ev
}

func pct(p *float64, of string) string {
	if p == nil {
		return "unknown"
	}
	return fmt.Sprintf("%.0f%% %s", *p*100, of)
}

// throttleCriterion checks that a pod is no longer CPU-throttled over the window.
// No data fails the check: recovery is never assumed from missing telemetry.
func (c *Collector) throttleCriterion(ctx context.Context, namespace, pod, container string) incident.Criterion {
	cr := incident.Criterion{Name: fmt.Sprintf("pod %s: CPU throttled in under %.0f%% of periods over %s", pod, detector.ThrottleThreshold*100, c.window)}
	if c.prom == nil {
		cr.Observed = "Prometheus is not configured; throttling cannot be verified"
		return cr
	}
	u, err := c.prom.ContainerUsage(ctx, namespace, pod, container, c.window)
	switch {
	case err != nil:
		cr.Observed = "querying Prometheus: " + err.Error()
	case u == nil:
		cr.Observed = "no metrics series for the pod"
	case u.ThrottledRatio == nil && u.CPUPeriods != nil && *u.CPUPeriods == 0:
		cr.Passed, cr.Observed = true, "no CPU activity in the window"
	case u.ThrottledRatio == nil:
		cr.Observed = "no throttling data"
	default:
		cr.Passed = *u.ThrottledRatio < detector.ThrottleThreshold
		cr.Observed = fmt.Sprintf("throttled in %.0f%% of periods (%s-level)", *u.ThrottledRatio*100, u.Scope)
	}
	return cr
}
