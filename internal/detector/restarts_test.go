package detector

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func pod(restarts int32, waiting string) *corev1.Pod {
	cs := corev1.ContainerStatus{
		Name:         "app",
		RestartCount: restarts,
		LastTerminationState: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1},
		},
	}
	switch waiting {
	case "":
		cs.State.Running = &corev1.ContainerStateRunning{}
		cs.Ready = true
	case "running-not-ready":
		cs.State.Running = &corev1.ContainerStateRunning{}
	default:
		cs.State.Waiting = &corev1.ContainerStateWaiting{Reason: waiting}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "crashloop-1", Namespace: "sim", UID: "uid-1"},
		Status:     corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{cs}},
	}
}

type step struct {
	at       time.Duration // offset from t0
	restarts int32
	waiting  string
	want     []SignalKind
}

func TestRestartDetector(t *testing.T) {
	tests := []struct {
		name  string
		steps []step
	}{
		{
			name: "restarts before watching are a baseline, not an incident",
			steps: []step{
				{at: 0, restarts: 50, want: nil},
				{at: time.Minute, restarts: 50, want: nil},
			},
		},
		{
			name: "slow mature crash loop fires on CrashLoopBackOff state",
			steps: []step{
				{at: 0, restarts: 39, waiting: "CrashLoopBackOff", want: []SignalKind{SignalTriggered}},
				{at: 5 * time.Minute, restarts: 40, waiting: "CrashLoopBackOff", want: nil},
			},
		},
		{
			name: "CrashLoopBackOff below threshold does not fire",
			steps: []step{
				{at: 0, restarts: 1, waiting: "CrashLoopBackOff", want: nil},
			},
		},
		{
			name: "fires once when threshold reached within window",
			steps: []step{
				{at: 0, restarts: 0},
				{at: 1 * time.Minute, restarts: 1},
				{at: 2 * time.Minute, restarts: 2},
				{at: 3 * time.Minute, restarts: 3, want: []SignalKind{SignalTriggered}},
				{at: 4 * time.Minute, restarts: 4, want: nil}, // deduplicated
			},
		},
		{
			name: "slow restarts spread beyond the window do not fire",
			steps: []step{
				{at: 0, restarts: 0},
				{at: 6 * time.Minute, restarts: 1},
				{at: 12 * time.Minute, restarts: 2},
				{at: 18 * time.Minute, restarts: 3},
			},
		},
		{
			name: "recovers after a full window with no restarts",
			steps: []step{
				{at: 0, restarts: 0},
				{at: 1 * time.Minute, restarts: 3, want: []SignalKind{SignalTriggered}},
				{at: 5 * time.Minute, restarts: 3, want: nil}, // still within window
				{at: 12 * time.Minute, restarts: 3, want: []SignalKind{SignalRecovered}},
				{at: 13 * time.Minute, restarts: 3, want: nil}, // recovery not repeated
			},
		},
		{
			name: "no recovery while the container is not ready",
			steps: []step{
				{at: 0, restarts: 0},
				{at: 1 * time.Minute, restarts: 3, want: []SignalKind{SignalTriggered}},
				{at: 12 * time.Minute, restarts: 3, waiting: "running-not-ready", want: nil},
				{at: 13 * time.Minute, restarts: 3, want: []SignalKind{SignalRecovered}},
			},
		},
		{
			name: "recurrence after recovery fires again",
			steps: []step{
				{at: 0, restarts: 0},
				{at: 1 * time.Minute, restarts: 3, want: []SignalKind{SignalTriggered}},
				{at: 12 * time.Minute, restarts: 3, want: []SignalKind{SignalRecovered}},
				{at: 13 * time.Minute, restarts: 6, want: []SignalKind{SignalTriggered}},
			},
		},
	}

	t0 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := NewRestartDetector(3, 10*time.Minute)
			for i, s := range tc.steps {
				got, err := d.Evaluate(context.Background(), DetectionInput{Now: t0.Add(s.at), Pod: pod(s.restarts, s.waiting)})
				if err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
				if len(got) != len(s.want) {
					t.Fatalf("step %d: got %d signals %+v, want %v", i, len(got), got, s.want)
				}
				for j := range got {
					if got[j].Kind != s.want[j] {
						t.Errorf("step %d: kind %s, want %s", i, got[j].Kind, s.want[j])
					}
				}
			}
		})
	}
}

func TestRestartDetectorEvidence(t *testing.T) {
	d := NewRestartDetector(3, 10*time.Minute)
	t0 := time.Now()
	ctx := context.Background()
	d.Evaluate(ctx, DetectionInput{Now: t0, Pod: pod(0, "")})
	got, _ := d.Evaluate(ctx, DetectionInput{Now: t0.Add(time.Minute), Pod: pod(4, "CrashLoopBackOff")})
	if len(got) != 1 {
		t.Fatalf("got %d signals, want 1", len(got))
	}
	s := got[0]
	if s.Severity != SeverityCritical || s.WaitingReason != "CrashLoopBackOff" || s.RestartDelta != 4 {
		t.Errorf("unexpected signal: %+v", s)
	}
	if s.Termination == nil || s.Termination.ExitCode != 1 || s.Termination.Reason != "Error" {
		t.Errorf("termination evidence missing: %+v", s.Termination)
	}
}

func TestRestartDetectorForgetsDeletedPods(t *testing.T) {
	d := NewRestartDetector(3, 10*time.Minute)
	ctx := context.Background()
	d.Evaluate(ctx, DetectionInput{Now: time.Now(), Pod: pod(0, "")})
	d.Evaluate(ctx, DetectionInput{Now: time.Now(), Pod: pod(0, ""), Deleted: true})
	if len(d.state) != 0 {
		t.Errorf("state not cleared: %d entries", len(d.state))
	}
}

func TestWorkload(t *testing.T) {
	yes := true
	p := pod(0, "")
	p.Labels = map[string]string{"pod-template-hash": "6d4f9c7b8"}
	p.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "crashloop-6d4f9c7b8", Controller: &yes}}
	if k, n := WorkloadOf(p); k != "Deployment" || n != "crashloop" {
		t.Errorf("deployment pod: got %s/%s", k, n)
	}
	p.OwnerReferences = []metav1.OwnerReference{{Kind: "StatefulSet", Name: "db", Controller: &yes}}
	if k, n := WorkloadOf(p); k != "StatefulSet" || n != "db" {
		t.Errorf("statefulset pod: got %s/%s", k, n)
	}
	p.OwnerReferences = nil
	if k, n := WorkloadOf(p); k != "Pod" || n != "crashloop-1" {
		t.Errorf("bare pod: got %s/%s", k, n)
	}
}
