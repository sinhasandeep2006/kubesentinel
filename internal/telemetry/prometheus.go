// Package telemetry queries Prometheus. It never stores time series: Prometheus
// owns them. A query that returns no series yields "no data", never zero.
package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const queryTimeout = 10 * time.Second

type Prometheus struct {
	base string
	http *http.Client
}

// New returns a client for the Prometheus at baseURL (e.g. http://localhost:30090).
func New(baseURL string) *Prometheus {
	return &Prometheus{base: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: queryTimeout}}
}

type Sample struct {
	Labels map[string]string
	Value  float64
}

// Query runs an instant query and returns the resulting vector.
func (p *Prometheus) Query(ctx context.Context, q string) ([]Sample, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/api/v1/query?query="+url.QueryEscape(q), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying prometheus: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("reading prometheus response: %w", err)
	}
	var r struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decoding prometheus response (HTTP %d): %w", resp.StatusCode, err)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("prometheus query failed: %s", r.Error)
	}
	if r.Data.ResultType != "vector" {
		return nil, fmt.Errorf("prometheus returned %s, want vector", r.Data.ResultType)
	}
	out := make([]Sample, 0, len(r.Data.Result))
	for _, x := range r.Data.Result {
		s, _ := x.Value[1].(string)
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing sample value %q: %w", s, err)
		}
		out = append(out, Sample{Labels: x.Metric, Value: v})
	}
	return out, nil
}

// Ready reports whether Prometheus answers its readiness endpoint.
func (p *Prometheus) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/-/ready", nil)
	if err != nil {
		return err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prometheus not ready: HTTP %d", resp.StatusCode)
	}
	return nil
}

// promDuration formats d for PromQL range selectors, e.g. 600s.
func promDuration(d time.Duration) string { return fmt.Sprintf("%ds", int(d.Seconds())) }

// quote escapes a label value for PromQL.
func quote(s string) string { return strconv.Quote(s) }

// Usage is a container's (or, where the runtime only exposes pod cgroups, a
// pod's) resource picture over a window. Nil fields mean "no data".
type Usage struct {
	Scope            string // "container" or "pod"
	MemoryMaxBytes   *float64
	MemoryLimitBytes *float64 // nil when no limit is set
	ThrottledRatio   *float64 // fraction of CFS periods throttled
	CPUPeriods       *float64 // CFS periods the container was runnable in (activity)
}

// ContainerUsage reports usage for one container over window. It prefers
// container-level series and falls back to pod-level ones, saying which it used.
// It returns (nil, nil) when Prometheus has no series for the pod at all.
func (p *Prometheus) ContainerUsage(ctx context.Context, namespace, pod, container string, window time.Duration) (*Usage, error) {
	w := promDuration(window)
	scopes := []struct{ name, sel string }{
		{"container", fmt.Sprintf(`namespace=%s,pod=%s,container=%s`, quote(namespace), quote(pod), quote(container))},
		{"pod", fmt.Sprintf(`namespace=%s,pod=%s,container=""`, quote(namespace), quote(pod))},
	}
	if container == "" {
		scopes = scopes[1:]
	}
	for _, sc := range scopes {
		mem, err := p.scalar(ctx, fmt.Sprintf(`max(max_over_time(container_memory_working_set_bytes{%s}[%s]))`, sc.sel, w))
		if err != nil {
			return nil, err
		}
		if mem == nil {
			continue // no series at this scope
		}
		u := &Usage{Scope: sc.name, MemoryMaxBytes: mem}
		if u.MemoryLimitBytes, err = p.scalar(ctx, fmt.Sprintf(`max(container_spec_memory_limit_bytes{%s})`, sc.sel)); err != nil {
			return nil, err
		}
		if u.MemoryLimitBytes != nil && *u.MemoryLimitBytes <= 0 {
			u.MemoryLimitBytes = nil // cAdvisor reports 0 for "no limit"
		}
		if u.CPUPeriods, err = p.scalar(ctx, fmt.Sprintf(`sum(increase(container_cpu_cfs_periods_total{%s}[%s]))`, sc.sel, w)); err != nil {
			return nil, err
		}
		if u.ThrottledRatio, err = p.scalar(ctx, fmt.Sprintf(`sum(increase(container_cpu_cfs_throttled_periods_total{%s}[%s])) / sum(increase(container_cpu_cfs_periods_total{%s}[%s]))`, sc.sel, w, sc.sel, w)); err != nil {
			return nil, err
		}
		return u, nil
	}
	return nil, nil
}

// scalar runs a query expected to return at most one sample. No sample, or NaN
// (e.g. 0/0 when a container had no CPU periods), is nil: no data, not zero.
func (p *Prometheus) scalar(ctx context.Context, q string) (*float64, error) {
	ss, err := p.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(ss) == 0 || ss[0].Value != ss[0].Value { // NaN != NaN
		return nil, nil
	}
	v := ss[0].Value
	return &v, nil
}

// Throttle is one pod's (or container's) CPU throttling over a window.
type Throttle struct {
	Namespace, Pod, Container string // Container is empty for pod-level series
	Ratio                     float64
	Periods                   float64
}

// Throttling returns CPU throttling per container (or per pod where only pod
// cgroups exist) in namespace over window, for workloads that were active in at
// least minPeriods CFS periods. Idle containers are left out: throttling only
// matters when something is trying to run.
func (p *Prometheus) Throttling(ctx context.Context, namespace string, window time.Duration, minPeriods float64) ([]Throttle, error) {
	w := promDuration(window)
	sel := fmt.Sprintf(`namespace=%s`, quote(namespace))
	q := fmt.Sprintf(`sum by (namespace, pod, container) (increase(container_cpu_cfs_throttled_periods_total{%s}[%s]))
		/ sum by (namespace, pod, container) (increase(container_cpu_cfs_periods_total{%s}[%s]))
		and sum by (namespace, pod, container) (increase(container_cpu_cfs_periods_total{%s}[%s])) >= %g`,
		sel, w, sel, w, sel, w, minPeriods)
	ratios, err := p.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	periods, err := p.Query(ctx, fmt.Sprintf(`sum by (namespace, pod, container) (increase(container_cpu_cfs_periods_total{%s}[%s]))`, sel, w))
	if err != nil {
		return nil, err
	}
	key := func(l map[string]string) string { return l["pod"] + "/" + l["container"] }
	act := map[string]float64{}
	for _, s := range periods {
		act[key(s.Labels)] = s.Value
	}
	// Where both container- and pod-level series exist, keep the container ones.
	hasContainer := map[string]bool{}
	for _, s := range ratios {
		if s.Labels["container"] != "" {
			hasContainer[s.Labels["pod"]] = true
		}
	}
	var out []Throttle
	for _, s := range ratios {
		if s.Value != s.Value || (s.Labels["container"] == "" && hasContainer[s.Labels["pod"]]) {
			continue
		}
		out = append(out, Throttle{Namespace: s.Labels["namespace"], Pod: s.Labels["pod"], Container: s.Labels["container"],
			Ratio: s.Value, Periods: act[key(s.Labels)]})
	}
	return out, nil
}
