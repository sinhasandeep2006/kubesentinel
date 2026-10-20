// Package remediation turns diagnosed hypotheses into suggested fixes.
// It is suggestion-only: it produces text for a human and never touches the cluster.
package remediation

import (
	"fmt"
	"strings"

	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
)

type Planner struct {
	KubeContext string // used only to print commands for a human
}

// Plan returns proposals for each hypothesis that has a known, safe remedy.
// Hypotheses without one (inconclusive, external termination) get none: the
// right next step there is more investigation, which the hypothesis already lists.
func (p Planner) Plan(inc incident.Incident, hs []incident.Hypothesis) []incident.Proposal {
	kc := "kubectl"
	if p.KubeContext != "" {
		kc += " --context " + p.KubeContext
	}
	kind := strings.ToLower(inc.WorkloadKind)
	target := fmt.Sprintf("%s/%s/%s (container %s)", inc.Namespace, kind, inc.WorkloadName, inc.Container)
	ref := fmt.Sprintf("%s/%s -n %s", kind, inc.WorkloadName, inc.Namespace)
	healthy := []string{
		fmt.Sprintf("every pod of %s/%s has container %s running and ready for a full detection window", inc.WorkloadKind, inc.WorkloadName, inc.Container),
		"no new restarts after the change",
	}
	isDeployment := inc.WorkloadKind == "Deployment"

	var out []incident.Proposal
	for _, h := range hs {
		id := h.ID
		base := incident.Proposal{
			IncidentID: inc.ID, HypothesisID: &id, Target: target, Supporting: h.Supporting,
			VerificationCriteria: healthy, Mode: incident.ModeSuggestionOnly,
		}
		switch h.Category {
		case "application_error":
			if isDeployment {
				pr := base
				pr.ActionType = "rollout_undo"
				pr.Title = "Roll back to the previous revision"
				pr.Rationale = "If the failure started with the latest rollout, the previous revision is a known-good state."
				pr.Preconditions = []string{
					"the failure began after the most recent rollout (check rollout history)",
					"the previous revision is compatible with current data and dependencies",
				}
				pr.ExpectedOutcome = "pods run the previous image and configuration and stop crashing"
				pr.Risk = "medium"
				pr.RequiredPermissions = []string{"apps/deployments: patch"}
				pr.SuggestedCommand = fmt.Sprintf("%s rollout history %s && %s rollout undo %s", kc, ref, kc, ref)
				pr.Rollback = fmt.Sprintf("%s rollout undo %s --to-revision=<current revision>", kc, ref)
				out = append(out, pr)
			}
			pr := base
			pr.ActionType = "fix_application"
			pr.Title = "Fix the error reported in the crash logs"
			pr.Rationale = "The process exits with an error by itself; the cause is in the application, its configuration, or a dependency."
			pr.Preconditions = []string{"the error in the previous container's logs is understood"}
			pr.ExpectedOutcome = "the application starts and keeps running"
			pr.Risk = "low"
			pr.RequiredPermissions = []string{"whatever the normal deploy process needs"}
			pr.SuggestedCommand = fmt.Sprintf("%s logs %s -c %s --previous", kc, ref, inc.Container)
			pr.Rollback = "redeploy the previous version through the normal deploy process"
			out = append(out, pr)

		case "memory_exhaustion":
			pr := base
			pr.ActionType = "increase_memory_limit"
			pr.Title = "Raise the container's memory limit"
			pr.Rationale = "The container was OOMKilled at its limit. More headroom stops the kills if usage is legitimate; it does not fix a leak."
			pr.Preconditions = []string{
				"memory usage before the kill was near the limit (needs metrics; may be unavailable)",
				"the node has room for the higher limit",
				"usage is not growing without bound (a leak needs a code fix, not a bigger limit)",
			}
			pr.ExpectedOutcome = "no further OOMKilled terminations at normal load"
			pr.Risk = "medium"
			pr.RequiredPermissions = []string{"apps/deployments: patch"}
			pr.SuggestedCommand = fmt.Sprintf("%s set resources %s -c %s --limits=memory=<new limit>", kc, ref, inc.Container)
			pr.Rollback = fmt.Sprintf("%s set resources %s -c %s --limits=memory=<old limit>", kc, ref, inc.Container)
			out = append(out, pr)

		case "cpu_throttling":
			pr := base
			pr.ActionType = "raise_cpu_limit"
			pr.Title = "Raise the CPU limit to fit the workload's real demand"
			pr.Rationale = "The container is held back by its CPU limit most of the time, which delays its work."
			pr.Preconditions = []string{
				"the CPU demand is legitimate (not a busy loop or runaway work)",
				"the node has spare CPU for the higher limit",
			}
			pr.ExpectedOutcome = "throttling drops below 50% of periods and latency improves"
			pr.Risk = "low"
			pr.RequiredPermissions = []string{"apps/deployments: patch"}
			pr.SuggestedCommand = fmt.Sprintf("%s set resources %s -c %s --limits=cpu=<new limit>", kc, ref, inc.Container)
			pr.Rollback = fmt.Sprintf("%s set resources %s -c %s --limits=cpu=<old limit>", kc, ref, inc.Container)
			out = append(out, pr)

		case "liveness_probe_failure":
			pr := base
			pr.ActionType = "relax_liveness_probe"
			pr.Title = "Give the liveness probe more time"
			pr.Rationale = "The kubelet restarts the container when the liveness probe fails. If the app is healthy but slow, the probe is too strict."
			pr.Preconditions = []string{
				"the application is actually healthy when the probe fails (otherwise fix the app)",
				"startup time or response latency explains the failures",
			}
			pr.ExpectedOutcome = "no more liveness-triggered restarts"
			pr.Risk = "low"
			pr.RequiredPermissions = []string{"apps/deployments: patch"}
			pr.SuggestedCommand = fmt.Sprintf("%s edit %s   # raise livenessProbe timeoutSeconds / failureThreshold, or add a startupProbe", kc, ref)
			pr.Rollback = fmt.Sprintf("%s rollout undo %s", kc, ref)
			out = append(out, pr)

		case "image_not_found", "image_reference_invalid":
			pr := base
			pr.ActionType = "fix_image_reference"
			pr.Title = "Point the container at an image that exists"
			pr.Rationale = "The kubelet cannot pull the configured image reference."
			pr.Preconditions = []string{"the corrected image:tag exists in the registry (check it first)"}
			pr.ExpectedOutcome = "the image pulls and the container starts"
			pr.Risk = "low"
			pr.RequiredPermissions = []string{"apps/deployments: patch"}
			pr.SuggestedCommand = fmt.Sprintf("%s set image %s %s=<image>:<tag>", kc, ref, inc.Container)
			pr.Rollback = fmt.Sprintf("%s rollout undo %s", kc, ref)
			out = append(out, pr)

		case "image_registry_auth":
			pr := base
			pr.ActionType = "configure_pull_secret"
			pr.Title = "Give the workload valid registry credentials"
			pr.Rationale = "The registry refused the pull; the pod needs an imagePullSecret it can use."
			pr.Preconditions = []string{"you have credentials with read access to this image", "the secret is created in the same namespace"}
			pr.ExpectedOutcome = "the image pulls and the container starts"
			pr.Risk = "medium"
			pr.RequiredPermissions = []string{"core/secrets: create", "apps/deployments: patch"}
			pr.SuggestedCommand = fmt.Sprintf("%s create secret docker-registry <name> -n %s --docker-server=<registry> --docker-username=<user> --docker-password=<token>   # then add it under spec.template.spec.imagePullSecrets", kc, inc.Namespace)
			pr.Rollback = fmt.Sprintf("remove the imagePullSecrets entry and %s delete secret <name> -n %s", kc, inc.Namespace)
			out = append(out, pr)

		case "scheduling_insufficient_resources":
			pr := base
			pr.ActionType = "lower_resource_requests"
			pr.Title = "Lower the pod's resource requests to what it actually uses"
			pr.Rationale = "No node has room for the requested CPU or memory. Requests are often set far above real usage."
			pr.Preconditions = []string{
				"real usage is known to be below the new request (needs metrics; may be unavailable)",
				"the alternative, adding node capacity, is not the better fix",
			}
			pr.ExpectedOutcome = "the scheduler places the pod"
			pr.Risk = "medium"
			pr.RequiredPermissions = []string{"apps/deployments: patch"}
			pr.SuggestedCommand = fmt.Sprintf("%s set resources %s --requests=cpu=<cpu>,memory=<memory>", kc, ref)
			pr.Rollback = fmt.Sprintf("%s rollout undo %s", kc, ref)
			out = append(out, pr)

		case "scheduling_placement_constraints":
			pr := base
			pr.ActionType = "fix_node_selection"
			pr.Title = "Fix the node selector or affinity, or label the intended nodes"
			pr.Rationale = "The pod's placement rules exclude every node."
			pr.Preconditions = []string{"you know which nodes the pod is meant to run on"}
			pr.ExpectedOutcome = "the scheduler places the pod on a matching node"
			pr.Risk = "low"
			pr.RequiredPermissions = []string{"apps/deployments: patch"}
			pr.SuggestedCommand = fmt.Sprintf("%s get nodes --show-labels && %s edit %s   # nodeSelector / affinity", kc, kc, ref)
			pr.Rollback = fmt.Sprintf("%s rollout undo %s", kc, ref)
			out = append(out, pr)

		case "scheduling_taints":
			pr := base
			pr.ActionType = "add_toleration"
			pr.Title = "Add a toleration only if the pod belongs on the tainted nodes"
			pr.Rationale = "Every node carries a taint the pod does not tolerate. Taints usually reserve nodes on purpose."
			pr.Preconditions = []string{"the taint is not protecting those nodes from exactly this kind of workload"}
			pr.ExpectedOutcome = "the scheduler places the pod"
			pr.Risk = "medium"
			pr.RequiredPermissions = []string{"apps/deployments: patch"}
			pr.SuggestedCommand = fmt.Sprintf("%s describe nodes | grep -i taints && %s edit %s   # spec.template.spec.tolerations", kc, kc, ref)
			pr.Rollback = fmt.Sprintf("%s rollout undo %s", kc, ref)
			out = append(out, pr)

		case "readiness_probe_failure", "startup_probe_failure":
			pr := base
			pr.ActionType = "fix_health_check"
			pr.Title = "Fix what the health check depends on, or correct the probe"
			pr.Rationale = "The probe keeps failing. Either the app is genuinely unhealthy, or the probe checks the wrong thing or gives up too soon."
			pr.Preconditions = []string{"the probe path, port, and command match what the app serves"}
			pr.ExpectedOutcome = "the container becomes ready and receives traffic"
			pr.Risk = "low"
			pr.RequiredPermissions = []string{"apps/deployments: patch"}
			pr.SuggestedCommand = fmt.Sprintf("%s edit %s   # readinessProbe / startupProbe", kc, ref)
			pr.Rollback = fmt.Sprintf("%s rollout undo %s", kc, ref)
			out = append(out, pr)

		case "normal_exit":
			pr := base
			pr.ActionType = "fix_command_or_use_job"
			pr.Title = "Keep the process running, or run it as a Job"
			pr.Rationale = "The process finishes successfully and Kubernetes restarts it. A one-shot task belongs in a Job; a server's entrypoint should not return."
			pr.Preconditions = []string{"confirm whether the workload is meant to run continuously"}
			pr.ExpectedOutcome = "no restarts after successful completion"
			pr.Risk = "low"
			pr.RequiredPermissions = []string{"whatever the normal deploy process needs"}
			pr.SuggestedCommand = fmt.Sprintf("%s get %s -o yaml   # check command and args", kc, ref)
			pr.Rollback = "redeploy the previous version through the normal deploy process"
			out = append(out, pr)
		}
	}
	return out
}
