// Package collector gathers evidence for signals and verifies recovery.
// It only reads from the cluster: get, list, and pod logs.
package collector

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"

	"github.com/sinhasandeep2006/kubesentinel/internal/detector"
	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
	"github.com/sinhasandeep2006/kubesentinel/internal/telemetry"
)

const (
	callTimeout = 10 * time.Second
	logLines    = 20
	logBytes    = 8 << 10
	maxEvents   = 20
)

type Collector struct {
	cs     kubernetes.Interface
	prom   *telemetry.Prometheus // nil when Prometheus is not configured
	window time.Duration         // how long a container must stay healthy to count as recovered
}

func New(cs kubernetes.Interface, prom *telemetry.Prometheus, window time.Duration) *Collector {
	return &Collector{cs: cs, prom: prom, window: window}
}

func (c *Collector) Collect(ctx context.Context, sig detector.Signal) []incident.Evidence {
	ev := []incident.Evidence{signalEvidence(sig)}
	if sig.Container != "" { // pod-level conditions (scheduling) have no container to inspect
		ev = append(ev, terminationEvidence(sig))
	}
	ev = append(ev, c.events(ctx, sig))
	if sig.Container != "" {
		ev = append(ev, c.previousLogs(ctx, sig))
	}
	return append(ev, c.metrics(ctx, sig))
}

func resource(sig detector.Signal) string {
	if sig.Container == "" {
		return fmt.Sprintf("pod/%s/%s", sig.Namespace, sig.Pod)
	}
	return fmt.Sprintf("pod/%s/%s/%s", sig.Namespace, sig.Pod, sig.Container)
}

func signalEvidence(sig detector.Signal) incident.Evidence {
	if sig.Reason != "" { // condition detectors
		return incident.Evidence{
			Type: incident.EvidenceDetectorSignal, Source: "kubernetes:pod.status", Resource: resource(sig),
			ObservedAt: sig.ObservedAt, Summary: sig.Summary, Availability: incident.Available,
			Payload: map[string]any{
				"detector": sig.Detector, "reason": sig.Reason, "message": redact(sig.Message),
				"waiting_reason": sig.WaitingReason, "restart_count": sig.RestartCount,
			},
		}
	}
	return incident.Evidence{
		Type: incident.EvidenceRestartSignal, Source: "kubernetes:pod.status", Resource: resource(sig),
		ObservedAt: sig.ObservedAt, Summary: sig.Summary, Availability: incident.Available,
		Payload: map[string]any{
			"restart_count": sig.RestartCount, "restart_delta": sig.RestartDelta,
			"window_seconds": int(sig.Window.Seconds()), "waiting_reason": sig.WaitingReason,
		},
	}
}

func terminationEvidence(sig detector.Signal) incident.Evidence {
	ev := incident.Evidence{
		Type: incident.EvidenceTermination, Source: "kubernetes:pod.status", Resource: resource(sig),
		ObservedAt: sig.ObservedAt, Payload: map[string]any{},
	}
	t := sig.Termination
	if t == nil {
		ev.Availability, ev.Detail, ev.Summary = incident.Unavailable, "kubelet reported no previous termination", "last termination"
		return ev
	}
	ev.Availability = incident.Available
	ev.Summary = fmt.Sprintf("last terminated: %s, exit code %d", t.Reason, t.ExitCode)
	ev.Payload = map[string]any{"reason": t.Reason, "exit_code": t.ExitCode, "finished_at": t.FinishedAt}
	if !t.FinishedAt.IsZero() {
		ev.ObservedAt = t.FinishedAt
	}
	return ev
}

func (c *Collector) events(ctx context.Context, sig detector.Signal) incident.Evidence {
	ev := incident.Evidence{
		Type: incident.EvidenceEvents, Source: "kubernetes:events", Resource: resource(sig),
		ObservedAt: sig.ObservedAt, Summary: "Kubernetes events for the pod", Payload: map[string]any{},
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	list, err := c.cs.CoreV1().Events(sig.Namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("involvedObject.uid", sig.PodUID).String(),
	})
	if err != nil {
		ev.Availability, ev.Detail = incident.Error, fmt.Sprintf("listing events: %v", err)
		return ev
	}

	items := list.Items[:0]
	for _, e := range list.Items {
		if string(e.InvolvedObject.UID) == sig.PodUID { // fake clients ignore field selectors
			items = append(items, e)
		}
	}
	sort.Slice(items, func(i, j int) bool { return lastSeen(items[i]).After(lastSeen(items[j])) })
	if len(items) > maxEvents {
		items = items[:maxEvents]
	}
	out := make([]map[string]any, 0, len(items))
	for _, e := range items {
		out = append(out, map[string]any{
			"type": e.Type, "reason": e.Reason, "message": redact(e.Message), "count": e.Count, "last_seen": lastSeen(e),
		})
	}
	ev.Availability = incident.Available
	ev.Summary = fmt.Sprintf("%d events for the pod", len(out))
	ev.Payload = map[string]any{"events": out}
	return ev
}

func lastSeen(e corev1.Event) time.Time {
	switch {
	case !e.LastTimestamp.IsZero():
		return e.LastTimestamp.Time
	case !e.EventTime.IsZero():
		return e.EventTime.Time
	}
	return e.CreationTimestamp.Time
}

