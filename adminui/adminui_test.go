package adminui

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_ServesPinnedSanitizerBeforeEditor(t *testing.T) {
	h := http.StripPrefix("/admin", Handler())
	index := httptest.NewRecorder()
	h.ServeHTTP(index, httptest.NewRequest("GET", "/admin/", nil))
	if sanitizer, editor := strings.Index(index.Body.String(), `src="/admin/purify.min.js"`), strings.Index(index.Body.String(), `src="/admin/admin.js"`); sanitizer < 0 || editor <= sanitizer {
		t.Fatal("sanitizer must load locally before editor")
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/admin/purify.min.js", nil))
	if r.Code != http.StatusOK || !strings.Contains(r.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("sanitizer asset unavailable: %d %q", r.Code, r.Header().Get("Content-Type"))
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(r.Body.Bytes())); got != "2c90a9b46d6463f26038a29b686e82bc91de01fdac9d5229e7cfe3b360134ea2" {
		t.Fatalf("vendored distribution differs from reviewed release: %s", got)
	}
}

func TestHandler_ServesIndex(t *testing.T) {
	h := http.StripPrefix("/admin", Handler())
	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("index: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Site editor") {
		t.Errorf("body did not contain title; got prefix %q", rec.Body.String()[:120])
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-rich-editor`,
		`data-editor-command="bold"`,
		`name="template_id"`,
		`name="publish_at"`,
		`name="unpublish_at"`,
		`value="scheduled"`,
		`role="textbox"`,
		`aria-multiline="true"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
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
	for _, want := range []string{
		"initRichEditors",
		"pagePayload",
		"dateTimeLocalToISO",
		"renderPreviewDocument",
		"sourceAuthoritative",
		"safeEditorURL",
		`setAttribute("sandbox", "allow-same-origin")`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("js body missing %q", want)
		}
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
