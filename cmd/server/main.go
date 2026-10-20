package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/sinhasandeep2006/kubesentinel/internal/api"
	"github.com/sinhasandeep2006/kubesentinel/internal/collector"
	"github.com/sinhasandeep2006/kubesentinel/internal/detector"
	"github.com/sinhasandeep2006/kubesentinel/internal/diagnosis"
	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
	"github.com/sinhasandeep2006/kubesentinel/internal/kubernetes"
	"github.com/sinhasandeep2006/kubesentinel/internal/remediation"
	"github.com/sinhasandeep2006/kubesentinel/internal/storage"
	"github.com/sinhasandeep2006/kubesentinel/internal/telemetry"
)

const defaultDatabaseURL = "postgres://kubesentinel:kubesentinel@127.0.0.1:5432/kubesentinel?sslmode=disable"

func main() {
	if err := run(); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := env("KS_ADDR", ":8080")
	kubeContext := env("KS_KUBE_CONTEXT", "docker-desktop")
	namespace := env("KS_NAMESPACE", "kubesentinel-sim")

	store, err := storage.Open(ctx, env("KS_DATABASE_URL", defaultDatabaseURL))
	if err != nil {
		return err
	}
	defer store.Close()
	if n, err := store.MigrateUp(ctx); err != nil {
		return err
	} else if n > 0 {
		slog.Info("applied migrations", "count", n)
	}

	cs, err := kubernetes.NewClientset(kubeContext)
	if err != nil {
		return err
	}

	window, err := time.ParseDuration(env("KS_RESTART_WINDOW", "10m"))
	if err != nil {
		return fmt.Errorf("parsing KS_RESTART_WINDOW: %w", err)
	}
	// Grace periods: how long a condition must persist before it counts.
	detectors := []detector.Detector{
		detector.NewRestartDetector(3, window),
		detector.NewOOMDetector(window),
		detector.NewImagePullDetector(time.Minute),
		detector.NewSchedulingDetector(time.Minute),
		detector.NewNotReadyDetector(2 * time.Minute),
	}
	// Prometheus is optional. Without it, metrics evidence is marked unavailable
	// and throttling detection is off; nothing pretends usage was zero.
	var prom *telemetry.Prometheus
	if u := os.Getenv("KS_PROMETHEUS_URL"); u != "" {
		prom = telemetry.New(u)
	}
	throttling := detector.NewCPUThrottleDetector(window)
	coll := collector.New(cs, prom, window)
	mgr := incident.NewManager(store, coll, diagnosis.Diagnoser{KubeContext: kubeContext}, remediation.Planner{KubeContext: kubeContext}, coll)

	onPod := func(p *corev1.Pod, deleted bool) {
		in := detector.DetectionInput{Now: time.Now(), Pod: p, Deleted: deleted}
		for _, d := range detectors {
			sigs, err := d.Evaluate(ctx, in)
			if err != nil {
				slog.Error("evaluating pod", "detector", d.Name(), "pod", p.Namespace+"/"+p.Name, "err", err)
				continue
			}
			for _, s := range sigs {
				slog.Info("signal", "detector", s.Detector, "kind", s.Kind, "pod", s.Namespace+"/"+s.Pod, "container", s.Container, "summary", s.Summary)
				mgr.Handle(ctx, s)
			}
		}
	}

	var synced atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !synced.Load() {
			http.Error(w, "pod cache not synced", http.StatusServiceUnavailable)
			return
		}
		if err := store.Ping(r.Context()); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	api.Register(mux, store, func(r *http.Request) []api.Source {
		k8s := api.Source{Name: "kubernetes", Status: "available", Detail: "context " + kubeContext + ", namespace " + namespace}
		if !synced.Load() {
			k8s.Status, k8s.Detail = "degraded", "pod cache not synced"
		}
		return []api.Source{
			k8s,
			{Name: "events", Status: k8s.Status},
			{Name: "logs", Status: k8s.Status, Detail: "previous container logs, last 20 lines"},
			promSource(r.Context(), prom),
		}
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	var wg sync.WaitGroup
	wg.Go(func() { mgr.Run(ctx, 30*time.Second) })

	errc := make(chan error, 2)
	go func() {
		slog.Info("listening", "addr", addr)
		errc <- srv.ListenAndServe()
	}()
	go func() {
		slog.Info("watching pods", "context", kubeContext, "namespace", namespace)
		errc <- kubernetes.WatchPods(ctx, cs, namespace, 30*time.Second, onPod, func(get kubernetes.PodGetter) {
			if prom != nil {
				wg.Go(func() { watchThrottling(ctx, prom, throttling, get, namespace, window, mgr.Handle) })
			}
			synced.Store(true)
			slog.Info("pod cache synced")
		})
	}()

	var runErr error
	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = err
	}
	wg.Wait()
	return runErr
}

func promSource(ctx context.Context, prom *telemetry.Prometheus) api.Source {
	src := api.Source{Name: "prometheus", Status: "unavailable", Detail: "not configured (set KS_PROMETHEUS_URL)"}
	if prom == nil {
		return src
	}
	if err := prom.Ready(ctx); err != nil {
		src.Detail = "configured but not reachable: " + err.Error()
		return src
	}
	src.Status, src.Detail = "available", "cAdvisor container metrics; pod-level only where the runtime exposes no container series"
	return src
}

// watchThrottling polls Prometheus once a minute for CPU throttling in the
// namespace and feeds the throttling detector. A failed poll is logged and
// skipped: no data is not "not throttled", so active state is left untouched.
func watchThrottling(ctx context.Context, prom *telemetry.Prometheus, d *detector.CPUThrottleDetector,
	get kubernetes.PodGetter, namespace string, window time.Duration, handle func(context.Context, detector.Signal)) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	// A container runnable in at least 10% of the window's 100ms CFS periods counts as busy.
	minPeriods := window.Seconds()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		ts, err := prom.Throttling(ctx, namespace, window, minPeriods)
		if err != nil {
			slog.Warn("polling CPU throttling", "err", err)
			continue
		}
		samples := make([]detector.ThrottleSample, 0, len(ts))
		for _, t := range ts {
			p, err := get(t.Pod)
			if err != nil {
				continue // pod already gone from the cache
			}
			samples = append(samples, detector.ThrottleSample{Pod: p, Container: t.Container, Ratio: t.Ratio, Periods: t.Periods})
		}
		for _, s := range d.EvaluateSamples(time.Now(), samples) {
			slog.Info("signal", "detector", s.Detector, "kind", s.Kind, "pod", s.Namespace+"/"+s.Pod, "container", s.Container, "summary", s.Summary)
			handle(ctx, s)
		}
	}
}
