package monitoring

import (
	"net/http"
	"net/http/httptest"
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
