package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/analytics"
	"github.com/GoCodeAlone/workflow-plugin-cms/audit"
	"github.com/GoCodeAlone/workflow-plugin-cms/bundle"
	"github.com/GoCodeAlone/workflow-plugin-cms/media"
	"github.com/GoCodeAlone/workflow-plugin-cms/store"
	"github.com/GoCodeAlone/workflow/telemetry"
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
	// Tenant resolves, but this fixture does not configure a bundle or
	// CMS page for the root path.
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for resolved tenant with no content; got %d", rec.Code)
	}
}

func TestServer_TenantStaticBundleServesIndex(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "gocodealone", "v1.0.0")
	if err := os.MkdirAll(filepath.Join(versionDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "index.html"), []byte("<h1>GoCodeAlone</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "assets", "app.css"), []byte("body{color:#111}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(versionDir, filepath.Join(root, "gocodealone", "current")); err != nil {
		t.Fatal(err)
	}

	r := &stubResolver{
		byHost: map[string]TenantInfo{
			"gocodealone.tech": {TenantID: 1, TenantSlug: "gocodealone", Kind: "vanity"},
		},
	}
	s := New(Config{TenantResolverStore: r, BundleRoot: root})

	rec := doReqWithHost(t, s, "GET", "/", "gocodealone.tech", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("index status: got %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "GoCodeAlone") {
		t.Fatalf("index body = %q, want static bundle content", rec.Body.String())
	}

	rec = doReqWithHost(t, s, "GET", "/assets/app.css", "gocodealone.tech", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("asset status: got %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "color") {
		t.Fatalf("asset body = %q, want CSS content", rec.Body.String())
	}
}

func TestServer_TenantStaticBundleInjectsAnalytics(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "gocodealone", "v1.0.0")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "index.html"), []byte("<html><head></head><body>site</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(versionDir, filepath.Join(root, "gocodealone", "current")); err != nil {
		t.Fatal(err)
	}
	r := &stubResolver{
		byHost: map[string]TenantInfo{
			"gocodealone.tech": {TenantID: 1, TenantSlug: "gocodealone", Kind: "vanity"},
		},
	}
	s := New(Config{
		TenantResolverStore: r,
		BundleRoot:          root,
		AnalyticsConfigForTenant: func(tenantID int64) analytics.TenantConfig {
			if tenantID != 1 {
				return analytics.TenantConfig{}
			}
			return analytics.TenantConfig{GoogleMeasurementID: "G-VM9JNJRJW1"}
		},
	})

	rec := doReqWithHost(t, s, http.MethodGet, "/", "gocodealone.tech", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("index status: got %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "G-VM9JNJRJW1") {
		t.Fatalf("analytics measurement ID missing from static HTML: %q", rec.Body.String())
	}
}

func TestServer_TenantStaticBundleDoesNotInjectAnalyticsIntoAssets(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "gocodealone", "v1.0.0", "assets")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "app.css"), []byte("body{color:#111}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "gocodealone", "v1.0.0"), filepath.Join(root, "gocodealone", "current")); err != nil {
		t.Fatal(err)
	}
	r := &stubResolver{
		byHost: map[string]TenantInfo{
			"gocodealone.tech": {TenantID: 1, TenantSlug: "gocodealone", Kind: "vanity"},
		},
	}
	s := New(Config{
		TenantResolverStore: r,
		BundleRoot:          root,
		AnalyticsConfigForTenant: func(int64) analytics.TenantConfig {
			return analytics.TenantConfig{GoogleMeasurementID: "G-VM9JNJRJW1"}
		},
	})

	rec := doReqWithHost(t, s, http.MethodGet, "/assets/app.css", "gocodealone.tech", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("asset status: got %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "G-VM9JNJRJW1") || strings.Contains(rec.Body.String(), "googletagmanager.com") {
		t.Fatalf("asset should not contain analytics snippet: %q", rec.Body.String())
	}
}

func TestServer_TenantStaticBundleServesSPAFallback(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "gocodealone", "v1.0.0")
	if err := os.MkdirAll(filepath.Join(versionDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "index.html"), []byte("<h1>GoCodeAlone SPA</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(versionDir, filepath.Join(root, "gocodealone", "current")); err != nil {
		t.Fatal(err)
	}

	r := &stubResolver{
		byHost: map[string]TenantInfo{
			"www.gocodealone.tech": {TenantID: 1, TenantSlug: "gocodealone", Kind: "vanity"},
		},
	}
	s := New(Config{TenantResolverStore: r, BundleRoot: root})

	rec := doReqWithHost(t, s, "GET", "/platform", "www.gocodealone.tech", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("SPA route status: got %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "GoCodeAlone SPA") {
		t.Fatalf("SPA route body = %q, want index.html", rec.Body.String())
	}

	rec = doReqWithHost(t, s, "GET", "/assets/missing.js", "www.gocodealone.tech", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset status: got %d body=%q, want 404", rec.Code, rec.Body.String())
	}
}

func TestServer_PublicCMSPageRendersAfterStaticMiss(t *testing.T) {
	pages := store.NewMemoryPageStore()
	if err := pages.Create(context.Background(), 1, &store.Page{
		TenantID: 1,
		Path:     "/about",
		Title:    "About",
		BodyHTML: "<main>about cms page</main>",
		Status:   store.StatusPublished,
	}); err != nil {
		t.Fatal(err)
	}
	r := &stubResolver{
		byHost: map[string]TenantInfo{
			"gocodealone.tech": {TenantID: 1, TenantSlug: "gocodealone", Kind: "vanity"},
		},
	}
	s := New(Config{TenantResolverStore: r, Pages: pages})

	rec := doReqWithHost(t, s, http.MethodGet, "/about", "gocodealone.tech", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("CMS page status: got %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "about cms page") {
		t.Fatalf("CMS page body = %q, want stored HTML", rec.Body.String())
	}
}

func TestServer_PublicCMSPageInjectsAnalytics(t *testing.T) {
	pages := store.NewMemoryPageStore()
	if err := pages.Create(context.Background(), 1, &store.Page{
		TenantID: 1,
		Path:     "/about",
		Title:    "About",
		BodyHTML: "<html><head></head><body>about cms page</body></html>",
		Status:   store.StatusPublished,
	}); err != nil {
		t.Fatal(err)
	}
	r := &stubResolver{
		byHost: map[string]TenantInfo{
			"gocodealone.tech": {TenantID: 1, TenantSlug: "gocodealone", Kind: "vanity"},
		},
	}
	s := New(Config{
		TenantResolverStore: r,
		Pages:               pages,
		AnalyticsConfigForTenant: func(tenantID int64) analytics.TenantConfig {
			if tenantID != 1 {
				return analytics.TenantConfig{}
			}
			return analytics.TenantConfig{GoogleMeasurementID: "G-VM9JNJRJW1", AnonymizeIP: true}
		},
	})

	rec := doReqWithHost(t, s, http.MethodGet, "/about", "gocodealone.tech", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("CMS page status: got %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "G-VM9JNJRJW1") || !strings.Contains(rec.Body.String(), "anonymize_ip") {
		t.Fatalf("analytics snippet missing from CMS HTML: %q", rec.Body.String())
	}
}

func TestServer_PublicCMSPageUsesBodyBlocksAndTemplate(t *testing.T) {
	pages := store.NewMemoryPageStore()
	if err := pages.Create(context.Background(), 1, &store.Page{
		TenantID:   1,
		Path:       "/about",
		Title:      "About",
		BodyHTML:   "<p>stale html</p>",
		BodyBlocks: json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"fresh blocks"}]}]}`),
		Status:     store.StatusPublished,
		TemplateID: "default",
	}); err != nil {
		t.Fatal(err)
	}
	r := &stubResolver{
		byHost: map[string]TenantInfo{
			"gocodealone.tech": {TenantID: 1, TenantSlug: "gocodealone", Kind: "vanity"},
		},
	}
	s := New(Config{
		TenantResolverStore: r,
		Pages:               pages,
		PageTemplates: map[string]string{
			"default": "<html><body><header>site shell</header><!--cms:body--></body></html>",
		},
	})

	rec := doReqWithHost(t, s, http.MethodGet, "/about", "gocodealone.tech", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("CMS page status: got %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "site shell") || !strings.Contains(body, "<p>fresh blocks</p>") {
		t.Fatalf("CMS page body = %q, want template shell and rendered blocks", body)
	}
	if strings.Contains(body, "stale html") {
		t.Fatalf("body_html rendered despite body_blocks being present: %q", body)
	}
}

func TestServer_PublicCMSDraftDoesNotRender(t *testing.T) {
	pages := store.NewMemoryPageStore()
	if err := pages.Create(context.Background(), 1, &store.Page{
		TenantID: 1,
		Path:     "/draft",
		Title:    "Draft",
		BodyHTML: "<main>draft</main>",
		Status:   store.StatusDraft,
	}); err != nil {
		t.Fatal(err)
	}
	r := &stubResolver{
		byHost: map[string]TenantInfo{
			"gocodealone.tech": {TenantID: 1, TenantSlug: "gocodealone", Kind: "vanity"},
		},
	}
	s := New(Config{TenantResolverStore: r, Pages: pages})

	rec := doReqWithHost(t, s, http.MethodGet, "/draft", "gocodealone.tech", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("draft page status: got %d body=%q, want 404", rec.Code, rec.Body.String())
	}
}

func TestServer_PublicCMSScheduledPageWaitsForPublishAt(t *testing.T) {
	publishAt := time.Now().UTC().Add(time.Hour)
	pages := store.NewMemoryPageStore()
	if err := pages.Create(context.Background(), 1, &store.Page{
		TenantID:  1,
		Path:      "/launch",
		Title:     "Launch",
		BodyHTML:  "<main>launch</main>",
		Status:    store.StatusScheduled,
		PublishAt: &publishAt,
	}); err != nil {
		t.Fatal(err)
	}
	r := &stubResolver{
		byHost: map[string]TenantInfo{
			"gocodealone.tech": {TenantID: 1, TenantSlug: "gocodealone", Kind: "vanity"},
		},
	}
	s := New(Config{TenantResolverStore: r, Pages: pages})

	rec := doReqWithHost(t, s, http.MethodGet, "/launch", "gocodealone.tech", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("future scheduled page status: got %d body=%q, want 404", rec.Code, rec.Body.String())
	}

	publishAt = time.Now().UTC().Add(-time.Second)
	page, err := pages.GetByPath(context.Background(), 1, "", "/launch")
	if err != nil {
		t.Fatal(err)
	}
	page.PublishAt = &publishAt
	if err := pages.Update(context.Background(), 1, page); err != nil {
		t.Fatal(err)
	}
	rec = doReqWithHost(t, s, http.MethodGet, "/launch", "gocodealone.tech", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "launch") {
		t.Fatalf("active scheduled page: got status=%d body=%q, want rendered", rec.Code, rec.Body.String())
	}
}

func TestServer_StaticExactWinsBeforeCMSPage(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "gocodealone", "v1.0.0")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "about.html"), []byte("static about"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(versionDir, filepath.Join(root, "gocodealone", "current")); err != nil {
		t.Fatal(err)
	}
	pages := store.NewMemoryPageStore()
	if err := pages.Create(context.Background(), 1, &store.Page{
		TenantID: 1,
		Path:     "/about.html",
		Title:    "About",
		BodyHTML: "cms about",
		Status:   store.StatusPublished,
	}); err != nil {
		t.Fatal(err)
	}
	r := &stubResolver{
		byHost: map[string]TenantInfo{
			"gocodealone.tech": {TenantID: 1, TenantSlug: "gocodealone", Kind: "vanity"},
		},
	}
	s := New(Config{TenantResolverStore: r, BundleRoot: root, Pages: pages})

	rec := doReqWithHost(t, s, http.MethodGet, "/about.html", "gocodealone.tech", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("static exact status: got %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "static about" {
		t.Fatalf("body = %q, want exact static content", rec.Body.String())
	}
}

func TestServer_CMSPageBeatsSPAFallback(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "gocodealone", "v1.0.0")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "index.html"), []byte("spa index"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(versionDir, filepath.Join(root, "gocodealone", "current")); err != nil {
		t.Fatal(err)
	}
	pages := store.NewMemoryPageStore()
	if err := pages.Create(context.Background(), 1, &store.Page{
		TenantID: 1,
		Path:     "/platform",
		Title:    "Platform",
		BodyHTML: "cms platform",
		Status:   store.StatusPublished,
	}); err != nil {
		t.Fatal(err)
	}
	r := &stubResolver{
		byHost: map[string]TenantInfo{
			"gocodealone.tech": {TenantID: 1, TenantSlug: "gocodealone", Kind: "vanity"},
		},
	}
	s := New(Config{TenantResolverStore: r, BundleRoot: root, Pages: pages})

	rec := doReqWithHost(t, s, http.MethodGet, "/platform", "gocodealone.tech", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("CMS before SPA status: got %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "cms platform" {
		t.Fatalf("body = %q, want CMS content before SPA fallback", rec.Body.String())
	}
}

func TestServer_CMSSubsitePageFallsBackToRootSubsite(t *testing.T) {
	pages := store.NewMemoryPageStore()
	if err := pages.Create(context.Background(), 1, &store.Page{
		TenantID: 1,
		Subsite:  "",
		Path:     "/contact",
		Title:    "Root Contact",
		BodyHTML: "root contact",
		Status:   store.StatusPublished,
	}); err != nil {
		t.Fatal(err)
	}
	if err := pages.Create(context.Background(), 1, &store.Page{
		TenantID: 1,
		Subsite:  "music",
		Path:     "/tour",
		Title:    "Tour",
		BodyHTML: "music tour",
		Status:   store.StatusPublished,
	}); err != nil {
		t.Fatal(err)
	}
	r := &stubResolver{
		byHost: map[string]TenantInfo{
			"band.example": {TenantID: 1, TenantSlug: "gocodealone", SubsiteLabel: "music", Kind: "vanity"},
		},
	}
	s := New(Config{TenantResolverStore: r, Pages: pages})

	rec := doReqWithHost(t, s, http.MethodGet, "/tour", "band.example", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "music tour" {
		t.Fatalf("subsite page: got status=%d body=%q, want music tour", rec.Code, rec.Body.String())
	}
	rec = doReqWithHost(t, s, http.MethodGet, "/contact", "band.example", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "root contact" {
		t.Fatalf("root fallback page: got status=%d body=%q, want root contact", rec.Code, rec.Body.String())
	}
}

func TestServer_AdminHostRootRequiresAuth(t *testing.T) {
	s := New(Config{
		AdminHost: "admin.example",
		AdminAuth: func(r *http.Request) bool {
			return false
		},
	})

	rec := doReqWithHost(t, s, "GET", "/", "admin.example", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin root without auth: got %d body=%q, want 401", rec.Code, rec.Body.String())
	}
}

func TestServer_AdminHostRootServesAdminUIWhenAuthorized(t *testing.T) {
	s := New(Config{
		AdminHost: "admin.example",
		AdminAuth: func(r *http.Request) bool {
			return true
		},
	})

	rec := doReqWithHost(t, s, "GET", "/", "admin.example", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin root with auth: got %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Multisite Admin") {
		t.Fatalf("admin root body = %q, want admin UI", rec.Body.String())
	}
}

func TestServer_PublicAdminPathNotExposedWhenAdminHostConfigured(t *testing.T) {
	s := New(Config{
		AdminHost: "admin.example",
		AdminAuth: func(r *http.Request) bool {
			return true
		},
	})

	rec := doReqWithHost(t, s, "GET", "/admin/", "www.example", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("public /admin/: got %d body=%q, want 404", rec.Code, rec.Body.String())
	}
}

func TestServer_AdminAPIRequiresAuthWhenAdminHostConfigured(t *testing.T) {
	s := New(Config{
		AdminHost: "admin.example",
		AdminAuth: func(r *http.Request) bool {
			return r.Header.Get("Authorization") == "Bearer ok"
		},
	})

	rec := doReqWithHost(t, s, "GET", "/api/v1/admin/tenants", "admin.example", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin API without auth: got %d body=%q, want 401", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://admin.example/api/v1/admin/tenants", nil)
	req.Host = "admin.example"
	req.Header.Set("Authorization", "Bearer ok")
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin API with auth: got %d body=%q, want 200", rec.Code, rec.Body.String())
	}
}

func TestServer_AdminCreatedTenantResolvesStaticBundle(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "acme", "v1.0.0")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "index.html"), []byte("hello acme"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(versionDir, filepath.Join(root, "acme", "current")); err != nil {
		t.Fatal(err)
	}
	s := New(Config{BundleRoot: root})

	rec := doReq(t, s, "POST", "/api/v1/admin/tenants", "application/json", strings.NewReader(`{"slug":"acme"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create tenant: got %d body=%q, want 201", rec.Code, rec.Body.String())
	}
	var tenant map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &tenant); err != nil {
		t.Fatal(err)
	}
	tid := int64(tenant["ID"].(float64))
	rec = doReq(t, s, "POST", "/api/v1/admin/tenants/"+strconv.FormatInt(tid, 10)+"/domains", "application/json", strings.NewReader(`{"host":"acme.example","kind":"vanity"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create domain: got %d body=%q, want 201", rec.Code, rec.Body.String())
	}

	rec = doReqWithHost(t, s, "GET", "/", "acme.example", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant static status: got %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello acme") {
		t.Fatalf("tenant static body = %q, want activated bundle", rec.Body.String())
	}
}

func TestServer_AdminMutationsRecordAuditEntries(t *testing.T) {
	sink := audit.NewMemorySink()
	s := New(Config{
		AuditSignKey: "test-sign-key",
		AuditSink:    sink,
		AuditActor: func(r *http.Request) string {
			return r.Header.Get("X-Actor")
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tenants", strings.NewReader(`{"slug":"acme"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Actor", "alice")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create tenant: got %d body=%q, want 201", rec.Code, rec.Body.String())
	}
	var tenant map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &tenant); err != nil {
		t.Fatal(err)
	}
	tid := int64(tenant["ID"].(float64))

	rec = doReq(t, s, http.MethodPost, "/api/v1/admin/tenants/"+strconv.FormatInt(tid, 10)+"/pages", "application/json",
		strings.NewReader(`{"path":"/welcome","title":"Welcome","body_html":"<h1>Welcome</h1>","status":"published"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create page: got %d body=%q, want 201", rec.Code, rec.Body.String())
	}

	entries, err := sink.List(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("audit entries = %d, want 2: %+v", len(entries), entries)
	}
	if entries[0].Actor != "alice" || entries[0].Action != "tenant.create" || entries[0].Subject != "tenant:"+strconv.FormatInt(tid, 10) {
		t.Fatalf("tenant audit entry = %+v", entries[0])
	}
	if entries[1].Actor != "admin" || entries[1].TenantID != tid || entries[1].Action != "page.create" {
		t.Fatalf("page audit entry = %+v", entries[1])
	}
	if broken, err := s.Audit().Verify(0); err != nil || broken != -1 {
		t.Fatalf("audit verify = (%d, %v), want clean", broken, err)
	}
}

func TestServer_AutoPreviewDomainRecordsAuditEntry(t *testing.T) {
	sink := audit.NewMemorySink()
	s := New(Config{
		PreviewSubdomainBase: "preview.example",
		AuditSignKey:         "test-sign-key",
		AuditSink:            sink,
	})

	rec := doReq(t, s, http.MethodPost, "/api/v1/admin/tenants", "application/json", strings.NewReader(`{"slug":"acme"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create tenant: got %d body=%q, want 201", rec.Code, rec.Body.String())
	}

	entries, err := sink.List(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("audit entries = %d, want tenant.create + domain.create: %+v", len(entries), entries)
	}
	if entries[1].Action != "domain.create" || entries[1].Meta["auto_preview"] != true {
		t.Fatalf("preview domain audit entry = %+v", entries[1])
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
		// Same as above: preview resolved, but no content is configured.
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

func TestServer_EmitMetricsParity(t *testing.T) {
	s := New(Config{})
	// Generate some traffic.
	_ = doReq(t, s, "GET", "/healthz", "", nil)
	_ = doReq(t, s, "GET", "/healthz", "", nil)
	_ = doReq(t, s, "POST", "/api/v1/admin/tenants", "", strings.NewReader(`{"slug":"acme"}`))

	recorder := telemetry.NewSnapshotRecorder()
	if err := s.EmitMetrics(context.Background(), recorder); err != nil {
		t.Fatal(err)
	}
	metrics := recorder.Metrics()
	assertMetric(t, metrics, "multisite_requests_total", 3, nil)
	assertMetric(t, metrics, "multisite_tenant_requests_total", 3, telemetry.Attrs{"tenant": "_unresolved"})
}

func TestServerMetricsEndpointRemoved(t *testing.T) {
	s := New(Config{})
	rec := doReq(t, s, "GET", "/metrics", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/metrics status = %d, want 404", rec.Code)
	}
}

func assertMetric(t *testing.T, metrics []telemetry.MetricRecord, name string, value float64, attrs telemetry.Attrs) {
	t.Helper()
	for _, metric := range metrics {
		if metric.Name != name || metric.Value != value {
			continue
		}
		if attrsMatch(metric.Attrs, attrs) {
			return
		}
	}
	t.Fatalf("missing metric %s=%v attrs=%v in %#v", name, value, attrs, metrics)
}

func attrsMatch(got, want telemetry.Attrs) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
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

func doReqWithHost(t *testing.T, h http.Handler, method, target, host, contentType string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://"+host+target, body)
	req.Host = host
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	} else if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
