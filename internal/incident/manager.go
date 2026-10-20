package incident

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/sinhasandeep2006/kubesentinel/internal/detector"
)

// Key identifies the thing an incident is about: one container of one workload
// (Container is empty for pod-level problems such as scheduling). Pods come and
// go; the workload persists. The detector is deliberately not part of the key:
// an OOM crash loop trips both the restart and OOM detectors, and that is one
// incident with more evidence, not two incidents.
type Key struct {
	Namespace, WorkloadKind, WorkloadName, Container string
}

func (i Incident) Key() Key {
	return Key{i.Namespace, i.WorkloadKind, i.WorkloadName, i.Container}
}

func keyOf(s detector.Signal) Key {
	return Key{s.Namespace, s.WorkloadKind, s.WorkloadName, s.Container}
}

// Store is the persistence the manager needs.
type Store interface {
	// LatestIncident returns the newest non-dismissed incident for the key, or nil.
	LatestIncident(ctx context.Context, k Key) (*Incident, error)
	CreateIncident(ctx context.Context, inc *Incident) error // starts in StatusDetected, sets ID
	Transition(ctx context.Context, id int64, from, to Status, reason string, at time.Time) error
	AddEvidence(ctx context.Context, incidentID int64, ev []Evidence) ([]Evidence, error)           // returns ev with IDs
	ReplaceHypotheses(ctx context.Context, incidentID int64, hs []Hypothesis) ([]Hypothesis, error) // returns hs with IDs
	ReplaceProposals(ctx context.Context, incidentID int64, ps []Proposal) error
	ListEvidence(ctx context.Context, incidentID int64) ([]Evidence, error)
	AddVerification(ctx context.Context, v *Verification) error
	OpenIncidents(ctx context.Context) ([]Incident, error)
}

// Collector gathers evidence for a signal. Sources that fail are returned as
// evidence with Availability Unavailable or Error, never dropped.
type Collector interface {
	Collect(ctx context.Context, sig detector.Signal) []Evidence
}

type Diagnoser interface {
	Diagnose(inc Incident, ev []Evidence) []Hypothesis
}

// Planner suggests remediation for diagnosed hypotheses. Suggestions only.
type Planner interface {
	Plan(inc Incident, hs []Hypothesis) []Proposal
}

// Verifier independently re-reads the cluster to decide whether the incident's
// workload has recovered: Passed, Failed (not yet), or Inconclusive (no pods left).
type Verifier interface {
	VerifyWorkload(ctx context.Context, inc Incident) Verification
}

type Manager struct {
	store    Store
	collect  Collector
	diagnose Diagnoser
	plan     Planner
	verify   Verifier
	jobs     chan detector.Signal
	now      func() time.Time
}

func NewManager(s Store, c Collector, d Diagnoser, p Planner, v Verifier) *Manager {
	return &Manager{store: s, collect: c, diagnose: d, plan: p, verify: v, jobs: make(chan detector.Signal, 64), now: time.Now}
}

// Handle queues a signal. It blocks when the queue is full (backpressure on the
// informer) rather than dropping signals.
func (m *Manager) Handle(ctx context.Context, sig detector.Signal) {
	select {
	case m.jobs <- sig:
	case <-ctx.Done():
	}
}

// Run processes signals one at a time, and checks open incidents for recovery
// every reconcileEvery, until ctx is done.
func (m *Manager) Run(ctx context.Context, reconcileEvery time.Duration) {
	tick := time.NewTicker(reconcileEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-m.jobs:
			if err := m.process(ctx, sig); err != nil {
				slog.Error("processing signal", "kind", sig.Kind, "pod", sig.Namespace+"/"+sig.Pod, "container", sig.Container, "err", err)
			}
		case <-tick.C:
			if err := m.Reconcile(ctx); err != nil {
				slog.Error("reconciling incidents", "err", err)
			}
		}
	}
}

