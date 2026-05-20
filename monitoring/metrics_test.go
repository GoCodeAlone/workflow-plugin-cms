package monitoring

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCounters_Inc(t *testing.T) {
	c := New()
	c.Inc("acme", 200)
	c.Inc("acme", 200)
	c.Inc("beta", 200)
	c.Inc("acme", 500)
	snap := c.Snapshot()
	if snap["acme"] != 3 {
		t.Errorf("acme = %d want 3", snap["acme"])
	}
	if snap["beta"] != 1 {
		t.Errorf("beta = %d want 1", snap["beta"])
	}
	if snap["_global"] != 4 {
		t.Errorf("_global = %d want 4", snap["_global"])
	}
}

func TestCounters_MetricsExposition(t *testing.T) {
	c := New()
	c.Inc("acme", 200)
	c.Inc("beta", 500)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: %d", rec.Code)
	}
	body := rec.Body.String()

	required := []string{
		"multisite_uptime_seconds",
		"multisite_requests_total 2",
		"multisite_request_errors_total 1",
		`multisite_tenant_requests_total{tenant="acme"} 1`,
		`multisite_tenant_requests_total{tenant="beta"} 1`,
		`multisite_tenant_request_errors_total{tenant="beta"} 1`,
	}
	for _, want := range required {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in body:\n%s", want, body)
		}
	}
}

func TestCounters_UnresolvedTenant_NotAttributed(t *testing.T) {
	c := New()
	c.Inc("", 200)
	snap := c.Snapshot()
	if snap["_global"] != 1 {
		t.Errorf("global = %d want 1", snap["_global"])
	}
	if len(snap) != 1 {
		t.Errorf("snapshot should only contain _global; got %v", snap)
	}
}

func TestEscapeLabel_StripsBreakoutChars(t *testing.T) {
	cases := map[string]string{
		`acme`:          `acme`,
		`a"b`:           `ab`,
		"a\nb":          "ab",
		"a\\b":          "ab",
	}
	for in, want := range cases {
		if got := escapeLabel(in); got != want {
			t.Errorf("escape(%q) = %q want %q", in, got, want)
		}
	}
}

func TestStatusRecorder_CapturesStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &StatusRecorder{ResponseWriter: rec}
	sr.WriteHeader(http.StatusTeapot)
	if sr.Status != http.StatusTeapot {
		t.Errorf("captured = %d want 418", sr.Status)
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("inner status = %d want 418", rec.Code)
	}
}
