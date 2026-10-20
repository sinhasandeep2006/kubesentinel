package kubernetes

import (
	"context"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestWatchPodsOnlySeesItsNamespace(t *testing.T) {
	cs := fake.NewClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "sim"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "other"}},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	var seen []string
	synced := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WatchPods(ctx, cs, "sim", 0, func(p *corev1.Pod, _ bool) {
			mu.Lock()
			seen = append(seen, p.Namespace+"/"+p.Name)
			mu.Unlock()
		}, func(get PodGetter) {
			if p, err := get("a"); err != nil || p.Name != "a" {
				t.Errorf("cache lookup: %v %v", p, err)
			}
			close(synced)
		})
	}()

	select {
	case <-synced:
	case <-ctx.Done():
		t.Fatal("cache never synced")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("WatchPods: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "sim/a" {
		t.Errorf("seen = %v, want [sim/a]", seen)
	}
}
