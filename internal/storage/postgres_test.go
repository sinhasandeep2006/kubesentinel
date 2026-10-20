package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
)

func TestLoadMigrations(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range ms {
		if m.up == "" || m.down == "" {
			t.Errorf("migration %d missing up or down", m.version)
		}
		if i > 0 && m.version <= ms[i-1].version {
			t.Errorf("migrations out of order")
		}
	}
}

// Integration test: set KS_TEST_DATABASE_URL to a disposable database, e.g.
// postgres://kubesentinel:kubesentinel@127.0.0.1:5432/kubesentinel?sslmode=disable
func TestStoreRoundTrip(t *testing.T) {
	url := os.Getenv("KS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("KS_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close) // registered first so it runs after the row cleanup below
	if _, err := s.MigrateUp(ctx); err != nil {
		t.Fatal(err)
	}

	inc := &incident.Incident{Namespace: "test", WorkloadKind: "Deployment", WorkloadName: "w-" + time.Now().Format("150405.000000"), Pod: "p",
		PodUID: "uid-" + time.Now().Format("150405.000000"), Container: "app", Detector: "repeated-restarts",
		Severity: "critical", Summary: "s", DetectedAt: time.Now()}
	if err := s.CreateIncident(ctx, inc); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, `DELETE FROM incidents WHERE id = $1`, inc.ID) })

	if err := s.Transition(ctx, inc.ID, incident.StatusDetected, incident.StatusInvestigating, "r", time.Now()); err != nil {
		t.Fatal(err)
	}
	// Stale "from" must be rejected.
	if err := s.Transition(ctx, inc.ID, incident.StatusDetected, incident.StatusInvestigating, "r", time.Now()); !errors.Is(err, incident.ErrInvalidTransition) {
		t.Errorf("stale transition: got %v", err)
	}

	ev, err := s.AddEvidence(ctx, inc.ID, []incident.Evidence{{Type: incident.EvidenceTermination, Source: "s", Resource: "r",
		ObservedAt: time.Now(), Summary: "x", Availability: incident.Available, Payload: map[string]any{"exit_code": 1}}})
	if err != nil || ev[0].ID == 0 {
		t.Fatalf("AddEvidence: %v %+v", err, ev)
	}
	id := ev[0].ID
	saved, err := s.ReplaceHypotheses(ctx, inc.ID, []incident.Hypothesis{{Rank: 1, Category: "c", Title: "t", Score: 1, Method: "m",
		Supporting: []incident.EvidenceRef{{EvidenceID: &id, Note: "n"}}, Contradicting: []incident.EvidenceRef{{Note: "none"}}}})
	if err != nil || saved[0].ID == 0 {
		t.Fatalf("ReplaceHypotheses: %v %+v", err, saved)
	}
	hid := saved[0].ID
	if err := s.ReplaceProposals(ctx, inc.ID, []incident.Proposal{{HypothesisID: &hid, ActionType: "rollout_undo", Title: "t",
		Target: "x", Rationale: "r", ExpectedOutcome: "e", Risk: "medium", SuggestedCommand: "kubectl ...", Rollback: "rb",
		Mode: incident.ModeSuggestionOnly, Preconditions: []string{"p"}}}); err != nil {
		t.Fatal(err)
	}
	ps, err := s.ListProposals(ctx, inc.ID)
	if err != nil || len(ps) != 1 || *ps[0].HypothesisID != hid || ps[0].Preconditions[0] != "p" || len(ps[0].Supporting) != 0 {
		t.Errorf("ListProposals: %v %+v", err, ps)
	}
	// The database itself refuses anything but a suggestion.
	if err := s.ReplaceProposals(ctx, inc.ID, []incident.Proposal{{Risk: "low", Mode: "execute"}}); err == nil {
		t.Error("a non-suggestion proposal must be rejected by the database")
	}
	hs, err := s.ListHypotheses(ctx, inc.ID)
	if err != nil || len(hs) != 1 || len(hs[0].Supporting) != 1 || *hs[0].Supporting[0].EvidenceID != id || len(hs[0].Contradicting) != 1 {
		t.Errorf("ListHypotheses: %v %+v", err, hs)
	}

	got, err := s.LatestIncident(ctx, inc.Key())
	if err != nil || got == nil || got.Status != incident.StatusInvestigating {
		t.Errorf("LatestIncident: %v %+v", err, got)
	}
	tr, err := s.ListTransitions(ctx, inc.ID)
	if err != nil || len(tr) != 2 {
		t.Errorf("ListTransitions: %v %+v", err, tr)
	}
	if _, err := s.GetIncident(ctx, -1); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("GetIncident(-1): %v", err)
	}
}
