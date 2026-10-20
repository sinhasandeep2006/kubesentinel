package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeProm answers queries by substring match; unmatched queries return an empty vector.
func fakeProm(t *testing.T, answers map[string]string) *Prometheus {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/-/ready" {
			fmt.Fprint(w, "ready")
			return
		}
		q := r.URL.Query().Get("query")
		result := "[]"
		for k, v := range answers {
			if strings.Contains(q, k) {
				result = v
				break
			}
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":%s}}`, result)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func one(v string) string { return `[{"metric":{},"value":[0,"` + v + `"]}]` }

func TestContainerUsageFallsBackToPodScope(t *testing.T) {
	p := fakeProm(t, map[string]string{
		`working_set_bytes{namespace="sim",pod="p",container=""}`:                        one("30000000"),
		`memory_limit_bytes{namespace="sim",pod="p",container=""}`:                       one("33554432"),
		`throttled_periods_total{namespace="sim",pod="p",container=""}`:                  one("0.75"),
		`increase(container_cpu_cfs_periods_total{namespace="sim",pod="p",container=""}`: one("1200"),
	})
	u, err := p.ContainerUsage(context.Background(), "sim", "p", "app", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if u == nil || u.Scope != "pod" || *u.MemoryMaxBytes != 30000000 || *u.MemoryLimitBytes != 33554432 || *u.ThrottledRatio != 0.75 {
		t.Errorf("got %+v", u)
	}
}

func TestContainerUsageNoSeriesIsNoData(t *testing.T) {
	u, err := fakeProm(t, nil).ContainerUsage(context.Background(), "sim", "p", "app", time.Minute)
	if err != nil || u != nil {
		t.Errorf("got %+v, %v; want nil, nil (no data, not zero)", u, err)
	}
}

func TestNaNIsNoData(t *testing.T) {
	p := fakeProm(t, map[string]string{
		"working_set_bytes":       one("100"),
		"throttled_periods_total": one("NaN"),
		"memory_limit_bytes":      one("0"),
	})
	u, err := p.ContainerUsage(context.Background(), "sim", "p", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if u.ThrottledRatio != nil || u.MemoryLimitBytes != nil {
		t.Errorf("NaN ratio and 0 limit must be nil, got ratio=%v limit=%v", u.ThrottledRatio, u.MemoryLimitBytes)
	}
}

func TestQueryErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"status":"error","error":"parse error"}`)
	}))
	defer srv.Close()
	if _, err := New(srv.URL).Query(context.Background(), "bad("); err == nil || !strings.Contains(err.Error(), "parse error") {
		t.Errorf("err = %v", err)
	}
	if _, err := New("http://127.0.0.1:1").Query(context.Background(), "up"); err == nil {
		t.Error("unreachable prometheus must be an error")
	}
}

func TestThrottlingPrefersContainerSeries(t *testing.T) {
	p := fakeProm(t, map[string]string{
		"/ sum by": `[
			{"metric":{"namespace":"sim","pod":"a","container":""},"value":[0,"0.9"]},
			{"metric":{"namespace":"sim","pod":"a","container":"app"},"value":[0,"0.8"]},
			{"metric":{"namespace":"sim","pod":"b","container":""},"value":[0,"0.6"]}]`,
		"sum by (namespace, pod, container) (increase(container_cpu_cfs_periods_total": `[
			{"metric":{"namespace":"sim","pod":"a","container":"app"},"value":[0,"5000"]},
			{"metric":{"namespace":"sim","pod":"b","container":""},"value":[0,"700"]}]`,
	})
	got, err := p.Throttling(context.Background(), "sim", 10*time.Minute, 600)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Container != "app" || got[0].Periods != 5000 || got[1].Pod != "b" || got[1].Container != "" {
		t.Errorf("got %+v", got)
	}
}
