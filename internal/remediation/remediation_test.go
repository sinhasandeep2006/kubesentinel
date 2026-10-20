package remediation

import (
	"strings"
	"testing"

	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
)

func TestPlan(t *testing.T) {
	deploy := incident.Incident{ID: 1, Namespace: "sim", WorkloadKind: "Deployment", WorkloadName: "web", Container: "app"}
	sts := deploy
	sts.WorkloadKind = "StatefulSet"
	tests := []struct {
		name     string
		inc      incident.Incident
		category string
		want     []string
	}{
		{"app error on deployment", deploy, "application_error", []string{"rollout_undo", "fix_application"}},
		{"app error on statefulset has no rollout undo", sts, "application_error", []string{"fix_application"}},
		{"oom", deploy, "memory_exhaustion", []string{"increase_memory_limit"}},
		{"liveness", deploy, "liveness_probe_failure", []string{"relax_liveness_probe"}},
		{"exit zero", deploy, "normal_exit", []string{"fix_command_or_use_job"}},
		{"missing image", deploy, "image_not_found", []string{"fix_image_reference"}},
		{"registry auth", deploy, "image_registry_auth", []string{"configure_pull_secret"}},
		{"registry down: nothing to change in the workload", deploy, "image_registry_unreachable", nil},
		{"insufficient resources", deploy, "scheduling_insufficient_resources", []string{"lower_resource_requests"}},
		{"selector", deploy, "scheduling_placement_constraints", []string{"fix_node_selection"}},
		{"taints", deploy, "scheduling_taints", []string{"add_toleration"}},
		{"readiness", deploy, "readiness_probe_failure", []string{"fix_health_check"}},
		{"cpu throttling", deploy, "cpu_throttling", []string{"raise_cpu_limit"}},
		{"external kill: investigate, don't change", deploy, "external_termination", nil},
		{"inconclusive: no proposal", deploy, "inconclusive", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ps := Planner{KubeContext: "docker-desktop"}.Plan(tc.inc, []incident.Hypothesis{{ID: 9, Category: tc.category}})
			var got []string
			for _, p := range ps {
				got = append(got, p.ActionType)
				if p.Mode != incident.ModeSuggestionOnly {
					t.Errorf("%s: mode %q, want suggestion_only", p.ActionType, p.Mode)
				}
				if p.HypothesisID == nil || *p.HypothesisID != 9 || p.IncidentID != 1 {
					t.Errorf("%s: not linked to its hypothesis and incident", p.ActionType)
				}
				if p.Risk == "" || p.Rollback == "" || p.ExpectedOutcome == "" || len(p.Preconditions) == 0 || len(p.VerificationCriteria) == 0 || len(p.RequiredPermissions) == 0 {
					t.Errorf("%s: incomplete proposal %+v", p.ActionType, p)
				}
				if !strings.HasPrefix(p.SuggestedCommand, "kubectl --context docker-desktop ") {
					t.Errorf("%s: command not pinned to the context: %q", p.ActionType, p.SuggestedCommand)
				}
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("actions = %v, want %v", got, tc.want)
			}
		})
	}
}
