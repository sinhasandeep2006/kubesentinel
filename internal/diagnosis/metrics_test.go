package diagnosis

import (
	"strings"
	"testing"

	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
)

func usage(memRatio, throttle any) incident.Evidence {
	return incident.Evidence{ID: 30, Type: incident.EvidenceMetrics, Availability: incident.Available,
		Payload: map[string]any{"memory_ratio": memRatio, "cpu_throttled_ratio": throttle}}
}

func TestOOMWithMetrics(t *testing.T) {
	hs := Diagnoser{}.Diagnose(incident.Incident{}, []incident.Evidence{term("OOMKilled", 137), usage(0.97, nil)})
	if hs[0].Score != 2 || !strings.Contains(hs[0].Supporting[1].Note, "97%") {
		t.Errorf("near-limit peak should support the OOM hypothesis: %+v", hs[0])
	}

	// A peak far below the limit is a scrape gap, not a contradiction.
	hs = Diagnoser{}.Diagnose(incident.Incident{}, []incident.Evidence{term("OOMKilled", 137), usage(0.01, nil)})
	if len(hs[0].Contradicting) != 0 || len(hs[0].Missing) != 1 || !strings.Contains(hs[0].Missing[0], "scrape interval") {
		t.Errorf("low observed peak should be missing evidence, not contradicting: %+v", hs[0])
	}

	// Null metrics (e.g. after a JSON round trip) add nothing.
	hs = Diagnoser{}.Diagnose(incident.Incident{}, []incident.Evidence{term("OOMKilled", 137), usage(nil, nil)})
	if hs[0].Score != 1 || len(hs[0].Missing) != 0 {
		t.Errorf("null metrics must not count either way: %+v", hs[0])
	}
}

func TestThrottlingSupportsProbeHypotheses(t *testing.T) {
	hs := Diagnoser{}.Diagnose(incident.Incident{}, []incident.Evidence{term("Error", 137), liveness(), usage(nil, 0.6)})
	if hs[0].Category != "liveness_probe_failure" || hs[0].Score != 3 {
		t.Errorf("throttling should add support to the liveness hypothesis: %+v", hs[0])
	}
	hs = Diagnoser{}.Diagnose(incident.Incident{}, []incident.Evidence{term("Error", 137), liveness(), usage(nil, 0.1)})
	if hs[0].Score != 2 {
		t.Errorf("light throttling is not evidence: %+v", hs[0])
	}
}

func TestCPUThrottlingRule(t *testing.T) {
	hs := Diagnoser{}.Diagnose(incident.Incident{}, []incident.Evidence{cond("CPUThrottled", "throttled in 80% of periods"), usage(nil, 0.8)})
	if hs[0].Category != "cpu_throttling" || hs[0].Score != 2 {
		t.Errorf("got %+v", hs[0])
	}
}

func TestOldGapIsNotMissingOnceObtained(t *testing.T) {
	hs := Diagnoser{}.Diagnose(incident.Incident{}, []incident.Evidence{term("OOMKilled", 137), noMetrics, usage(0.95, nil)})
	if len(hs[0].Missing) != 0 {
		t.Errorf("metrics were obtained in a later round; missing = %v", hs[0].Missing)
	}
}
