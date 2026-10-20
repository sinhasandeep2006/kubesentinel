// Package storage persists incidents in PostgreSQL.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
	"github.com/sinhasandeep2006/kubesentinel/migrations"
)

const queryTimeout = 5 * time.Second

type Store struct {
	pool *pgxpool.Pool
}

// Open connects to Postgres. The URL holds credentials and must never be logged.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	return s.pool.Ping(ctx)
}

// ---- migrations ----

type migration struct {
	version  int
	up, down string
}

func loadMigrations() ([]migration, error) {
	byVersion := map[int]*migration{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		dir := ""
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			dir = "up"
		case strings.HasSuffix(name, ".down.sql"):
			dir = "down"
		default:
			continue
		}
		v, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return nil, fmt.Errorf("migration %s: bad version prefix", name)
		}
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return nil, err
		}
		m := byVersion[v]
		if m == nil {
			m = &migration{version: v}
			byVersion[v] = m
		}
		if dir == "up" {
			m.up = string(body)
		} else {
			m.down = string(body)
		}
	}
	out := make([]migration, 0, len(byVersion))
	for _, m := range byVersion {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func (s *Store) applied(ctx context.Context) (map[int]bool, error) {
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, fmt.Errorf("creating schema_migrations: %w", err)
	}
	rows, err := s.pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("reading schema_migrations: %w", err)
	}
	vs, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return nil, err
	}
	done := map[int]bool{}
	for _, v := range vs {
		done[v] = true
	}
	return done, nil
}

// MigrateUp applies every pending migration, each in its own transaction.
func (s *Store) MigrateUp(ctx context.Context) (int, error) {
	ms, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	done, err := s.applied(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range ms {
		if done[m.version] {
			continue
		}
		err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.up); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, m.version)
			return err
		})
		if err != nil {
			return n, fmt.Errorf("applying migration %d: %w", m.version, err)
		}
		n++
	}
	return n, nil
}

// MigrateDown rolls back the most recently applied migration. Returns its version, or 0 if none.
func (s *Store) MigrateDown(ctx context.Context) (int, error) {
	ms, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	done, err := s.applied(ctx)
	if err != nil {
		return 0, err
	}
	for i := len(ms) - 1; i >= 0; i-- {
		m := ms[i]
		if !done[m.version] {
			continue
		}
		err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.down); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, m.version)
			return err
		})
		if err != nil {
			return 0, fmt.Errorf("rolling back migration %d: %w", m.version, err)
		}
		return m.version, nil
	}
	return 0, nil
}

// ---- writes (incident.Store) ----

const incidentCols = `id, namespace, workload_kind, workload_name, pod, pod_uid, container, detector,
	status, severity, summary, detected_at, updated_at, resolved_at`

func scanIncident(row pgx.Row) (incident.Incident, error) {
	var i incident.Incident
	err := row.Scan(&i.ID, &i.Namespace, &i.WorkloadKind, &i.WorkloadName, &i.Pod, &i.PodUID, &i.Container,
		&i.Detector, &i.Status, &i.Severity, &i.Summary, &i.DetectedAt, &i.UpdatedAt, &i.ResolvedAt)
	return i, err
}

func (s *Store) LatestIncident(ctx context.Context, k incident.Key) (*incident.Incident, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	inc, err := scanIncident(s.pool.QueryRow(ctx, `SELECT `+incidentCols+` FROM incidents
		WHERE namespace = $1 AND workload_kind = $2 AND workload_name = $3 AND container = $4
		AND status <> 'dismissed' ORDER BY id DESC LIMIT 1`,
		k.Namespace, k.WorkloadKind, k.WorkloadName, k.Container))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding incident for %s/%s/%s: %w", k.Namespace, k.WorkloadName, k.Container, err)
	}
	return &inc, nil
}

