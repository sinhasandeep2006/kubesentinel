// Package incident defines incidents, their evidence and hypotheses, and the
// lifecycle state machine. Evidence (observed facts) and Hypothesis (diagnostic
// claims) are deliberately separate types.
package incident

import (
	"errors"
	"fmt"
	"time"
)

type Status string

const (
	StatusDetected         Status = "detected"
	StatusInvestigating    Status = "investigating"
	StatusDiagnosed        Status = "diagnosed"
	StatusAwaitingApproval Status = "awaiting_approval"
	StatusRemediating      Status = "remediating"
	StatusVerifying        Status = "verifying"
	StatusResolved         Status = "resolved"
	StatusReopened         Status = "reopened"
	StatusDismissed        Status = "dismissed"
)

var transitions = map[Status][]Status{
	StatusDetected:         {StatusInvestigating, StatusDismissed},
	StatusInvestigating:    {StatusDiagnosed, StatusDismissed},
	StatusDiagnosed:        {StatusAwaitingApproval, StatusVerifying, StatusDismissed},
	StatusAwaitingApproval: {StatusRemediating, StatusDiagnosed, StatusDismissed}, // diagnosed = approval rejected
	StatusRemediating:      {StatusVerifying, StatusDiagnosed},                    // diagnosed = action failed
	StatusVerifying:        {StatusResolved, StatusDiagnosed, StatusDismissed},    // diagnosed = verification failed
	StatusResolved:         {StatusReopened},
	StatusReopened:         {StatusInvestigating, StatusDismissed},
	StatusDismissed:        nil,
}

var (
	ErrInvalidTransition = errors.New("invalid incident transition")
	ErrNotFound          = errors.New("not found")
)

// Filter narrows incident listings; empty fields match everything.
type Filter struct {
	Status, Namespace, Severity string
}

// CheckTransition reports whether from → to is allowed.
func CheckTransition(from, to Status) error {
	for _, s := range transitions[from] {
		if s == to {
			return nil
		}
	}
	return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
}

// Open reports whether the incident still needs attention.
func (s Status) Open() bool { return s != StatusResolved && s != StatusDismissed }

type Incident struct {
	ID           int64      `json:"id"`
	Namespace    string     `json:"namespace"`
	WorkloadKind string     `json:"workload_kind"`
	WorkloadName string     `json:"workload_name"`
	Pod          string     `json:"pod"`
	PodUID       string     `json:"pod_uid"`
	Container    string     `json:"container"`
	Detector     string     `json:"detector"`
	Status       Status     `json:"status"`
	Severity     string     `json:"severity"`
	Summary      string     `json:"summary"`
	DetectedAt   time.Time  `json:"detected_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ResolvedAt   *time.Time `json:"resolved_at"`
}

type Transition struct {
	IncidentID int64     `json:"incident_id"`
	From       Status    `json:"from"` // empty for the initial state
	To         Status    `json:"to"`
	Reason     string    `json:"reason"`
	At         time.Time `json:"at"`
}

type Availability string

const (
	Available   Availability = "available"
	Unavailable Availability = "unavailable" // source not configured or data does not exist
	Error       Availability = "error"       // retrieval failed
)

// Evidence is an observed fact with its provenance.
type Evidence struct {
	ID           int64          `json:"id"`
	IncidentID   int64          `json:"incident_id"`
	Type         string         `json:"type"`
	Source       string         `json:"source"`
	Resource     string         `json:"resource"`
	ObservedAt   time.Time      `json:"observed_at"`
	Summary      string         `json:"summary"`
	Availability Availability   `json:"availability"`
	Detail       string         `json:"detail"`
	Payload      map[string]any `json:"payload"`
}

const (
	EvidenceRestartSignal  = "restart_signal"
	EvidenceDetectorSignal = "detector_signal" // condition detectors: image pull, scheduling, readiness, OOM
	EvidenceTermination    = "termination"
	EvidenceEvents         = "kubernetes_events"
	EvidencePreviousLogs   = "previous_logs"
	EvidenceMetrics        = "metrics"
	EvidencePodStatus      = "pod_status"
)

type EvidenceRef struct {
	EvidenceID *int64 `json:"evidence_id"`
	Note       string `json:"note"`
}

// Hypothesis is a diagnostic claim. Score ranks hypotheses; it is not a probability.
type Hypothesis struct {
	ID            int64         `json:"id"`
	IncidentID    int64         `json:"incident_id"`
	Rank          int           `json:"rank"`
	Category      string        `json:"category"`
	Title         string        `json:"title"`
	Score         int           `json:"score"`
	Rationale     string        `json:"rationale"`
	Supporting    []EvidenceRef `json:"supporting"`
	Contradicting []EvidenceRef `json:"contradicting"`
	Missing       []string      `json:"missing_evidence"`
	Alternatives  []string      `json:"alternatives"`
	NextSteps     []string      `json:"next_steps"`
	Method        string        `json:"method"`
}

// Proposal is a suggested remediation for a human to review and, if they agree,
// carry out themselves. KubeSentinel never executes proposals.
type Proposal struct {
	ID                   int64         `json:"id"`
	IncidentID           int64         `json:"incident_id"`
	HypothesisID         *int64        `json:"hypothesis_id"`
	ActionType           string        `json:"action_type"`
	Title                string        `json:"title"`
	Target               string        `json:"target"`
	Rationale            string        `json:"rationale"`
	Supporting           []EvidenceRef `json:"supporting"`
	Preconditions        []string      `json:"preconditions"`
	ExpectedOutcome      string        `json:"expected_outcome"`
	Risk                 string        `json:"risk"` // low | medium | high
	RequiredPermissions  []string      `json:"required_permissions"`
	SuggestedCommand     string        `json:"suggested_command"` // for a human to review and run; never executed
	VerificationCriteria []string      `json:"verification_criteria"`
	Rollback             string        `json:"rollback"`
	Mode                 string        `json:"mode"` // always "suggestion_only" in this release
}

const ModeSuggestionOnly = "suggestion_only"

type Outcome string

const (
	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

type Criterion struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Observed string `json:"observed"`
}

type Verification struct {
	ID         int64       `json:"id"`
	IncidentID int64       `json:"incident_id"`
	CheckedAt  time.Time   `json:"checked_at"`
	Outcome    Outcome     `json:"outcome"`
	Summary    string      `json:"summary"`
	Criteria   []Criterion `json:"criteria"`
}