func (c *Collector) previousLogs(ctx context.Context, sig detector.Signal) incident.Evidence {
	ev := incident.Evidence{
		Type: incident.EvidencePreviousLogs, Source: "kubernetes:pods/log?previous=true", Resource: resource(sig),
		ObservedAt: sig.ObservedAt, Summary: "logs of the previous (crashed) container", Payload: map[string]any{},
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	tail, limit := int64(logLines), int64(logBytes)
	raw, err := c.cs.CoreV1().Pods(sig.Namespace).GetLogs(sig.Pod, &corev1.PodLogOptions{
		Container: sig.Container, Previous: true, TailLines: &tail, LimitBytes: &limit,
	}).DoRaw(ctx)
	switch {
	case apierrors.IsNotFound(err) || apierrors.IsBadRequest(err):
		ev.Availability, ev.Detail = incident.Unavailable, fmt.Sprintf("no previous container logs: %v", err)
		return ev
	case err != nil:
		ev.Availability, ev.Detail = incident.Error, fmt.Sprintf("reading logs: %v", err)
		return ev
	}
	text := strings.TrimRight(string(raw), "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(redact(text), "\n")
	}
	ev.Availability = incident.Available
	ev.Summary = fmt.Sprintf("last %d log lines of the previous container", len(lines))
	ev.Payload = map[string]any{"lines": lines, "truncated": len(raw) >= logBytes}
	return ev
}

var secretPattern = regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key|authorization)(\s*[:=]\s*)\S+`)

// redact masks obvious credentials before anything is stored.
func redact(s string) string { return secretPattern.ReplaceAllString(s, "$1$2[REDACTED]") }

// VerifyWorkload re-reads the workload's pods directly from the API (not the
// cache). It passes only when every live pod's container has been running and
// ready for at least Window. Container start time resets on every restart, so
// this needs no restart history and works for replacement pods too.
func (c *Collector) VerifyWorkload(ctx context.Context, inc incident.Incident) incident.Verification {
	v := incident.Verification{CheckedAt: time.Now()}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	list, err := c.cs.CoreV1().Pods(inc.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		// A failed read proves nothing either way: report failed so the incident stays open.
		v.Outcome, v.Summary = incident.OutcomeFailed, "could not list pods: "+err.Error()
		return v
	}

	v.Outcome = incident.OutcomePassed
	pods := 0
	for i := range list.Items {
		p := &list.Items[i]
		if k, n := detector.WorkloadOf(p); k != inc.WorkloadKind || n != inc.WorkloadName || p.DeletionTimestamp != nil {
			continue // other workloads, and pods already on their way out
		}
		pods++
		// Pod-level incidents (scheduling) have no container: then every container must be healthy.
		names := []string{inc.Container}
		if inc.Container == "" {
			names = nil
			for _, cs := range p.Status.ContainerStatuses {
				names = append(names, cs.Name)
			}
			if len(names) == 0 {
				v.Outcome = incident.OutcomeFailed
				v.Criteria = append(v.Criteria, incident.Criterion{Name: "pod " + p.Name + ": containers running and ready",
					Observed: fmt.Sprintf("no containers started (phase %s)", p.Status.Phase)})
				continue
			}
		}
		for _, name := range names {
			cr := incident.Criterion{Name: fmt.Sprintf("pod %s: %s running and ready for %s", p.Name, name, c.window)}
			cs := containerStatus(p, name)
			switch {
			case cs == nil:
				cr.Observed = "container status not reported"
			case cs.State.Running == nil:
				cr.Observed = stateName(cs.State)
			case !cs.Ready:
				cr.Observed = "running, not ready"
			default:
				up := v.CheckedAt.Sub(cs.State.Running.StartedAt.Time).Round(time.Second)
				cr.Passed = up >= c.window
				cr.Observed = fmt.Sprintf("running and ready for %s, %d restarts total", up, cs.RestartCount)
			}
			if !cr.Passed {
				v.Outcome = incident.OutcomeFailed
			}
			v.Criteria = append(v.Criteria, cr)
		}
		// A throttled container is usually running and ready; the throttling itself must stop.
		if inc.Detector == "cpu-throttling" {
			cr := c.throttleCriterion(ctx, inc.Namespace, p.Name, inc.Container)
			if !cr.Passed {
				v.Outcome = incident.OutcomeFailed
			}
			v.Criteria = append(v.Criteria, cr)
		}
	}

	switch {
	case pods == 0:
		v.Outcome = incident.OutcomeInconclusive
		v.Summary = fmt.Sprintf("%s/%s has no pods left; recovery could not be verified", inc.WorkloadKind, inc.WorkloadName)
	case v.Outcome == incident.OutcomePassed:
		v.Summary = fmt.Sprintf("all %d pod(s) of %s/%s running and ready for at least %s", len(v.Criteria), inc.WorkloadKind, inc.WorkloadName, c.window)
	default:
		v.Summary = "workload has not recovered"
	}
	return v
}

func containerStatus(p *corev1.Pod, name string) *corev1.ContainerStatus {
	for i := range p.Status.ContainerStatuses {
		if p.Status.ContainerStatuses[i].Name == name {
			return &p.Status.ContainerStatuses[i]
		}
	}
	return nil
}

func stateName(s corev1.ContainerState) string {
	switch {
	case s.Running != nil:
		return "running"
	case s.Waiting != nil:
		return "waiting: " + s.Waiting.Reason
	case s.Terminated != nil:
		return "terminated: " + s.Terminated.Reason
	}
	return "unknown"
}