func (s *Store) CreateIncident(ctx context.Context, inc *incident.Incident) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	now := time.Now().UTC()
	inc.Status, inc.UpdatedAt = incident.StatusDetected, now
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO incidents (namespace, workload_kind, workload_name, pod, pod_uid,
			container, detector, status, severity, summary, detected_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
			inc.Namespace, inc.WorkloadKind, inc.WorkloadName, inc.Pod, inc.PodUID, inc.Container, inc.Detector,
			inc.Status, inc.Severity, inc.Summary, inc.DetectedAt, now).Scan(&inc.ID)
		if err != nil {
			return fmt.Errorf("inserting incident: %w", err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO incident_transitions (incident_id, to_status, reason, at) VALUES ($1,$2,$3,$4)`,
			inc.ID, inc.Status, inc.Summary, inc.DetectedAt)
		return err
	})
}

// Transition changes status only if the incident is still in from (row-locked),
// and records the change in the audit trail in the same transaction.
func (s *Store) Transition(ctx context.Context, id int64, from, to incident.Status, reason string, at time.Time) error {
	if err := incident.CheckTransition(from, to); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var cur incident.Status
		if err := tx.QueryRow(ctx, `SELECT status FROM incidents WHERE id = $1 FOR UPDATE`, id).Scan(&cur); err != nil {
			return fmt.Errorf("locking incident %d: %w", id, err)
		}
		if cur != from {
			return fmt.Errorf("incident %d is %s, not %s: %w", id, cur, from, incident.ErrInvalidTransition)
		}
		_, err := tx.Exec(ctx, `UPDATE incidents SET status = $2, updated_at = $3,
			resolved_at = CASE WHEN $2 = 'resolved' THEN $3 WHEN $2 = 'reopened' THEN NULL ELSE resolved_at END
			WHERE id = $1`, id, to, at)
		if err != nil {
			return fmt.Errorf("updating incident %d: %w", id, err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO incident_transitions (incident_id, from_status, to_status, reason, at)
			VALUES ($1,$2,$3,$4,$5)`, id, from, to, reason, at)
		return err
	})
}

