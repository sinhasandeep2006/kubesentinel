package diagnosis

import (
	"testing"

	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
)

func cond(reason, msg string) incident.Evidence {
	return incident.Evidence{ID: 20, Type: incident.EvidenceDetectorSignal, Availability: incident.Available,
		Payload: map[string]any{"reason": reason, "message": msg}}
}

func events(reason string, msgs ...string) incident.Evidence {
	var es []any
	for _, m := range msgs {
		es = append(es, map[string]any{"reason": reason, "message": m})
	}
	return incident.Evidence{ID: 21, Type: incident.EvidenceEvents, Availability: incident.Available, Payload: map[string]any{"events": es}}
}

func TestConditionRules(t *testing.T) {
	tests := []struct {
		name string
		ev   []incident.Evidence
		want string
	}{
		{"tag not found", []incident.Evidence{cond("ImagePullBackOff", "Back-off pulling image"),
			events("Failed", `Failed to pull image "busybox:nope": manifest unknown`)}, "image_not_found"},
		{"auth", []incident.Evidence{cond("ErrImagePull", "pull access denied, repository does not exist or may require authorization: unauthorized")}, "image_not_found"},
		{"auth only", []incident.Evidence{cond("ErrImagePull", "401 Unauthorized")}, "image_registry_auth"},
		{"network", []incident.Evidence{cond("ErrImagePull", "dial tcp: lookup registry.example: no such host")}, "image_registry_unreachable"},
		{"invalid name", []incident.Evidence{cond("InvalidImageName", "couldn't parse image reference")}, "image_reference_invalid"},
		{"unknown pull error", []incident.Evidence{cond("ImagePullBackOff", "")}, "image_pull_failure"},
		{"insufficient memory", []incident.Evidence{cond("Unschedulable", "0/1 nodes are available: 1 Insufficient memory.")}, "scheduling_insufficient_resources"},
		{"selector", []incident.Evidence{cond("Unschedulable", "0/1 nodes are available: 1 node(s) didn't match Pod's node affinity/selector.")}, "scheduling_placement_constraints"},
		{"taint", []incident.Evidence{cond("Unschedulable", "0/1 nodes are available: 1 node(s) had untolerated taint {x: y}.")}, "scheduling_taints"},
		{"readiness", []incident.Evidence{cond("NotReady", ""), events("Unhealthy", "Readiness probe failed: HTTP probe failed with statuscode: 503")}, "readiness_probe_failure"},
		{"startup", []incident.Evidence{cond("StartupProbeNotPassed", "")}, "startup_probe_failure"},
	}
	inc := incident.Incident{Namespace: "sim", Pod: "p", WorkloadKind: "Deployment", WorkloadName: "w"}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hs := Diagnoser{}.Diagnose(inc, tc.ev)
			if len(hs) == 0 || hs[0].Category != tc.want {
				t.Fatalf("top = %+v, want %s", hs, tc.want)
			}
			if len(hs[0].Supporting) == 0 || len(hs[0].NextSteps) == 0 {
				t.Errorf("hypothesis lacks supporting evidence or next steps: %+v", hs[0])
			}
		})
	}
}

func TestReadinessWithoutProbeEventsIsContradicted(t *testing.T) {
	hs := Diagnoser{}.Diagnose(incident.Incident{}, []incident.Evidence{cond("NotReady", "")})
	if len(hs[0].Contradicting) != 1 {
		t.Errorf("want a contradicting note when no probe events exist: %+v", hs[0])
	}
}

func TestMissingEvidenceIsDeduplicated(t *testing.T) {
	hs := Diagnoser{}.Diagnose(incident.Incident{}, []incident.Evidence{term("OOMKilled", 137), noMetrics, noMetrics})
	if len(hs[0].Missing) != 1 {
		t.Errorf("missing = %v, want one entry", hs[0].Missing)
	}
}
