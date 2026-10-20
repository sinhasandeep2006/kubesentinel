package collector

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/sinhasandeep2006/kubesentinel/internal/detector"
	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
)

func testPod(restarts int32, ready bool) *corev1.Pod {
	cs := corev1.ContainerStatus{Name: "app", RestartCount: restarts, Ready: ready}
	if ready {
		cs.State.Running = &corev1.ContainerStateRunning{}
	} else {
		cs.State.Waiting = &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "sim", UID: "u"},
		Status:     corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{cs}},
	}
}

func TestCollect(t *testing.T) {
	cs := fake.NewClientset(
		testPod(5, false),
		&corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "e1", Namespace: "sim"}, InvolvedObject: corev1.ObjectReference{UID: "u"}, Reason: "BackOff", Message: "Back-off restarting failed container"},
		&corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "e2", Namespace: "sim"}, InvolvedObject: corev1.ObjectReference{UID: "other"}, Reason: "Pulled"},
	)
	sig := detector.Signal{Namespace: "sim", Pod: "p", PodUID: "u", Container: "app", RestartCount: 5,
		Termination: &detector.Termination{Reason: "Error", ExitCode: 1}}

	ev := New(cs, nil, time.Minute).Collect(context.Background(), sig)
	byType := map[string]incident.Evidence{}
	for _, e := range ev {
		byType[e.Type] = e
	}

	if e := byType[incident.EvidenceTermination]; e.Availability != incident.Available || e.Payload["exit_code"] != int32(1) {
		t.Errorf("termination: %+v", e)
	}
	if e := byType[incident.EvidenceEvents]; e.Availability != incident.Available || len(e.Payload["events"].([]map[string]any)) != 1 {
		t.Errorf("events should contain only the pod's event: %+v", e)
	}
	if e := byType[incident.EvidenceMetrics]; e.Availability != incident.Unavailable || e.Detail == "" {
		t.Errorf("metrics must be explicitly unavailable: %+v", e)
	}
	if e := byType[incident.EvidencePreviousLogs]; e.Availability != incident.Available {
		t.Errorf("logs: %+v", e)
	}
}

func TestTerminationMissing(t *testing.T) {
	e := terminationEvidence(detector.Signal{})
	if e.Availability != incident.Unavailable || e.Detail == "" {
		t.Errorf("got %+v, want unavailable with reason", e)
	}
}

func TestRedact(t *testing.T) {
	tests := map[string]string{
		"connecting with password=hunter2 now": "connecting with password=[REDACTED] now",
		"Authorization: Bearer abc":            "Authorization: [REDACTED] abc",
		"API_KEY=xyz":                          "API_KEY=[REDACTED]",
		"plain line":                           "plain line",
	}
	for in, want := range tests {
		if got := redact(in); got != want {
			t.Errorf("redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func deployPod(name string, uptime time.Duration, ready bool, now time.Time) *corev1.Pod {
	yes := true
	cs := corev1.ContainerStatus{Name: "app", Ready: ready}
	if uptime > 0 {
		cs.State.Running = &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(now.Add(-uptime))}
	} else {
		cs.State.Waiting = &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "sim", UID: types.UID("uid-" + name),
			Labels:          map[string]string{"pod-template-hash": "abc"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "web-abc", Controller: &yes}}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{cs}},
	}
}

func TestVerifyWorkload(t *testing.T) {
	now := time.Now()
	inc := incident.Incident{Namespace: "sim", WorkloadKind: "Deployment", WorkloadName: "web", Container: "app"}
	other := testPod(0, false) // bare pod, different workload: must be ignored
	terminating := deployPod("old", 0, false, now)
	terminating.DeletionTimestamp = &metav1.Time{Time: now}

	tests := []struct {
		name string
		pods []*corev1.Pod
		want incident.Outcome
	}{
		{"all healthy for the window", []*corev1.Pod{deployPod("a", 11*time.Minute, true, now), deployPod("b", 20*time.Minute, true, now)}, incident.OutcomePassed},
		{"healthy but not for long enough", []*corev1.Pod{deployPod("a", 2*time.Minute, true, now)}, incident.OutcomeFailed},
		{"one pod still crashing", []*corev1.Pod{deployPod("a", 11*time.Minute, true, now), deployPod("b", 0, false, now)}, incident.OutcomeFailed},
		{"running but not ready", []*corev1.Pod{deployPod("a", 11*time.Minute, false, now)}, incident.OutcomeFailed},
		{"replacement healthy, old pod terminating", []*corev1.Pod{deployPod("new", 11*time.Minute, true, now), terminating}, incident.OutcomePassed},
		{"no pods left", []*corev1.Pod{other}, incident.OutcomeInconclusive},
	}
	pending := deployPod("pending", 0, false, now)
	pending.Status.ContainerStatuses = nil
	podLevel := inc
	podLevel.Container = ""
	for _, tc := range []struct {
		name string
		pods []*corev1.Pod
		want incident.Outcome
	}{
		{"pod-level: still pending", []*corev1.Pod{pending}, incident.OutcomeFailed},
		{"pod-level: scheduled and healthy", []*corev1.Pod{deployPod("a", 11*time.Minute, true, now)}, incident.OutcomePassed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objs := []runtime.Object{}
			for _, p := range tc.pods {
				objs = append(objs, p)
			}
			if v := New(fake.NewClientset(objs...), nil, 10*time.Minute).VerifyWorkload(context.Background(), podLevel); v.Outcome != tc.want {
				t.Errorf("outcome %s, want %s: %+v", v.Outcome, tc.want, v.Criteria)
			}
		})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			objs := []runtime.Object{}
			for _, p := range tc.pods {
				objs = append(objs, p)
			}
			v := New(fake.NewClientset(objs...), nil, 10*time.Minute).VerifyWorkload(context.Background(), inc)
			if v.Outcome != tc.want {
				t.Errorf("outcome %s, want %s: %s %+v", v.Outcome, tc.want, v.Summary, v.Criteria)
			}
		})
	}
}
