package internal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newStaticServe(t *testing.T, bundleRoot string) *staticServeModule {
	t.Helper()
	mod, err := newStaticServeModule("test", map[string]any{"bundle_root": bundleRoot})
	if err != nil {
		t.Fatalf("newStaticServeModule: %v", err)
	}
	return mod.(*staticServeModule)
}

// makeBundle creates a fake tenant bundle layout for tests.
//
//	<bundleRoot>/<slug>/current/<files…>
func makeBundle(t *testing.T, root, slug string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, slug, "current")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStaticServe_HitServesFile(t *testing.T) {
	root := t.TempDir()
	makeBundle(t, root, "gocodealone", map[string]string{
		"index.html": "<h1>Hi</h1>",
	})

	m := newStaticServe(t, root)
	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler called on static hit")
	}))

	req := httptest.NewRequest("GET", "http://gocodealone.com/index.html", nil)
	req = req.WithContext(WithTenant(req.Context(), TenantInfo{TenantSlug: "gocodealone"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rec.Code)
	}
	if rec.Body.String() != "<h1>Hi</h1>" {
		t.Errorf("body: got %q want %q", rec.Body.String(), "<h1>Hi</h1>")
	}
}

func TestStaticServe_StaticHitShortCircuitsCMSFallback(t *testing.T) {
	root := t.TempDir()
	makeBundle(t, root, "gocodealone", map[string]string{
		"about.html": "static about",
	})

	m := newStaticServe(t, root)
	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("CMS fallback called despite exact static hit")
	}))

	req := httptest.NewRequest(http.MethodGet, "http://gocodealone.com/about.html", nil)
	req = req.WithContext(WithTenant(req.Context(), TenantInfo{TenantSlug: "gocodealone"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "static about" {
		t.Fatalf("body = %q, want exact static file", rec.Body.String())
	}
}

func TestStaticServe_RootResolvesToIndex(t *testing.T) {
	root := t.TempDir()
	makeBundle(t, root, "gocodealone", map[string]string{
		"index.html": "INDEX",
	})

	m := newStaticServe(t, root)
	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next called")
	}))

	req := httptest.NewRequest("GET", "http://gocodealone.com/", nil)
	req = req.WithContext(WithTenant(req.Context(), TenantInfo{TenantSlug: "gocodealone"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rec.Code)
	}
	if rec.Body.String() != "INDEX" {
		t.Errorf("body: got %q want INDEX", rec.Body.String())
	}
}

func TestStaticServe_MissFallsThrough(t *testing.T) {
	root := t.TempDir()
	makeBundle(t, root, "gocodealone", map[string]string{
		"index.html": "x",
	})

	m := newStaticServe(t, root)
	nextHit := false
	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextHit = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "http://gocodealone.com/missing.html", nil)
	req = req.WithContext(WithTenant(req.Context(), TenantInfo{TenantSlug: "gocodealone"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !nextHit {
		t.Error("expected fall-through to next handler on miss")
	}
}

func TestStaticServe_NoTenantContextFallsThrough(t *testing.T) {
	m := newStaticServe(t, t.TempDir())
	nextHit := false
	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextHit = true
	}))

	req := httptest.NewRequest("GET", "http://gocodealone.com/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !nextHit {
		t.Error("missing tenant context should fall through")
	}
}

func TestStaticServe_PathTraversalRejected(t *testing.T) {
	root := t.TempDir()
	// Plant a file OUTSIDE the tenant dir.
	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	// And the tenant's index for context.
	makeBundle(t, root, "gocodealone", map[string]string{
		"index.html": "OK",
	})

	m := newStaticServe(t, root)
	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Try several traversal patterns.
	for _, badPath := range []string{
		"/../../secret.txt",
		"/../secret.txt",
		"/foo/../../secret.txt",
	} {
		req := httptest.NewRequest("GET", "http://gocodealone.com"+badPath, nil)
		req = req.WithContext(WithTenant(req.Context(), TenantInfo{TenantSlug: "gocodealone"}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Body.String() == "SECRET" {
			t.Errorf("path %s leaked secret content", badPath)
		}
	}
}

func TestStaticServe_DirectoryNotServed(t *testing.T) {
	// A directory should NOT be served as a file even if it exists.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "gocodealone", "current", "blog"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := newStaticServe(t, root)
	nextHit := false
	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextHit = true
	}))

	req := httptest.NewRequest("GET", "http://gocodealone.com/blog", nil)
	req = req.WithContext(WithTenant(req.Context(), TenantInfo{TenantSlug: "gocodealone"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !nextHit {
		t.Error("directory should not be served as file; expected fall-through")
	}
}

func TestStaticServe_EmptyBundleRootFallsThrough(t *testing.T) {
	m := newStaticServe(t, "")
	nextHit := false
	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextHit = true
	}))

	req := httptest.NewRequest("GET", "http://gocodealone.com/", nil)
	req = req.WithContext(WithTenant(req.Context(), TenantInfo{TenantSlug: "gocodealone"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !nextHit {
		t.Error("empty bundle_root should fall through; got static serve")
	}
}

func TestResolvePath_EscapesRejected(t *testing.T) {
	m := &staticServeModule{bundleRoot: "/tmp/bundles"}
	for _, urlPath := range []string{"/../../etc/passwd", "/foo/../../../etc"} {
		_, ok := m.resolvePath("gocodealone", urlPath)
		if ok {
			t.Errorf("resolvePath should reject %q", urlPath)
		}
	}
}

func TestResolvePath_HappyPath(t *testing.T) {
	m := &staticServeModule{bundleRoot: "/tmp/bundles"}
	path, ok := m.resolvePath("gocodealone", "/assets/style.css")
	if !ok {
		t.Fatal("happy path rejected")
	}
	want := filepath.Join("/tmp/bundles", "gocodealone", "current", "assets", "style.css")
	if path != want {
		t.Errorf("path: got %q want %q", path, want)
	}
}

func TestStaticServe_LifecycleNoops(t *testing.T) {
	m := newStaticServe(t, t.TempDir())
	if err := m.Init(); err != nil {
		t.Errorf("Init: %v", err)
	}
	ctx := context.Background()
	if err := m.Start(ctx); err != nil {
		t.Errorf("Start: %v", err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Errorf("Stop: %v", err)
	}
}
