package collector

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/sinhasandeep2006/kubesentinel/internal/detector"
	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
	"github.com/sinhasandeep2006/kubesentinel/internal/telemetry"
)

// promWith serves pod-level series only, like Docker Desktop's cAdvisor.
func promWith(t *testing.T, mem, limit, throttle string) *telemetry.Prometheus {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		v := ""
		if strings.Contains(q, `container=""`) {
			switch {
			case strings.Contains(q, "working_set"):
				v = mem
			case strings.Contains(q, "memory_limit"):
				v = limit
			case strings.Contains(q, "throttled"):
				v = throttle
			}
		}
		res := "[]"
		if v != "" {
			res = `[{"metric":{},"value":[0,"` + v + `"]}]`
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":%s}}`, res)
	}))
	t.Cleanup(srv.Close)
	return telemetry.New(srv.URL)
}

func TestMetricsEvidence(t *testing.T) {
	sig := detector.Signal{Namespace: "sim", Pod: "p", Container: "app"}
	ctx := context.Background()

	if e := New(nil, nil, time.Minute).metrics(ctx, sig); e.Availability != incident.Unavailable || e.Detail != "Prometheus is not configured" {
		t.Errorf("not configured: %+v", e)
	}
	if e := New(nil, promWith(t, "", "", ""), time.Minute).metrics(ctx, sig); e.Availability != incident.Unavailable {
		t.Errorf("no series must be unavailable, not zero: %+v", e)
	}
	if e := New(nil, telemetry.New("http://127.0.0.1:1"), time.Minute).metrics(ctx, sig); e.Availability != incident.Error {
		t.Errorf("unreachable prometheus must be an error: %+v", e)
	}

	e := New(nil, promWith(t, "30000000", "33554432", ""), time.Minute).metrics(ctx, sig)
	if e.Availability != incident.Available || e.Payload["scope"] != "pod" || e.Detail == "" {
		t.Fatalf("pod-level fallback must be labelled: %+v", e)
	}
	if r, _ := e.Payload["memory_ratio"].(float64); r < 0.89 || r > 0.9 {
		t.Errorf("memory_ratio = %v", e.Payload["memory_ratio"])
	}
	if e.Payload["cpu_throttled_ratio"] != nil {
		t.Errorf("missing throttling must be null, got %v", e.Payload["cpu_throttled_ratio"])
	}
}

func TestVerifyThrottlingIncident(t *testing.T) {
	now := time.Now()
	inc := incident.Incident{Namespace: "sim", WorkloadKind: "Deployment", WorkloadName: "web", Container: "app", Detector: "cpu-throttling"}
	healthyPod := deployPod("a", 11*time.Minute, true, now)
	tests := []struct {
		name string
		prom *telemetry.Prometheus
		want incident.Outcome
	}{
		{"still throttled", promWith(t, "1", "2", "0.9"), incident.OutcomeFailed},
		{"no longer throttled", promWith(t, "1", "2", "0.1"), incident.OutcomePassed},
		{"no prometheus: cannot verify, never resolve", nil, incident.OutcomeFailed},
		{"no series: cannot verify", promWith(t, "", "", ""), incident.OutcomeFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := New(fake.NewClientset(healthyPod), tc.prom, 10*time.Minute).VerifyWorkload(context.Background(), inc)
			if v.Outcome != tc.want {
				t.Errorf("outcome %s, want %s: %+v", v.Outcome, tc.want, v.Criteria)
			}
		})
	}
}
