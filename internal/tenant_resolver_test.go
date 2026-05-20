package internal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// memStore is an in-memory TenantStore for tests.
type memStore struct {
	byDomain map[string]TenantInfo
	bySlug   map[string]TenantInfo
}

func (m *memStore) Lookup(_ context.Context, host string) (TenantInfo, bool) {
	v, ok := m.byDomain[host]
	return v, ok
}

func (m *memStore) LookupBySlug(_ context.Context, slug string) (TenantInfo, bool) {
	v, ok := m.bySlug[slug]
	return v, ok
}

func newResolver(t *testing.T, base string) *tenantResolverModule {
	t.Helper()
	mod, err := newTenantResolverModule("test", map[string]any{
		"preview_subdomain_base": base,
		"on_unknown_domain":      "404_neutral",
	})
	if err != nil {
		t.Fatalf("newTenantResolverModule: %v", err)
	}
	return mod.(*tenantResolverModule)
}

func TestResolver_ExactDomainHit(t *testing.T) {
	r := newResolver(t, "preview.example.com")
	r.SetStore(&memStore{
		byDomain: map[string]TenantInfo{
			"gocodealone.com": {TenantID: 1, TenantSlug: "gocodealone", Kind: "vanity"},
		},
	})

	gotTenant := TenantInfo{}
	handler := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotTenant, _ = TenantFromContext(req.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "http://gocodealone.com/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rec.Code)
	}
	if gotTenant.TenantSlug != "gocodealone" {
		t.Errorf("tenant slug: got %q want gocodealone", gotTenant.TenantSlug)
	}
}

func TestResolver_PreviewSubdomainFallback(t *testing.T) {
	r := newResolver(t, "preview.example.com")
	r.SetStore(&memStore{
		bySlug: map[string]TenantInfo{
			"bandname": {TenantID: 2, TenantSlug: "bandname"},
		},
	})

	gotTenant := TenantInfo{}
	handler := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotTenant, _ = TenantFromContext(req.Context())
	}))

	req := httptest.NewRequest("GET", "http://bandname.preview.example.com/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK && rec.Code != 0 {
		t.Errorf("status: got %d want 200/0", rec.Code)
	}
	if gotTenant.TenantSlug != "bandname" {
		t.Errorf("slug from preview: got %q want bandname", gotTenant.TenantSlug)
	}
	if gotTenant.Kind != "preview" {
		t.Errorf("kind: got %q want preview", gotTenant.Kind)
	}
}

func TestResolver_UnknownDomain404Neutral(t *testing.T) {
	r := newResolver(t, "preview.example.com")
	r.SetStore(&memStore{})

	called := false
	handler := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
	}))

	req := httptest.NewRequest("GET", "http://unknown.example.com/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rec.Code)
	}
	if called {
		t.Error("downstream handler should NOT have been called on unknown domain")
	}
	body := rec.Body.String()
	if body != "Not Found" {
		t.Errorf("body: got %q want %q", body, "Not Found")
	}
	// V16: no info leak.
	if rec.Header().Get("X-Tenant-Id") != "" {
		t.Error("response should not include tenant header on 404")
	}
}

func TestResolver_PortStrippedFromHost(t *testing.T) {
	r := newResolver(t, "")
	r.SetStore(&memStore{
		byDomain: map[string]TenantInfo{
			"gocodealone.com": {TenantSlug: "gocodealone"},
		},
	})

	hit := false
	handler := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hit = true
	}))

	req := httptest.NewRequest("GET", "http://gocodealone.com:8080/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !hit {
		t.Errorf("expected hit; got status %d (port should have been stripped)", rec.Code)
	}
}

func TestResolver_PreviewBaseEmpty_NoFallback(t *testing.T) {
	// When preview base is unset, slug fallback must not engage.
	r := newResolver(t, "")
	r.SetStore(&memStore{
		bySlug: map[string]TenantInfo{
			"bandname": {TenantSlug: "bandname"},
		},
	})

	hit := false
	handler := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hit = true
	}))

	req := httptest.NewRequest("GET", "http://bandname.preview.example.com/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if hit {
		t.Error("preview fallback engaged despite empty base")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rec.Code)
	}
}

func TestResolver_RejectDeepSubdomain(t *testing.T) {
	// V22: subsites are explicit. <slug>.<sub>.<base> must NOT resolve.
	r := newResolver(t, "preview.example.com")
	r.SetStore(&memStore{
		bySlug: map[string]TenantInfo{
			"bandname": {TenantSlug: "bandname"},
		},
	})

	hit := false
	handler := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hit = true
	}))

	req := httptest.NewRequest("GET", "http://shop.bandname.preview.example.com/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if hit {
		t.Error("deeper subdomain should not have resolved")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rec.Code)
	}
}

func TestStripPort(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"example.com", "example.com"},
		{"example.com:8080", "example.com"},
		{"localhost:3000", "localhost"},
		{"[::1]:8080", "[::1]"},
		{"[::1]", "[::1]"},
		{"", ""},
	}
	for _, tc := range cases {
		got := stripPort(tc.in)
		if got != tc.want {
			t.Errorf("stripPort(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestMatchPreviewSubdomain(t *testing.T) {
	cases := []struct {
		host, base string
		wantSlug   string
		wantOK     bool
	}{
		{"bandname.preview.example.com", "preview.example.com", "bandname", true},
		{"preview.example.com", "preview.example.com", "", false},      // host = base
		{"foo.preview.example.com", "", "", false},                     // empty base
		{"a.b.preview.example.com", "preview.example.com", "", false},  // deeper
		{"other.com", "preview.example.com", "", false},
	}
	for _, tc := range cases {
		slug, ok := matchPreviewSubdomain(tc.host, tc.base)
		if slug != tc.wantSlug || ok != tc.wantOK {
			t.Errorf("matchPreviewSubdomain(%q, %q)=(%q,%v) want (%q,%v)",
				tc.host, tc.base, slug, ok, tc.wantSlug, tc.wantOK)
		}
	}
}
