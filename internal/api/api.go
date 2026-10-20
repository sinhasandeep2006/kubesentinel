// Package api serves the read-only REST API under /api/v1.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
)

type Reader interface {
	ListIncidents(ctx context.Context, f incident.Filter) ([]incident.Incident, error)
	GetIncident(ctx context.Context, id int64) (incident.Incident, error)
	ListEvidence(ctx context.Context, incidentID int64) ([]incident.Evidence, error)
	ListHypotheses(ctx context.Context, incidentID int64) ([]incident.Hypothesis, error)
	ListTransitions(ctx context.Context, incidentID int64) ([]incident.Transition, error)
	ListVerifications(ctx context.Context, incidentID int64) ([]incident.Verification, error)
	ListProposals(ctx context.Context, incidentID int64) ([]incident.Proposal, error)
}

type Source struct {
	Name   string `json:"name"`
	Status string `json:"status"` // available | degraded | unavailable
	Detail string `json:"detail"`
}

// Register adds the /api/v1 routes to mux. capabilities is called per request
// so it reflects current state.
func Register(mux *http.ServeMux, r Reader, capabilities func(*http.Request) []Source) {
	mux.HandleFunc("GET /api/v1/system/capabilities", func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"sources": capabilities(req)})
	})
	mux.HandleFunc("GET /api/v1/incidents", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		items, err := r.ListIncidents(req.Context(), incident.Filter{Status: q.Get("status"), Namespace: q.Get("namespace"), Severity: q.Get("severity")})
		respond(w, items, err)
	})
	mux.HandleFunc("GET /api/v1/incidents/{id}", withIncident(r, func(w http.ResponseWriter, _ *http.Request, inc incident.Incident) {
		writeJSON(w, http.StatusOK, inc)
	}))
	mux.HandleFunc("GET /api/v1/incidents/{id}/evidence", withIncident(r, func(w http.ResponseWriter, req *http.Request, inc incident.Incident) {
		items, err := r.ListEvidence(req.Context(), inc.ID)
		respond(w, items, err)
	}))
	mux.HandleFunc("GET /api/v1/incidents/{id}/hypotheses", withIncident(r, func(w http.ResponseWriter, req *http.Request, inc incident.Incident) {
		items, err := r.ListHypotheses(req.Context(), inc.ID)
		respond(w, items, err)
	}))
	mux.HandleFunc("GET /api/v1/incidents/{id}/verification", withIncident(r, func(w http.ResponseWriter, req *http.Request, inc incident.Incident) {
		items, err := r.ListVerifications(req.Context(), inc.ID)
		respond(w, items, err)
	}))
	mux.HandleFunc("GET /api/v1/incidents/{id}/remediations", withIncident(r, func(w http.ResponseWriter, req *http.Request, inc incident.Incident) {
		items, err := r.ListProposals(req.Context(), inc.ID)
		respond(w, items, err)
	}))
	mux.HandleFunc("GET /api/v1/incidents/{id}/timeline", withIncident(r, func(w http.ResponseWriter, req *http.Request, inc incident.Incident) {
		items, err := timeline(req.Context(), r, inc.ID)
		respond(w, items, err)
	}))
}

type TimelineItem struct {
	At         time.Time `json:"at"`
	Kind       string    `json:"kind"` // transition | evidence | verification
	Title      string    `json:"title"`
	Detail     string    `json:"detail"`
	EvidenceID *int64    `json:"evidence_id"`
}

func timeline(ctx context.Context, r Reader, id int64) ([]TimelineItem, error) {
	trs, err := r.ListTransitions(ctx, id)
	if err != nil {
		return nil, err
	}
	evs, err := r.ListEvidence(ctx, id)
	if err != nil {
		return nil, err
	}
	vs, err := r.ListVerifications(ctx, id)
	if err != nil {
		return nil, err
	}
	var out []TimelineItem
	for _, t := range trs {
		title := string(t.To)
		if t.From != "" {
			title = string(t.From) + " → " + string(t.To)
		}
		out = append(out, TimelineItem{At: t.At, Kind: "transition", Title: title, Detail: t.Reason})
	}
	for _, e := range evs {
		detail := e.Summary
		if e.Availability != incident.Available {
			detail = fmt.Sprintf("%s: %s", e.Availability, e.Detail)
		}
		out = append(out, TimelineItem{At: e.ObservedAt, Kind: "evidence", Title: e.Type, Detail: detail, EvidenceID: &e.ID})
	}
	for _, v := range vs {
		out = append(out, TimelineItem{At: v.CheckedAt, Kind: "verification", Title: "verification " + string(v.Outcome), Detail: v.Summary})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

func withIncident(r Reader, h func(http.ResponseWriter, *http.Request, incident.Incident)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid incident id")
			return
		}
		inc, err := r.GetIncident(req.Context(), id)
		if errors.Is(err, incident.ErrNotFound) {
			writeError(w, http.StatusNotFound, "incident not found")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		h(w, req, inc)
	}
}

func respond[T any](w http.ResponseWriter, items []T, err error) {
	if err != nil {
		serverError(w, err)
		return
	}
	if items == nil {
		items = []T{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func serverError(w http.ResponseWriter, err error) {
	slog.Error("api request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error") // details stay in the server log
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encoding response", "err", err)
	}
}
