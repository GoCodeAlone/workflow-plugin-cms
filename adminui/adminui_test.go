package adminui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_ServesIndex(t *testing.T) {
	h := http.StripPrefix("/admin", Handler())
	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("index: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Multisite Admin") {
		t.Errorf("body did not contain title; got prefix %q", rec.Body.String()[:120])
	}
}

func TestHandler_ServesCSS(t *testing.T) {
	h := http.StripPrefix("/admin", Handler())
	req := httptest.NewRequest(http.MethodGet, "/admin/admin.css", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("css: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "body") {
		t.Errorf("css body missing")
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "css") {
		t.Errorf("css content-type: %q", rec.Header().Get("Content-Type"))
	}
}

func TestHandler_ServesJS(t *testing.T) {
	h := http.StripPrefix("/admin", Handler())
	req := httptest.NewRequest(http.MethodGet, "/admin/admin.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("js: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "createTenant") {
		t.Errorf("js body missing expected symbol")
	}
}

func TestHandler_404OnUnknown(t *testing.T) {
	h := http.StripPrefix("/admin", Handler())
	req := httptest.NewRequest(http.MethodGet, "/admin/nope", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown: %d want 404", rec.Code)
	}
}
