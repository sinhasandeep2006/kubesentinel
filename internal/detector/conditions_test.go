package detector

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var c0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func withStatus(cs ...corev1.ContainerStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "sim", UID: "u"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: cs},
	}
}

func waiting(reason string) corev1.ContainerStatus {
	return corev1.ContainerStatus{Name: "app", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: "pull failed"}}}
}

func runningNotReady(since time.Time) corev1.ContainerStatus {
	return corev1.ContainerStatus{Name: "app", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(since)}}}
}

func healthy() corev1.ContainerStatus {
	return corev1.ContainerStatus{Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}
}

func oomKilled(at time.Time) corev1.ContainerStatus {
	cs := healthy()
	cs.RestartCount = 1
	cs.LastTerminationState.Terminated = &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137, FinishedAt: metav1.NewTime(at)}
	return cs
}

func unschedulable(since time.Time) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "sim", UID: "u"},
		Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{{
			Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable,
			Message: "0/1 nodes are available: 1 Insufficient memory.", LastTransitionTime: metav1.NewTime(since),
		}}},
	}
}

type obs struct {
	at   time.Duration
	pod  *corev1.Pod
	want int // number of triggered signals expected
}

func TestConditionDetectors(t *testing.T) {
	tests := []struct {
		name string
		det  Detector
		obs  []obs
	}{
		{"image pull fires after grace, once", NewImagePullDetector(time.Minute), []obs{
			{0, withStatus(waiting("ErrImagePull")), 0},
			{30 * time.Second, withStatus(waiting("ImagePullBackOff")), 0},
			{61 * time.Second, withStatus(waiting("ImagePullBackOff")), 1},
			{2 * time.Minute, withStatus(waiting("ImagePullBackOff")), 0},
		}},
		{"image pull that resolves within grace never fires", NewImagePullDetector(time.Minute), []obs{
			{0, withStatus(waiting("ErrImagePull")), 0},
			{30 * time.Second, withStatus(healthy()), 0},
			{2 * time.Minute, withStatus(healthy()), 0},
		}},
		{"image pull fires again after clearing and recurring", NewImagePullDetector(0), []obs{
			{0, withStatus(waiting("ErrImagePull")), 1},
			{time.Minute, withStatus(healthy()), 0},
			{2 * time.Minute, withStatus(waiting("ErrImagePull")), 1},
		}},
		{"CrashLoopBackOff is not an image problem", NewImagePullDetector(0), []obs{
			{0, withStatus(waiting("CrashLoopBackOff")), 0},
		}},
		{"unschedulable uses the scheduler's transition time", NewSchedulingDetector(time.Minute), []obs{
			{0, unschedulable(c0.Add(-5 * time.Minute)), 1},
		}},
		{"freshly unschedulable waits for grace", NewSchedulingDetector(time.Minute), []obs{
			{0, unschedulable(c0), 0},
			{61 * time.Second, unschedulable(c0), 1},
		}},
		{"not ready past grace fires", NewNotReadyDetector(2 * time.Minute), []obs{
			{0, withStatus(runningNotReady(c0)), 0},
			{3 * time.Minute, withStatus(runningNotReady(c0)), 1},
		}},
		{"briefly not ready while starting does not fire", NewNotReadyDetector(2 * time.Minute), []obs{
			{0, withStatus(runningNotReady(c0)), 0},
			{time.Minute, withStatus(healthy()), 0},
		}},
		{"recent OOM kill fires immediately", NewOOMDetector(10 * time.Minute), []obs{
			{0, withStatus(oomKilled(c0.Add(-time.Minute))), 1},
			{time.Minute, withStatus(oomKilled(c0.Add(-time.Minute))), 0},
		}},
		{"old OOM kill is history", NewOOMDetector(10 * time.Minute), []obs{
			{0, withStatus(oomKilled(c0.Add(-time.Hour))), 0},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for i, o := range tc.obs {
				got, err := tc.det.Evaluate(context.Background(), DetectionInput{Now: c0.Add(o.at), Pod: o.pod})
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != o.want {
					t.Fatalf("obs %d: got %d signals %+v, want %d", i, len(got), got, o.want)
				}
			}
		})
	}
}

func TestConditionSignalContent(t *testing.T) {
	got, _ := NewSchedulingDetector(0).Evaluate(context.Background(), DetectionInput{Now: c0, Pod: unschedulable(c0)})
	if len(got) != 1 {
		t.Fatalf("got %d signals", len(got))
	}
	s := got[0]
	if s.Container != "" || s.Reason != "Unschedulable" || s.Message == "" || s.Severity != SeverityCritical || s.WorkloadKind != "Pod" {
		t.Errorf("unexpected signal %+v", s)
	}

	got, _ = NewOOMDetector(10*time.Minute).Evaluate(context.Background(), DetectionInput{Now: c0, Pod: withStatus(oomKilled(c0))})
	if len(got) != 1 || got[0].Termination == nil || got[0].Termination.Reason != "OOMKilled" || got[0].Container != "app" {
		t.Errorf("OOM signal missing termination evidence: %+v", got)
	}
}

func TestConditionStateIsBounded(t *testing.T) {
	d := NewImagePullDetector(0).(*conditionDetector)
	ctx := context.Background()
	d.Evaluate(ctx, DetectionInput{Now: c0, Pod: withStatus(waiting("ErrImagePull"))})
	d.Evaluate(ctx, DetectionInput{Now: c0, Pod: withStatus(waiting("ErrImagePull")), Deleted: true})
	if len(d.first) != 0 || len(d.active) != 0 {
		t.Errorf("state not cleared after delete: %v %v", d.first, d.active)
	}
}
