package media

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalFS_PutGet(t *testing.T) {
	dir := t.TempDir()
	be := &LocalFS{Root: dir, PublicURL: "https://x/media"}

	body := bytes.NewReader([]byte("hello"))
	url, err := be.Put(7, body, "text/plain")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if !strings.HasPrefix(url, "https://x/media/7/") || !strings.HasSuffix(url, ".txt") {
		t.Errorf("url shape: %q", url)
	}

	rc, ct, err := be.Get(url)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer rc.Close()
	if ct != "text/plain" {
		t.Errorf("content_type = %q want text/plain", ct)
	}
	data, _ := io.ReadAll(rc)
	if string(data) != "hello" {
		t.Errorf("body roundtrip = %q want hello", data)
	}
}

func TestLocalFS_Idempotent(t *testing.T) {
	dir := t.TempDir()
	be := &LocalFS{Root: dir}
	body1 := bytes.NewReader([]byte("dupe"))
	body2 := bytes.NewReader([]byte("dupe"))
	url1, _ := be.Put(1, body1, "text/plain")
	url2, _ := be.Put(1, body2, "text/plain")
	if url1 != url2 {
		t.Errorf("idempotent put: %q vs %q", url1, url2)
	}
}

func TestLocalFS_TenantScoped(t *testing.T) {
	dir := t.TempDir()
	be := &LocalFS{Root: dir}
	url1, _ := be.Put(1, bytes.NewReader([]byte("a")), "text/plain")
	url2, _ := be.Put(2, bytes.NewReader([]byte("a")), "text/plain")
	// Same content, different tenants → different URLs (path includes tenant).
	if url1 == url2 {
		t.Errorf("tenant scope leaked: both tenants got %q", url1)
	}
}

func TestLocalFS_TooLarge(t *testing.T) {
	dir := t.TempDir()
	be := &LocalFS{Root: dir}
	big := bytes.NewReader(make([]byte, MaxUploadBytes+1))
	_, err := be.Put(1, big, "application/octet-stream")
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("too-large: %v want ErrTooLarge", err)
	}
}

func TestLocalFS_RejectTraversal(t *testing.T) {
	dir := t.TempDir()
	be := &LocalFS{Root: dir}
	_, _, err := be.Get("/1/../../etc/passwd")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("traversal: %v want ErrNotFound", err)
	}
}

func TestUploadHandler_HappyPath(t *testing.T) {
	dir := t.TempDir()
	h := &UploadHandler{Backend: &LocalFS{Root: dir, PublicURL: "https://x/m"}}
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("hi"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.ServeForTenant(rec, req, 42)

	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"url":"https://x/m/42/`) {
		t.Errorf("body url: %q", body)
	}
}

func TestUploadHandler_TooLarge_413(t *testing.T) {
	dir := t.TempDir()
	h := &UploadHandler{Backend: &LocalFS{Root: dir}}
	big := bytes.NewReader(make([]byte, MaxUploadBytes+1))
	req := httptest.NewRequest(http.MethodPost, "/upload", big)
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	h.ServeForTenant(rec, req, 1)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status: %d want 413", rec.Code)
	}
}

func TestUploadHandler_NoBackend_503(t *testing.T) {
	h := &UploadHandler{}
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("x"))
	rec := httptest.NewRecorder()
	h.ServeForTenant(rec, req, 1)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no backend: %d want 503", rec.Code)
	}
}

func TestUploadHandler_MethodNotAllowed(t *testing.T) {
	dir := t.TempDir()
	h := &UploadHandler{Backend: &LocalFS{Root: dir}}
	req := httptest.NewRequest(http.MethodGet, "/upload", nil)
	rec := httptest.NewRecorder()
	h.ServeForTenant(rec, req, 1)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d want 405", rec.Code)
	}
}
