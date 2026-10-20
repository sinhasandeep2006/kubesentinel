package detector

import (
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// ThrottleThreshold: a container throttled in at least this fraction of its CFS
// periods across the whole window is being held back by its CPU limit.
const ThrottleThreshold = 0.5

// ThrottleSample is one container's (or pod's) CPU throttling over the window,
// already resolved to its pod.
type ThrottleSample struct {
	Pod       *corev1.Pod
	Container string // empty when the runtime only exposes pod-level series
	Ratio     float64
	Periods   float64
}

// CPUThrottleDetector (rule D) fires when throttling stays at or above
// ThrottleThreshold averaged over the window, for a container that was actually
// busy (the caller filters out idle ones). High CPU usage alone never fires it.
// Unlike the pod-event detectors it consumes Prometheus samples, so it has its
// own entry point instead of Evaluate.
type CPUThrottleDetector struct {
	Window time.Duration

	mu     sync.Mutex
	active map[string]bool
}

func NewCPUThrottleDetector(window time.Duration) *CPUThrottleDetector {
	return &CPUThrottleDetector{Window: window, active: map[string]bool{}}
}

func (d *CPUThrottleDetector) Name() string { return "cpu-throttling" }

// EvaluateSamples takes the full current sample set for the watched namespace.
// Containers missing from it, or below the threshold, are no longer active.
func (d *CPUThrottleDetector) EvaluateSamples(now time.Time, samples []ThrottleSample) []Signal {
	d.mu.Lock()
	defer d.mu.Unlock()
	still := map[string]bool{}
	var out []Signal
	for _, s := range samples {
		if s.Pod == nil || s.Ratio < ThrottleThreshold {
			continue
		}
		container := s.Container
		// Pod-level series in a single-container pod can only mean that container.
		if container == "" && len(s.Pod.Spec.Containers) == 1 {
			container = s.Pod.Spec.Containers[0].Name
		}
		k := string(s.Pod.UID) + "/" + container
		still[k] = true
		if d.active[k] {
			continue
		}
		d.active[k] = true
		wk, wn := WorkloadOf(s.Pod)
		msg := fmt.Sprintf("throttled in %.0f%% of CFS periods over %s (%.0f busy periods)", s.Ratio*100, d.Window, s.Periods)
		out = append(out, Signal{
			Detector: d.Name(), Kind: SignalTriggered, Severity: SeverityWarning, ObservedAt: now,
			Namespace: s.Pod.Namespace, WorkloadKind: wk, WorkloadName: wn, Pod: s.Pod.Name, PodUID: string(s.Pod.UID),
			Container: container, Reason: "CPUThrottled", Message: msg, Window: d.Window,
			Summary: fmt.Sprintf("%s: CPU %s", nameOr(container, s.Pod.Name), msg),
		})
	}
	for k := range d.active {
		if !still[k] {
			delete(d.active, k)
		}
	}
	return out
}

func nameOr(container, pod string) string {
	if container != "" {
		return "container " + container
	}
	return "pod " + pod
}
