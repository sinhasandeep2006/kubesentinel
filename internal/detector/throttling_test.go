package detector

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func throttledPod(containers ...string) *corev1.Pod {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "sim", UID: "u"}}
	for _, c := range containers {
		p.Spec.Containers = append(p.Spec.Containers, corev1.Container{Name: c})
	}
	return p
}

func TestCPUThrottleDetector(t *testing.T) {
	single := throttledPod("app")
	multi := throttledPod("app", "sidecar")
	tests := []struct {
		name   string
		rounds [][]ThrottleSample
		want   []int
	}{
		{"fires once while throttled", [][]ThrottleSample{
			{{Pod: single, Ratio: 0.9}}, {{Pod: single, Ratio: 0.9}},
		}, []int{1, 0}},
		{"below threshold never fires", [][]ThrottleSample{
			{{Pod: single, Ratio: 0.3}},
		}, []int{0}},
		{"fires again after clearing", [][]ThrottleSample{
			{{Pod: single, Ratio: 0.9}}, {}, {{Pod: single, Ratio: 0.9}},
		}, []int{1, 0, 1}},
		{"dipping below threshold clears", [][]ThrottleSample{
			{{Pod: single, Ratio: 0.9}}, {{Pod: single, Ratio: 0.2}}, {{Pod: single, Ratio: 0.9}},
		}, []int{1, 0, 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := NewCPUThrottleDetector(10 * time.Minute)
			for i, r := range tc.rounds {
				if got := d.EvaluateSamples(c0, r); len(got) != tc.want[i] {
					t.Fatalf("round %d: got %d signals, want %d", i, len(got), tc.want[i])
				}
			}
		})
	}

	d := NewCPUThrottleDetector(10 * time.Minute)
	if s := d.EvaluateSamples(c0, []ThrottleSample{{Pod: single, Ratio: 0.9}}); s[0].Container != "app" || s[0].Reason != "CPUThrottled" {
		t.Errorf("pod-level series in a single-container pod should map to that container: %+v", s[0])
	}
	d = NewCPUThrottleDetector(10 * time.Minute)
	if s := d.EvaluateSamples(c0, []ThrottleSample{{Pod: multi, Ratio: 0.9}}); s[0].Container != "" {
		t.Errorf("pod-level series in a multi-container pod must stay pod-level: %+v", s[0])
	}
}