func (s *Store) AddEvidence(ctx context.Context, incidentID int64, ev []incident.Evidence) ([]incident.Evidence, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for i := range ev {
			ev[i].IncidentID = incidentID
			payload, err := json.Marshal(ev[i].Payload)
			if err != nil {
				return fmt.Errorf("encoding %s payload: %w", ev[i].Type, err)
			}
			err = tx.QueryRow(ctx, `INSERT INTO evidence_records (incident_id, type, source, resource, observed_at,
				summary, availability, detail, payload) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
				incidentID, ev[i].Type, ev[i].Source, ev[i].Resource, ev[i].ObservedAt, ev[i].Summary,
				ev[i].Availability, ev[i].Detail, payload).Scan(&ev[i].ID)
			if err != nil {
				return fmt.Errorf("inserting %s evidence: %w", ev[i].Type, err)
			}
		}
		return nil
	})
	return ev, err
}

func (s *Store) ReplaceHypotheses(ctx context.Context, incidentID int64, hs []incident.Hypothesis) ([]incident.Hypothesis, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM hypotheses WHERE incident_id = $1`, incidentID); err != nil {
			return fmt.Errorf("clearing hypotheses: %w", err)
		}
		for i := range hs {
			h := &hs[i]
			h.IncidentID = incidentID
			err := tx.QueryRow(ctx, `INSERT INTO hypotheses (incident_id, rank, category, title, score, rationale,
				missing_evidence, alternatives, next_steps, method) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
				incidentID, h.Rank, h.Category, h.Title, h.Score, h.Rationale,
				orEmpty(h.Missing), orEmpty(h.Alternatives), orEmpty(h.NextSteps), h.Method).Scan(&h.ID)
			if err != nil {
				return fmt.Errorf("inserting hypothesis: %w", err)
			}
			for rel, refs := range map[string][]incident.EvidenceRef{"supporting": h.Supporting, "contradicting": h.Contradicting} {
				for _, r := range refs {
					if _, err := tx.Exec(ctx, `INSERT INTO hypothesis_evidence (hypothesis_id, evidence_id, relation, note)
						VALUES ($1,$2,$3,$4)`, h.ID, r.EvidenceID, rel, r.Note); err != nil {
						return fmt.Errorf("linking evidence: %w", err)
					}
				}
			}
		}
		return nil
	})
	return hs, err
}

// ReplaceProposals swaps the incident's suggestions for a fresh set; they follow the latest diagnosis.
func (s *Store) ReplaceProposals(ctx context.Context, incidentID int64, ps []incident.Proposal) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM remediation_proposals WHERE incident_id = $1`, incidentID); err != nil {
			return fmt.Errorf("clearing proposals: %w", err)
		}
		for _, p := range ps {
			supporting := p.Supporting
			if supporting == nil {
				supporting = []incident.EvidenceRef{}
			}
			_, err := tx.Exec(ctx, `INSERT INTO remediation_proposals (incident_id, hypothesis_id, action_type, title,
				target, rationale, supporting, preconditions, expected_outcome, risk, required_permissions,
				suggested_command, verification_criteria, rollback, mode)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
				incidentID, p.HypothesisID, p.ActionType, p.Title, p.Target, p.Rationale, supporting,
				orEmpty(p.Preconditions), p.ExpectedOutcome, p.Risk, orEmpty(p.RequiredPermissions),
				p.SuggestedCommand, orEmpty(p.VerificationCriteria), p.Rollback, p.Mode)
			if err != nil {
				return fmt.Errorf("inserting %s proposal: %w", p.ActionType, err)
			}
		}
		return nil
	})
}

func (s *Store) ListProposals(ctx context.Context, incidentID int64) ([]incident.Proposal, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT id, incident_id, hypothesis_id, action_type, title, target, rationale,
		supporting, preconditions, expected_outcome, risk, required_permissions, suggested_command,
		verification_criteria, rollback, mode FROM remediation_proposals WHERE incident_id = $1 ORDER BY id`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("listing proposals: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (incident.Proposal, error) {
		var p incident.Proposal
		err := r.Scan(&p.ID, &p.IncidentID, &p.HypothesisID, &p.ActionType, &p.Title, &p.Target, &p.Rationale,
			&p.Supporting, &p.Preconditions, &p.ExpectedOutcome, &p.Risk, &p.RequiredPermissions, &p.SuggestedCommand,
			&p.VerificationCriteria, &p.Rollback, &p.Mode)
		return p, err
	})
}

func (s *Store) AddVerification(ctx context.Context, v *incident.Verification) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	criteria := v.Criteria
	if criteria == nil {
		criteria = []incident.Criterion{}
	}
	err := s.pool.QueryRow(ctx, `INSERT INTO verification_results (incident_id, checked_at, outcome, summary, criteria)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`, v.IncidentID, v.CheckedAt, v.Outcome, v.Summary, criteria).Scan(&v.ID)
	if err != nil {
		return fmt.Errorf("inserting verification: %w", err)
	}
	return nil
}

func (s *Store) OpenIncidents(ctx context.Context) ([]incident.Incident, error) {
	return s.queryIncidents(ctx, `SELECT `+incidentCols+` FROM incidents WHERE status NOT IN ('resolved','dismissed') ORDER BY id`)
}

// ---- reads (API) ----

func (s *Store) ListIncidents(ctx context.Context, f incident.Filter) ([]incident.Incident, error) {
	return s.queryIncidents(ctx, `SELECT `+incidentCols+` FROM incidents
		WHERE ($1 = '' OR status = $1) AND ($2 = '' OR namespace = $2) AND ($3 = '' OR severity = $3)
		ORDER BY detected_at DESC, id DESC LIMIT 200`, f.Status, f.Namespace, f.Severity)
}

func (s *Store) queryIncidents(ctx context.Context, sql string, args ...any) ([]incident.Incident, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("listing incidents: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (incident.Incident, error) { return scanIncident(r) })
}

func (s *Store) GetIncident(ctx context.Context, id int64) (incident.Incident, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	inc, err := scanIncident(s.pool.QueryRow(ctx, `SELECT `+incidentCols+` FROM incidents WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return inc, incident.ErrNotFound
	}
	if err != nil {
		return inc, fmt.Errorf("getting incident %d: %w", id, err)
	}
	return inc, nil
}

func (s *Store) ListEvidence(ctx context.Context, incidentID int64) ([]incident.Evidence, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT id, incident_id, type, source, resource, observed_at, summary,
		availability, detail, payload FROM evidence_records WHERE incident_id = $1 ORDER BY observed_at, id`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("listing evidence: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (incident.Evidence, error) {
		var e incident.Evidence
		err := r.Scan(&e.ID, &e.IncidentID, &e.Type, &e.Source, &e.Resource, &e.ObservedAt, &e.Summary,
			&e.Availability, &e.Detail, &e.Payload)
		return e, err
	})
}

func (s *Store) ListHypotheses(ctx context.Context, incidentID int64) ([]incident.Hypothesis, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT id, incident_id, rank, category, title, score, rationale,
		missing_evidence, alternatives, next_steps, method FROM hypotheses WHERE incident_id = $1 ORDER BY rank`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("listing hypotheses: %w", err)
	}
	hs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (incident.Hypothesis, error) {
		h := incident.Hypothesis{Supporting: []incident.EvidenceRef{}, Contradicting: []incident.EvidenceRef{}}
		err := r.Scan(&h.ID, &h.IncidentID, &h.Rank, &h.Category, &h.Title, &h.Score, &h.Rationale,
			&h.Missing, &h.Alternatives, &h.NextSteps, &h.Method)
		return h, err
	})
	if err != nil || len(hs) == 0 {
		return hs, err
	}

	byID := map[int64]*incident.Hypothesis{}
	ids := make([]int64, len(hs))
	for i := range hs {
		byID[hs[i].ID] = &hs[i]
		ids[i] = hs[i].ID
	}
	refs, err := s.pool.Query(ctx, `SELECT hypothesis_id, evidence_id, relation, note FROM hypothesis_evidence
		WHERE hypothesis_id = ANY($1) ORDER BY id`, ids)
	if err != nil {
		return nil, fmt.Errorf("listing hypothesis evidence: %w", err)
	}
	defer refs.Close()
	for refs.Next() {
		var hid int64
		var rel string
		var r incident.EvidenceRef
		if err := refs.Scan(&hid, &r.EvidenceID, &rel, &r.Note); err != nil {
			return nil, err
		}
		if h := byID[hid]; h != nil && rel == "supporting" {
			h.Supporting = append(h.Supporting, r)
		} else if h != nil {
			h.Contradicting = append(h.Contradicting, r)
		}
	}
	return hs, refs.Err()
}

func (s *Store) ListTransitions(ctx context.Context, incidentID int64) ([]incident.Transition, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT incident_id, COALESCE(from_status, ''), to_status, reason, at
		FROM incident_transitions WHERE incident_id = $1 ORDER BY at, id`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("listing transitions: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (incident.Transition, error) {
		var t incident.Transition
		err := r.Scan(&t.IncidentID, &t.From, &t.To, &t.Reason, &t.At)
		return t, err
	})
}

func (s *Store) ListVerifications(ctx context.Context, incidentID int64) ([]incident.Verification, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT id, incident_id, checked_at, outcome, summary, criteria
		FROM verification_results WHERE incident_id = $1 ORDER BY checked_at, id`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("listing verifications: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (incident.Verification, error) {
		var v incident.Verification
		err := r.Scan(&v.ID, &v.IncidentID, &v.CheckedAt, &v.Outcome, &v.Summary, &v.Criteria)
		return v, err
	})
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
