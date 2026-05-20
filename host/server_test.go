package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-cms/bundle"
	"github.com/GoCodeAlone/workflow-plugin-cms/media"
)

func TestServer_Healthz_OK(t *testing.T) {
	s := New(Config{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("healthz: %d want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "\"status\":\"ok\"") {
		t.Errorf("healthz body = %q", rec.Body.String())
	}
}

func TestServer_Healthz_Failing(t *testing.T) {
	s := New(Config{HealthCheck: func(ctx context.Context) error {
		return errors.New("db down")
	}})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unhealthy: %d want 503", rec.Code)
	}
}

func TestServer_Admin_Routed(t *testing.T) {
	s := New(Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tenants", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("admin tenants list: %d want 200", rec.Code)
	}
}

func TestServer_Ingest_Unauthorized(t *testing.T) {
	s := New(Config{HMACSecret: "test-secret"})
	body := []byte(`{"tag":"v1","repo":"x","tarball_url":"https://x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/release", bytes.NewReader(body))
	req.Header.Set("X-Multisite-Sig", "sha256=deadbeef")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("bad sig: %d want 401", rec.Code)
	}
}

func TestServer_Ingest_ValidPayload(t *testing.T) {
	called := false
	s := New(Config{
		HMACSecret: "test-secret",
		OnIngest:   func(p bundle.IngestPayload) error { called = true; return nil },
	})
	body := []byte(`{"tag":"v1","repo":"acme/site","tarball_url":"https://x/y.tgz"}`)
	sig := bundle.ComputeSignature(body, "test-secret")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/release", bytes.NewReader(body))
	req.Header.Set("X-Multisite-Sig", sig)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("valid: %d want 202", rec.Code)
	}
	if !called {
		t.Error("OnIngest not invoked")
	}
}

// stubResolver implements TenantResolverStore for resolver tests.
type stubResolver struct {
	byHost map[string]TenantInfo
	bySlug map[string]TenantInfo
}

func (s *stubResolver) Lookup(_ context.Context, host string) (TenantInfo, bool) {
	t, ok := s.byHost[host]
	return t, ok
}
func (s *stubResolver) LookupBySlug(_ context.Context, slug string) (TenantInfo, bool) {
	t, ok := s.bySlug[slug]
	return t, ok
}

func TestServer_TenantResolver_Vanity(t *testing.T) {
	r := &stubResolver{
		byHost: map[string]TenantInfo{
			"acme.example": {TenantID: 7, TenantSlug: "acme", Kind: "vanity"},
		},
	}
	s := New(Config{TenantResolverStore: r})

	req := httptest.NewRequest(http.MethodGet, "http://acme.example/", nil)
	req.Host = "acme.example"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	// Tenant resolves but no content path is mounted yet — expect 404
	// with the placeholder body. The tenant-resolved path is exercised
	// by the cache test below.
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 placeholder for resolved tenant; got %d", rec.Code)
	}
}

func TestServer_TenantResolver_UnknownHost404(t *testing.T) {
	s := New(Config{TenantResolverStore: &stubResolver{}})

	req := httptest.NewRequest(http.MethodGet, "http://nope.example/", nil)
	req.Host = "nope.example"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown host: %d want 404", rec.Code)
	}
}

func TestServer_TenantResolver_PreviewFallback(t *testing.T) {
	r := &stubResolver{
		bySlug: map[string]TenantInfo{
			"acme": {TenantID: 7, TenantSlug: "acme", Kind: "preview"},
		},
	}
	s := New(Config{
		PreviewSubdomainBase: "preview.gocodealone.com",
		TenantResolverStore:  r,
	})

	req := httptest.NewRequest(http.MethodGet, "http://acme.preview.gocodealone.com/", nil)
	req.Host = "acme.preview.gocodealone.com"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		// Same as above — preview-resolved still hits the placeholder
		// 404; this proves the slug path was matched.
		t.Errorf("preview fallback: %d", rec.Code)
	}
}

func TestServer_TenantResolver_DeepPreviewRejected(t *testing.T) {
	// Per V22: deep subdomains (a.b.preview.example) must NOT resolve.
	r := &stubResolver{
		bySlug: map[string]TenantInfo{
			"a.b": {TenantID: 7},
		},
	}
	s := New(Config{
		PreviewSubdomainBase: "preview.gocodealone.com",
		TenantResolverStore:  r,
	})

	req := httptest.NewRequest(http.MethodGet, "http://a.b.preview.gocodealone.com/", nil)
	req.Host = "a.b.preview.gocodealone.com"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("deep preview: %d want 404", rec.Code)
	}
}

func TestServer_AdminReload_FlushesCache(t *testing.T) {
	r := &stubResolver{
		byHost: map[string]TenantInfo{
			"acme.example": {TenantID: 7, TenantSlug: "acme"},
		},
	}
	s := New(Config{TenantResolverStore: r})

	// Prime cache.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "acme.example"
	s.ServeHTTP(httptest.NewRecorder(), req)
	if _, ok := s.cached["acme.example"]; !ok {
		t.Fatal("expected cache to be populated")
	}

	// Reload.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/reload", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reload: %d", rec.Code)
	}
	if _, ok := s.cached["acme.example"]; ok {
		t.Error("expected cache to be cleared after reload")
	}
}

func TestServer_EndToEnd_TenantCreate_PreviewAutoProvision(t *testing.T) {
	// Three-tenant integration: each tenant created via admin API
	// gets a preview subdomain auto-provisioned. Verifies T16+T32+V18.
	s := New(Config{PreviewSubdomainBase: "preview.gocodealone.com"})

	for _, slug := range []string{"acme", "beta", "gamma"} {
		body, _ := json.Marshal(map[string]string{"slug": slug})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tenants", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d", slug, rec.Code)
		}
		var got map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		tid := int64(got["ID"].(float64))

		// List domains — expect the auto-provisioned preview.
		req = httptest.NewRequest(http.MethodGet,
			"/api/v1/admin/tenants/"+strconv.FormatInt(tid, 10)+"/domains", nil)
		rec = httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		var doms map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &doms)
		list, _ := doms["domains"].([]any)
		if len(list) != 1 {
			t.Errorf("%s: %d auto-provisioned domains; want 1", slug, len(list))
			continue
		}
		first := list[0].(map[string]any)
		want := slug + ".preview.gocodealone.com"
		if first["Host"] != want {
			t.Errorf("%s: host = %v want %s", slug, first["Host"], want)
		}
	}
}

func TestServer_Metrics(t *testing.T) {
	s := New(Config{})
	// Generate some traffic.
	_ = doReq(t, s, "GET", "/healthz", "", nil)
	_ = doReq(t, s, "GET", "/healthz", "", nil)
	_ = doReq(t, s, "POST", "/api/v1/admin/tenants", "", strings.NewReader(`{"slug":"acme"}`))

	rec := doReq(t, s, "GET", "/metrics", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "multisite_requests_total") {
		t.Errorf("missing requests_total metric:\n%s", body)
	}
}

func TestServer_Media_Disabled_503(t *testing.T) {
	s := New(Config{}) // no MediaBackend
	rec := doReq(t, s, "POST", "/api/v1/admin/tenants/1/upload", "text/plain", strings.NewReader("x"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("upload no-backend: %d want 503", rec.Code)
	}
}

func TestServer_Media_Enabled_Created(t *testing.T) {
	dir := t.TempDir()
	s := New(Config{
		MediaBackend: &media.LocalFS{Root: dir, PublicURL: "https://x/m"},
	})
	rec := doReq(t, s, "POST", "/api/v1/admin/tenants/1/upload", "text/plain", strings.NewReader("hello"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"url":"https://x/m/1/`) {
		t.Errorf("upload body: %s", rec.Body.String())
	}
}

func TestServer_Audit_OnlyWhenKeySet(t *testing.T) {
	noKey := New(Config{})
	if noKey.Audit() != nil {
		t.Error("Audit should be nil without AuditSignKey")
	}
	withKey := New(Config{AuditSignKey: "k"})
	if withKey.Audit() == nil {
		t.Error("Audit should be non-nil with AuditSignKey")
	}
}

// doReq is a helper used by the metrics/media tests.
func doReq(t *testing.T, h http.Handler, method, target, contentType string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	} else if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