func (m *Manager) process(ctx context.Context, sig detector.Signal) error {
	if sig.Kind != detector.SignalTriggered {
		return nil // recovery is decided by Reconcile at workload level, not per pod
	}
	inc, err := m.store.LatestIncident(ctx, keyOf(sig))
	if err != nil {
		return err
	}
	switch {
	case inc != nil && inc.Status.Open():
		// Deduplicated: another detector, or another pod of the same workload,
		// reports the same problem. Keep its evidence and, once diagnosed, re-rank
		// the hypotheses on everything known so far.
		if _, err := m.store.AddEvidence(ctx, inc.ID, m.collect.Collect(ctx, sig)); err != nil {
			return err
		}
		if inc.Status != StatusDiagnosed {
			return nil
		}
		all, err := m.store.ListEvidence(ctx, inc.ID)
		if err != nil {
			return err
		}
		_, err = m.diagnoseAndPlan(ctx, inc, all)
		return err
	case inc != nil: // resolved: the failure recurred
		if err := m.move(ctx, inc, StatusReopened, "failure recurred after resolution: "+sig.Summary); err != nil {
			return err
		}
	default:
		inc = &Incident{
			Namespace: sig.Namespace, WorkloadKind: sig.WorkloadKind, WorkloadName: sig.WorkloadName,
			Pod: sig.Pod, PodUID: sig.PodUID, Container: sig.Container, Detector: sig.Detector,
			Severity: string(sig.Severity), Summary: sig.Summary, DetectedAt: sig.ObservedAt,
		}
		if err := m.store.CreateIncident(ctx, inc); err != nil {
			return err
		}
	}
	if err := m.move(ctx, inc, StatusInvestigating, "collecting evidence"); err != nil {
		return err
	}

	ev, err := m.store.AddEvidence(ctx, inc.ID, m.collect.Collect(ctx, sig))
	if err != nil {
		return err
	}
	summary, err := m.diagnoseAndPlan(ctx, inc, ev)
	if err != nil {
		return err
	}
	return m.move(ctx, inc, StatusDiagnosed, summary)
}

func (m *Manager) diagnoseAndPlan(ctx context.Context, inc *Incident, ev []Evidence) (string, error) {
	hs, err := m.store.ReplaceHypotheses(ctx, inc.ID, m.diagnose.Diagnose(*inc, ev))
	if err != nil {
		return "", err
	}
	ps := m.plan.Plan(*inc, hs)
	if err := m.store.ReplaceProposals(ctx, inc.ID, ps); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d hypotheses ranked, %d remediation suggestions", len(hs), len(ps)), nil
}

// Reconcile verifies every diagnosed incident against the live workload.
// Only a passed verification resolves; a workload with no pods left is
// dismissed because recovery cannot be verified. Failed checks are not stored:
// "not recovered yet" every few seconds is noise, not evidence.
func (m *Manager) Reconcile(ctx context.Context) error {
	open, err := m.store.OpenIncidents(ctx)
	if err != nil {
		return fmt.Errorf("listing open incidents: %w", err)
	}
	for i := range open {
		inc := &open[i]
		// Known limitation: incidents interrupted mid-investigation stay in detected/investigating.
		if inc.Status != StatusDiagnosed {
			continue
		}
		v := m.verify.VerifyWorkload(ctx, *inc)
		v.IncidentID = inc.ID
		var err error
		switch v.Outcome {
		case OutcomePassed:
			err = m.resolve(ctx, inc, &v)
		case OutcomeInconclusive:
			if err = m.store.AddVerification(ctx, &v); err == nil {
				err = m.move(ctx, inc, StatusDismissed, v.Summary)
			}
		}
		if err != nil {
			slog.Error("verifying incident", "incident", inc.ID, "err", err)
		}
	}
	return nil
}

func (m *Manager) resolve(ctx context.Context, inc *Incident, v *Verification) error {
	if err := m.move(ctx, inc, StatusVerifying, "workload looks healthy; verifying"); err != nil {
		return err
	}
	if err := m.store.AddVerification(ctx, v); err != nil {
		return err
	}
	return m.move(ctx, inc, StatusResolved, "verification passed: "+v.Summary)
}

func (m *Manager) move(ctx context.Context, inc *Incident, to Status, reason string) error {
	if err := CheckTransition(inc.Status, to); err != nil {
		return err
	}
	if err := m.store.Transition(ctx, inc.ID, inc.Status, to, reason, m.now()); err != nil {
		return fmt.Errorf("incident %d: %w", inc.ID, err)
	}
	inc.Status = to
	return nil
}
