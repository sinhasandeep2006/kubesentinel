package detector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// RestartDetector fires when a container restarts at least Threshold times
// within Window, and recovers once a full Window passes with no new restarts.
// Rule A.
type RestartDetector struct {
	Threshold int32
	Window    time.Duration

	mu    sync.Mutex
	state map[string]*containerState // keyed by pod UID + container name
}

type sample struct {
	at    time.Time
	count int32
}

type containerState struct {
	samples []sample // oldest first, pruned to Window
	active  bool     // a triggered signal is outstanding
}

func NewRestartDetector(threshold int32, window time.Duration) *RestartDetector {
	return &RestartDetector{Threshold: threshold, Window: window, state: map[string]*containerState{}}
}

func (d *RestartDetector) Name() string { return "repeated-restarts" }

func (d *RestartDetector) Evaluate(_ context.Context, in DetectionInput) ([]Signal, error) {
	if in.Pod == nil {
		return nil, fmt.Errorf("restart detector: nil pod")
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if in.Deleted {
		// Forget deleted pods so state stays bounded. A replacement pod has a new UID.
		for _, cs := range in.Pod.Status.ContainerStatuses {
			delete(d.state, key(in.Pod, cs.Name))
		}
		return nil, nil
	}

	var out []Signal

	for _, cs := range in.Pod.Status.ContainerStatuses {
		k := key(in.Pod, cs.Name)
		st := d.state[k]
		if st == nil {
			st = &containerState{}
			d.state[k] = st
		}

		// The first sample is a baseline: restarts that happened before we
		// started watching are history, not a new incident.
		if n := len(st.samples); n == 0 || st.samples[n-1].count != cs.RestartCount {
			st.samples = append(st.samples, sample{at: in.Now, count: cs.RestartCount})
		}
		cutoff := in.Now.Add(-d.Window)
		st.prune(cutoff)

		delta := st.samples[len(st.samples)-1].count - st.samples[0].count
		quietForWindow := delta == 0 && !st.samples[0].at.After(cutoff)
		// Kubelet back-off grows to 5m, so a long-running crash loop restarts too
		// slowly to cross the delta threshold. Being in CrashLoopBackOff now is a
		// current fact, not history, so it triggers on its own.
		crashLooping := cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" && cs.RestartCount >= d.Threshold
		switch {
		case !st.active && (delta >= d.Threshold || crashLooping):
			st.active = true
			out = append(out, d.signal(in, cs, SignalTriggered, delta))
		case st.active && quietForWindow && cs.Ready && cs.State.Running != nil:
			st.active = false
			out = append(out, d.signal(in, cs, SignalRecovered, delta))
		}
	}
	return out, nil
}

// prune drops samples older than cutoff but always keeps the newest one before
// cutoff, so the delta covers the whole window rather than starting mid-window.
func (s *containerState) prune(cutoff time.Time) {
	i := 0
	for i+1 < len(s.samples) && !s.samples[i+1].at.After(cutoff) {
		i++
	}
	s.samples = s.samples[i:]
	// A single old sample means nothing changed in the window; re-anchor it at the cutoff.
	if len(s.samples) == 1 && s.samples[0].at.Before(cutoff) {
		s.samples[0].at = cutoff
	}
}

func (d *RestartDetector) signal(in DetectionInput, cs corev1.ContainerStatus, kind SignalKind, delta int32) Signal {
	wk, wn := WorkloadOf(in.Pod)
	s := Signal{
		Detector:     d.Name(),
		Kind:         kind,
		Severity:     SeverityWarning,
		ObservedAt:   in.Now,
		Namespace:    in.Pod.Namespace,
		WorkloadKind: wk,
		WorkloadName: wn,
		Pod:          in.Pod.Name,
		PodUID:       string(in.Pod.UID),
		Container:    cs.Name,
		RestartCount: cs.RestartCount,
		RestartDelta: delta,
		Window:       d.Window,
	}
	if w := cs.State.Waiting; w != nil {
		s.WaitingReason = w.Reason
	}
	if t := cs.LastTerminationState.Terminated; t != nil {
		s.Termination = &Termination{Reason: t.Reason, ExitCode: t.ExitCode, FinishedAt: t.FinishedAt.Time}
	}
	switch kind {
	case SignalTriggered:
		if s.WaitingReason == "CrashLoopBackOff" {
			s.Severity = SeverityCritical
		}
		if delta >= d.Threshold {
			s.Summary = fmt.Sprintf("container %s restarted %d times in %s", cs.Name, delta, d.Window)
		} else {
			s.Summary = fmt.Sprintf("container %s is in CrashLoopBackOff after %d restarts", cs.Name, cs.RestartCount)
		}
	case SignalRecovered:
		s.Summary = fmt.Sprintf("container %s running and ready with no restarts in the last %s", cs.Name, d.Window)
	}
	return s
}

func key(p *corev1.Pod, container string) string { return string(p.UID) + "/" + container }

// WorkloadOf names the pod's controller. A Deployment's pod is owned by a
// ReplicaSet named <deployment>-<pod-template-hash>.
func WorkloadOf(p *corev1.Pod) (kind, name string) {
	for _, o := range p.OwnerReferences {
		if o.Controller == nil || !*o.Controller {
			continue
		}
		if h := p.Labels["pod-template-hash"]; o.Kind == "ReplicaSet" && h != "" && strings.HasSuffix(o.Name, "-"+h) {
			return "Deployment", strings.TrimSuffix(o.Name, "-"+h)
		}
		return o.Kind, o.Name
	}
	return "Pod", p.Name
}
