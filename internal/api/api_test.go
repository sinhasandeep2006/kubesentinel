package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sinhasandeep2006/kubesentinel/internal/incident"
)

type fakeReader struct{}

var t0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func (fakeReader) ListIncidents(_ context.Context, f incident.Filter) ([]incident.Incident, error) {
	if f.Status == "none" {
		return nil, nil
	}
	return []incident.Incident{{ID: 1}}, nil
}
func (fakeReader) GetIncident(_ context.Context, id int64) (incident.Incident, error) {
	if id != 1 {
		return incident.Incident{}, incident.ErrNotFound
	}
	return incident.Incident{ID: 1}, nil
}
func (fakeReader) ListEvidence(context.Context, int64) ([]incident.Evidence, error) {
	return []incident.Evidence{
		{ID: 7, Type: "termination", ObservedAt: t0.Add(time.Second), Availability: incident.Available, Summary: "exit 1"},
		{ID: 8, Type: "metrics", ObservedAt: t0.Add(time.Second), Availability: incident.Unavailable, Detail: "not configured"},
	}, nil
}
func (fakeReader) ListHypotheses(context.Context, int64) ([]incident.Hypothesis, error) {
	return nil, nil
}
func (fakeReader) ListTransitions(context.Context, int64) ([]incident.Transition, error) {
	return []incident.Transition{{To: incident.StatusDetected, At: t0}, {From: incident.StatusDetected, To: incident.StatusInvestigating, At: t0.Add(2 * time.Second)}}, nil
}
func (fakeReader) ListVerifications(context.Context, int64) ([]incident.Verification, error) {
	return nil, nil
}

func (fakeReader) ListProposals(context.Context, int64) ([]incident.Proposal, error) {
	return nil, nil
}

func serve(t *testing.T, path string) (int, map[string]json.RawMessage) {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, fakeReader{}, func(*http.Request) []Source { return []Source{{Name: "prometheus", Status: "unavailable"}} })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]json.RawMessage
	json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestRoutes(t *testing.T) {
	tests := []struct {
		path string
		code int
		key  string
	}{
		{"/api/v1/incidents", 200, "items"},
		{"/api/v1/incidents?status=none", 200, "items"},
		{"/api/v1/incidents/1", 200, "id"},
		{"/api/v1/incidents/2", 404, "error"},
		{"/api/v1/incidents/abc", 400, "error"},
		{"/api/v1/incidents/1/evidence", 200, "items"},
		{"/api/v1/incidents/1/hypotheses", 200, "items"},
		{"/api/v1/incidents/2/hypotheses", 404, "error"},
		{"/api/v1/incidents/1/remediations", 200, "items"},
		{"/api/v1/incidents/2/remediations", 404, "error"},
		{"/api/v1/system/capabilities", 200, "sources"},
	}
	for _, tc := range tests {
		code, body := serve(t, tc.path)
		if code != tc.code || body[tc.key] == nil {
			t.Errorf("%s: code %d body %v, want %d with %q", tc.path, code, body, tc.code, tc.key)
		}
	}
	// Empty lists are [] not null, so the UI can tell "none" from "missing".
	if _, body := serve(t, "/api/v1/incidents?status=none"); string(body["items"]) != "[]" {
		t.Errorf("empty list = %s, want []", body["items"])
	}
}

func TestTimelineIsChronological(t *testing.T) {
	_, body := serve(t, "/api/v1/incidents/1/timeline")
	var items []TimelineItem
	if err := json.Unmarshal(body["items"], &items); err != nil {
		t.Fatal(err)
	}
	want := []string{"detected", "termination", "metrics", "detected → investigating"}
	if len(items) != len(want) {
		t.Fatalf("got %d items: %+v", len(items), items)
	}
	for i, w := range want {
		if items[i].Title != w {
			t.Errorf("item %d = %q, want %q", i, items[i].Title, w)
		}
	}
	if items[2].Detail != "unavailable: not configured" {
		t.Errorf("unavailable evidence detail = %q", items[2].Detail)
	}
}
