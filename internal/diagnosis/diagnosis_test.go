package diagnosis

import (
	"strings"
	"testing"

	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
)

func term(reason string, code int) incident.Evidence {
	return incident.Evidence{ID: 1, Type: incident.EvidenceTermination, Availability: incident.Available,
		Payload: map[string]any{"reason": reason, "exit_code": int32(code)}}
}

func logs(lines ...string) incident.Evidence {
	return incident.Evidence{ID: 2, Type: incident.EvidencePreviousLogs, Availability: incident.Available,
		Payload: map[string]any{"lines": lines}}
}

func liveness() incident.Evidence {
	// Decoded-from-JSON shape on purpose.
	return incident.Evidence{ID: 3, Type: incident.EvidenceEvents, Availability: incident.Available,
		Payload: map[string]any{"events": []any{map[string]any{"reason": "Unhealthy", "message": "Liveness probe failed: timeout"}}}}
}

var noMetrics = incident.Evidence{Type: incident.EvidenceMetrics, Summary: "memory and CPU usage history",
	Availability: incident.Unavailable, Detail: "Prometheus is not configured"}

func TestDiagnose(t *testing.T) {
	tests := []struct {
		name     string
		ev       []incident.Evidence
		wantTop  string
		wantCats []string
	}{
		{"app error with error log", []incident.Evidence{term("Error", 1), logs("starting", "fatal: simulated failure")}, "application_error", []string{"application_error"}},
		{"oom", []incident.Evidence{term("OOMKilled", 137)}, "memory_exhaustion", []string{"memory_exhaustion"}},
		{"exit zero", []incident.Evidence{term("Completed", 0)}, "normal_exit", []string{"normal_exit"}},
		{"sigkill without probes", []incident.Evidence{term("Error", 137)}, "external_termination", []string{"external_termination"}},
		{"liveness with kill", []incident.Evidence{term("Error", 137), liveness()}, "liveness_probe_failure", []string{"liveness_probe_failure"}},
		{"liveness but exit 1 ranks app error first", []incident.Evidence{term("Error", 1), liveness(), logs("panic: boom")}, "application_error", []string{"application_error", "liveness_probe_failure"}},
		{"nothing known", []incident.Evidence{noMetrics}, "inconclusive", []string{"inconclusive"}},
	}
	inc := incident.Incident{Namespace: "sim", Pod: "p", Container: "app", WorkloadKind: "Deployment", WorkloadName: "crashloop"}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hs := Diagnoser{KubeContext: "docker-desktop"}.Diagnose(inc, tc.ev)
			if len(hs) != len(tc.wantCats) {
				t.Fatalf("got %d hypotheses, want %d: %+v", len(hs), len(tc.wantCats), hs)
			}
			if hs[0].Category != tc.wantTop || hs[0].Rank != 1 {
				t.Errorf("top = %s (rank %d), want %s", hs[0].Category, hs[0].Rank, tc.wantTop)
			}
			for _, h := range hs {
				if h.Method == "" || len(h.NextSteps) == 0 {
					t.Errorf("%s: missing method or next steps", h.Category)
				}
				for _, s := range h.NextSteps {
					if !strings.HasPrefix(s, "kubectl --context docker-desktop ") {
						t.Errorf("next step not a pinned kubectl command: %q", s)
					}
				}
			}
		})
	}
}

func TestDiagnoseReportsMissingEvidence(t *testing.T) {
	hs := Diagnoser{}.Diagnose(incident.Incident{}, []incident.Evidence{term("OOMKilled", 137), noMetrics})
	if len(hs[0].Missing) != 1 || !strings.Contains(hs[0].Missing[0], "Prometheus is not configured") {
		t.Errorf("missing = %v", hs[0].Missing)
	}
}

func TestAppErrorWithoutErrorLogsIsContradicted(t *testing.T) {
	hs := Diagnoser{}.Diagnose(incident.Incident{}, []incident.Evidence{term("Error", 2), logs("all good")})
	if len(hs[0].Contradicting) != 1 || hs[0].Score != 1 {
		t.Errorf("got %+v", hs[0])
	}
}
