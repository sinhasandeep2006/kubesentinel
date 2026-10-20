// Package kubernetes holds all cluster access. It only reads: get, list, watch.
package kubernetes

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

// NewClientset builds a client from the default kubeconfig, pinned to kubeContext
// so we can never silently talk to a different cluster.
func NewClientset(kubeContext string) (kubernetes.Interface, error) {
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: kubeContext},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig context %q: %w", kubeContext, err)
	}
	cfg.Timeout = 30 * time.Second
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating clientset: %w", err)
	}
	return cs, nil
}

// PodGetter returns a pod by name from the informer cache.
type PodGetter func(name string) (*corev1.Pod, error)

// PodHandler receives every pod observation. deleted is true on removal.
type PodHandler func(pod *corev1.Pod, deleted bool)

// WatchPods runs a shared pod informer for one namespace until ctx is done.
// resync re-delivers every cached pod periodically so time-based rules
// (like recovery after a quiet window) are re-evaluated without new events.
// onSynced is called once the initial list is in the cache, with a lookup that
// reads pods from that cache (no API call).
func WatchPods(ctx context.Context, cs kubernetes.Interface, namespace string, resync time.Duration, h PodHandler, onSynced func(get PodGetter)) error {
	factory := informers.NewSharedInformerFactoryWithOptions(cs, resync, informers.WithNamespace(namespace))
	pods := factory.Core().V1().Pods()
	inf := pods.Informer()

	_, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { callPod(h, obj, false) },
		UpdateFunc: func(_, obj any) { callPod(h, obj, false) },
		DeleteFunc: func(obj any) {
			if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tomb.Obj
			}
			callPod(h, obj, true)
		},
	})
	if err != nil {
		return fmt.Errorf("adding pod handler: %w", err)
	}

	factory.Start(ctx.Done())
	defer factory.Shutdown()
	if !cache.WaitForCacheSync(ctx.Done(), inf.HasSynced) {
		return fmt.Errorf("pod cache sync in %s: %w", namespace, ctx.Err())
	}
	lister := pods.Lister().Pods(namespace)
	onSynced(lister.Get)
	<-ctx.Done()
	return nil
}

func callPod(h PodHandler, obj any, deleted bool) {
	if p, ok := obj.(*corev1.Pod); ok {
		h(p, deleted)
	}
}
