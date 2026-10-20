// Package detector turns observed workload state into signals.
// Detectors only read their input; they never touch the cluster.
package detector

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// Detector evaluates one observation and returns any signals it supports.
type Detector interface {
	Name() string
	Evaluate(ctx context.Context, input DetectionInput) ([]Signal, error)
}

// DetectionInput is one observation of a pod at a point in time.
type DetectionInput struct {
	Now     time.Time
	Pod     *corev1.Pod
	Deleted bool
}

type SignalKind string

const (
	SignalTriggered SignalKind = "triggered"
	SignalRecovered SignalKind = "recovered"
)

type Severity string

const (
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Termination is the container's last observed termination, as reported by the kubelet.
type Termination struct {
	Reason     string
	ExitCode   int32
	FinishedAt time.Time
}

// Signal is an observed fact, not a diagnosis.
type Signal struct {
	Detector     string
	Kind         SignalKind
	Severity     Severity
	ObservedAt   time.Time
	Namespace    string
	WorkloadKind string // e.g. Deployment; "Pod" for bare pods
	WorkloadName string
	Pod          string
	PodUID       string
	Container    string
	Summary      string

	// Condition detectors (image pull, scheduling, readiness, OOM) set these.
	// Container is empty for pod-level conditions such as scheduling.
	Reason  string
	Message string

	RestartCount  int32
	RestartDelta  int32
	Window        time.Duration
	WaitingReason string       // e.g. CrashLoopBackOff; empty if not waiting
	Termination   *Termination // nil if the kubelet reported none
}
