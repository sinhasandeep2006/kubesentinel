package diagnosis

import (
	"fmt"
	"strings"

	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
)

type cmds struct {
	kc, describe, workload string
	inc                    incident.Incident
}

// matches reports whether any of texts contains any of words (case-insensitive).
func matches(texts []string, words ...string) bool {
	for _, t := range texts {
		t = strings.ToLower(t)
		for _, w := range words {
			if strings.Contains(t, w) {
				return true
			}
		}
	}
	return false
}

// conditionRules explains problems found by the condition detectors: image
// pulls, scheduling, and readiness. Each sub-cause is chosen from the kubelet's
// or scheduler's own message text, never guessed.
func conditionRules(f facts, c cmds, add func(incident.Hypothesis)) {
	texts := append([]string{f.condMessage}, f.failed...)
	eventsRef := func(note string) []incident.EvidenceRef {
		if f.events == nil || len(f.failed) == 0 {
			return nil
		}
		return []incident.EvidenceRef{ref(f.events, note)}
	}

	switch f.condReason {
	case "ErrImagePull", "ImagePullBackOff", "InvalidImageName", "ErrImageNeverPull":
		base := []incident.EvidenceRef{ref(f.cond, "container waiting: "+f.condReason)}
		next := []string{c.describe + "   # the Events section shows the exact pull error"}
		var h incident.Hypothesis
		switch {
		case f.condReason == "InvalidImageName":
			h = incident.Hypothesis{Category: "image_reference_invalid", Title: "The image reference is malformed",
				Rationale: "The kubelet reports InvalidImageName: the image string itself cannot be parsed."}
		case matches(texts, "not found", "manifest unknown", "does not exist", "no such image"):
			h = incident.Hypothesis{Category: "image_not_found", Title: "The image or tag does not exist in the registry",
				Rationale: "The pull error says the image or tag was not found.",
				Alternatives: []string{"typo in the image name or tag", "tag not pushed yet", "wrong registry",
					"private repository: some registries answer 'not found' instead of 'unauthorized'"}}
		case matches(texts, "unauthorized", "denied", "authentication required", "forbidden"):
			h = incident.Hypothesis{Category: "image_registry_auth", Title: "The registry rejected the pull credentials",
				Rationale:    "The pull error says access was denied or unauthorized.",
				Alternatives: []string{"missing or expired imagePullSecret", "private image referenced by mistake"}}
		case matches(texts, "timeout", "dial tcp", "no such host", "i/o timeout", "connection refused"):
			h = incident.Hypothesis{Category: "image_registry_unreachable", Title: "The registry cannot be reached from the node",
				Rationale:    "The pull error is a network failure.",
				Alternatives: []string{"registry outage", "DNS or proxy problem on the node", "rate limiting"}}
		default:
			h = incident.Hypothesis{Category: "image_pull_failure", Title: "The image cannot be pulled",
				Rationale:    "The container is waiting with " + f.condReason + ", but the error text does not say why.",
				Alternatives: []string{"image or tag does not exist", "registry credentials", "registry unreachable"}}
		}
		h.Supporting = append(base, eventsRef("pull error events")...)
		h.NextSteps = next
		add(h)

	case "Unschedulable":
		base := []incident.EvidenceRef{ref(f.cond, "PodScheduled=False: "+f.condMessage)}
		next := []string{c.describe + "   # scheduler message per node", c.kc + " describe nodes   # allocatable vs requested, taints"}
		var h incident.Hypothesis
		switch {
		case matches(texts, "insufficient cpu", "insufficient memory", "insufficient ephemeral-storage", "insufficient"):
			h = incident.Hypothesis{Category: "scheduling_insufficient_resources", Title: "No node has enough free resources for the pod's requests",
				Rationale:    "The scheduler reports insufficient resources on every node.",
				Alternatives: []string{"requests set far higher than needed", "cluster genuinely full", "other pods over-requesting"}}
		case matches(texts, "node affinity", "node selector", "didn't match pod's node"):
			h = incident.Hypothesis{Category: "scheduling_placement_constraints", Title: "No node matches the pod's node selector or affinity",
				Rationale:    "The scheduler reports that node selection rules exclude every node.",
				Alternatives: []string{"label missing on the intended nodes", "selector typo"}}
		case matches(texts, "taint", "untolerated"):
			h = incident.Hypothesis{Category: "scheduling_taints", Title: "Every node has a taint the pod does not tolerate",
				Rationale: "The scheduler reports untolerated taints."}
		case matches(texts, "persistentvolumeclaim", "volume", "pvc"):
			h = incident.Hypothesis{Category: "scheduling_storage", Title: "A volume the pod needs is not available",
				Rationale: "The scheduler reports a volume or claim problem."}
		default:
			h = incident.Hypothesis{Category: "scheduling_failure", Title: "The scheduler cannot place the pod",
				Rationale: "The pod is Unschedulable; the message does not match a known cause: " + f.condMessage}
		}
		h.Supporting = append(base, eventsRef("FailedScheduling events")...)
		h.NextSteps = next
		add(h)

	case "CPUThrottled":
		h := incident.Hypothesis{
			Category: "cpu_throttling",
			Title:    "The CPU limit is throttling the workload",
			Rationale: "The container hit its CPU limit in most scheduling periods across the window. High CPU on its own is " +
				"not a problem; being throttled means work is being delayed.",
			Supporting:   []incident.EvidenceRef{ref(f.cond, f.condMessage)},
			Alternatives: []string{"CPU limit set too low for normal load", "load spike", "busy loop or runaway work in the app"},
			NextSteps:    []string{c.describe + "   # check CPU requests/limits"},
		}
		if f.throttle != nil && *f.throttle >= throttleThreshold {
			h.Supporting = append(h.Supporting, ref(f.metrics, fmt.Sprintf("%.0f%% of CFS periods throttled", *f.throttle*100)))
		}
		add(h)

	case "NotReady", "StartupProbeNotPassed":
		h := incident.Hypothesis{
			Category: "readiness_probe_failure",
			Title:    "The readiness probe keeps failing, so the pod receives no traffic",
			Rationale: "The container is running but not ready. Readiness failures remove the pod from Service endpoints; " +
				"unlike liveness failures they do not restart it.",
			Supporting:   []incident.EvidenceRef{ref(f.cond, "running but not ready")},
			Alternatives: []string{"dependency the health check relies on is down", "probe path or port is wrong", "app still warming up"},
			NextSteps:    []string{c.describe + "   # probe definition and Unhealthy events"},
		}
		if f.condReason == "StartupProbeNotPassed" || f.startup > 0 {
			h.Category = "startup_probe_failure"
			h.Title = "The startup probe has not passed yet"
			h.Rationale = "The container is running but its startup probe has not succeeded; readiness and liveness checks wait for it."
		}
		if r := throttledRef(f); r != nil {
			h.Supporting = append(h.Supporting, *r)
		}
		if f.readiness+f.startup > 0 {
			h.Supporting = append(h.Supporting, ref(f.events, fmt.Sprintf("%d probe failure events", f.readiness+f.startup)))
		} else {
			h.Contradicting = append(h.Contradicting, incident.EvidenceRef{Note: "no probe failure events found; the container may have no readiness probe and be waiting on something else"})
		}
		add(h)
	}
}
