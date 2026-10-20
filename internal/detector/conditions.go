package detector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// condition is one bad state observed on a pod or one of its containers.
type condition struct {
	container string // empty for pod-level conditions
	reason    string
	message   string
	since     time.Time // when the state began, if the API says; zero means "first observed"
	severity  Severity
}

// conditionDetector fires once a condition has persisted for Grace, and stays
// quiet until the condition clears (recovery itself is verified per workload
// by the incident manager). State is per pod UID + container and is dropped
// when the condition clears or the pod is deleted, so it stays bounded.
type conditionDetector struct {
	name  string
	grace time.Duration
	find  func(p *corev1.Pod, now time.Time) []condition

	mu     sync.Mutex
	first  map[string]time.Time
	active map[string]bool
}

func newConditionDetector(name string, grace time.Duration, find func(*corev1.Pod, time.Time) []condition) *conditionDetector {
	return &conditionDetector{name: name, grace: grace, find: find, first: map[string]time.Time{}, active: map[string]bool{}}
}

func (d *conditionDetector) Name() string { return d.name }

func (d *conditionDetector) Evaluate(_ context.Context, in DetectionInput) ([]Signal, error) {
	if in.Pod == nil {
		return nil, fmt.Errorf("%s detector: nil pod", d.name)
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	prefix := string(in.Pod.UID) + "/"
	var conds []condition
	if !in.Deleted {
		conds = d.find(in.Pod, in.Now)
	}
	present := map[string]bool{}
	var out []Signal
	for _, c := range conds {
		k := prefix + c.container
		present[k] = true
		if _, ok := d.first[k]; !ok {
			d.first[k] = in.Now
			if !c.since.IsZero() && c.since.Before(in.Now) {
				d.first[k] = c.since
			}
		}
		if d.active[k] || in.Now.Sub(d.first[k]) < d.grace {
			continue
		}
		d.active[k] = true
		out = append(out, d.signal(in, c, in.Now.Sub(d.first[k])))
	}
	// Forget this pod's conditions that have cleared.
	for k := range d.first {
		if strings.HasPrefix(k, prefix) && !present[k] {
			delete(d.first, k)
			delete(d.active, k)
		}
	}
	return out, nil
}

func (d *conditionDetector) signal(in DetectionInput, c condition, lasted time.Duration) Signal {
	wk, wn := WorkloadOf(in.Pod)
	s := Signal{
		Detector: d.name, Kind: SignalTriggered, Severity: c.severity, ObservedAt: in.Now,
		Namespace: in.Pod.Namespace, WorkloadKind: wk, WorkloadName: wn, Pod: in.Pod.Name, PodUID: string(in.Pod.UID),
		Container: c.container, Reason: c.reason, Message: c.message, Window: d.grace,
	}
	what := "pod " + in.Pod.Name
	if cs := containerStatus(in.Pod, c.container); cs != nil {
		what = "container " + c.container
		s.RestartCount = cs.RestartCount
		if cs.State.Waiting != nil {
			s.WaitingReason = cs.State.Waiting.Reason
		}
		if t := cs.LastTerminationState.Terminated; t != nil {
			s.Termination = &Termination{Reason: t.Reason, ExitCode: t.ExitCode, FinishedAt: t.FinishedAt.Time}
		}
	}
	s.Summary = fmt.Sprintf("%s: %s for %s", what, c.reason, lasted.Round(time.Second))
	return s
}

func containerStatus(p *corev1.Pod, name string) *corev1.ContainerStatus {
	if name == "" {
		return nil
	}
	for i := range p.Status.ContainerStatuses {
		if p.Status.ContainerStatuses[i].Name == name {
			return &p.Status.ContainerStatuses[i]
		}
	}
	return nil
}

var imagePullReasons = map[string]bool{
	"ErrImagePull": true, "ImagePullBackOff": true, "InvalidImageName": true, "ErrImageNeverPull": true,
}

// NewImagePullDetector: a container cannot start because its image cannot be
// pulled, for at least grace (rule H).
func NewImagePullDetector(grace time.Duration) Detector {
	return newConditionDetector("image-pull", grace, func(p *corev1.Pod, _ time.Time) []condition {
		var out []condition
		for _, cs := range p.Status.ContainerStatuses {
			if w := cs.State.Waiting; w != nil && imagePullReasons[w.Reason] {
				out = append(out, condition{container: cs.Name, reason: w.Reason, message: w.Message, severity: SeverityCritical})
			}
		}
		return out
	})
}

// NewSchedulingDetector: the pod has been unschedulable for at least grace,
// per the scheduler's own PodScheduled condition (rule F).
func NewSchedulingDetector(grace time.Duration) Detector {
	return newConditionDetector("unschedulable", grace, func(p *corev1.Pod, _ time.Time) []condition {
		if p.Status.Phase != corev1.PodPending || p.DeletionTimestamp != nil {
			return nil
		}
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == corev1.PodReasonUnschedulable {
				return []condition{{reason: "Unschedulable", message: c.Message, since: c.LastTransitionTime.Time, severity: SeverityCritical}}
			}
		}
		return nil
	})
}

// NewNotReadyDetector: a container has been running but not ready for at least
// grace (rule E, readiness side). Readiness failures remove the pod from Service
// endpoints; they do not restart it. Liveness failures restart the container
// and are caught by the restart detector instead.
func NewNotReadyDetector(grace time.Duration) Detector {
	return newConditionDetector("not-ready", grace, func(p *corev1.Pod, _ time.Time) []condition {
		if p.DeletionTimestamp != nil {
			return nil
		}
		var out []condition
		for _, cs := range p.Status.ContainerStatuses {
			if cs.State.Running == nil || cs.Ready {
				continue
			}
			reason := "NotReady"
			if cs.Started != nil && !*cs.Started {
				reason = "StartupProbeNotPassed"
			}
			out = append(out, condition{container: cs.Name, reason: reason, since: cs.State.Running.StartedAt.Time, severity: SeverityWarning})
		}
		return out
	})
}

// NewOOMDetector: a container was OOMKilled within the last window (rule C).
// It fires even when kills are too rare for the restart detector. The
// condition clears once the last OOM kill is older than window.
func NewOOMDetector(window time.Duration) Detector {
	return newConditionDetector("oom-killed", 0, func(p *corev1.Pod, now time.Time) []condition {
		var out []condition
		for _, cs := range p.Status.ContainerStatuses {
			t := cs.LastTerminationState.Terminated
			if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
				t = cs.State.Terminated
			}
			if t == nil || t.Reason != "OOMKilled" || now.Sub(t.FinishedAt.Time) > window {
				continue
			}
			out = append(out, condition{container: cs.Name, reason: "OOMKilled",
				message: fmt.Sprintf("exit code %d", t.ExitCode), since: t.FinishedAt.Time, severity: SeverityCritical})
		}
		return out
	})
}
