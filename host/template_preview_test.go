package host

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

func TestTenantBundleTemplatesAndAuthenticatedPreview(t *testing.T) {
	root := t.TempDir()
	tenants := store.NewMemoryTenantAdminStore()
	pages := store.NewMemoryPageStore()
	a := &store.Tenant{Slug: "a"}
	b := &store.Tenant{Slug: "b"}
	for _, tenant := range []*store.Tenant{a, b} {
		if err := tenants.CreateTenant(context.Background(), tenant); err != nil {
			t.Fatal(err)
		}
		_ = tenants.CreateDomain(context.Background(), &store.Domain{TenantID: tenant.ID, Host: tenant.Slug + ".test", Kind: "preview"})
	}
	dir := filepath.Join(root, "a", "current", "cms", "templates")
	_ = os.MkdirAll(dir, 0700)
	_ = os.WriteFile(filepath.Join(dir, "shell.html"), []byte(`<html><head><link rel="stylesheet" href="/assets/site.css"></head><body><!--cms:body--></body></html>`), 0600)
	assetDir := filepath.Join(root, "a", "current", "assets")
	_ = os.MkdirAll(assetDir, 0700)
	_ = os.WriteFile(filepath.Join(assetDir, "site.css"), []byte("body{color:navy}"), 0600)
	_ = os.WriteFile(filepath.Join(assetDir, "attack.html"), []byte("<script>alert(1)</script>"), 0600)
	_ = os.WriteFile(filepath.Join(assetDir, "fake.png"), []byte("<svg onload='alert(1)'></svg>"), 0600)
	_ = os.Symlink(filepath.Join(dir, "shell.html"), filepath.Join(assetDir, "escape.css"))
	for _, tenant := range []*store.Tenant{a, b} {
		_ = pages.Create(context.Background(), tenant.ID, &store.Page{Path: "/", Title: "Home", TemplateID: "shell", BodyHTML: "<p>Published</p>", Status: store.StatusPublished})
	}
	s := New(Config{BundleRoot: root, TenantsAdmin: tenants, Pages: pages, AdminHost: "admin.test", AdminAuth: func(r *http.Request) bool { return r.Header.Get("Authorization") == "test" }, AdminTenantAccess: func(r *http.Request, id int64) bool { return id == a.ID }})
	req := httptest.NewRequest("GET", "https://a.test/", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<html>") {
		t.Fatalf("template missing: %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest("GET", "https://b.test/", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Fatalf("tenant B reused A shell: %d", rec.Code)
	}
	for _, tc := range []struct {
		method, path, auth string
		want               int
	}{
		{"POST", "/api/v1/admin/tenants/1/pages/preview", "", 401},
		{"POST", "/api/v1/admin/tenants/2/pages/preview", "test", 403},
		{"POST", "/api/v1/admin/tenants/1/pages/preview", "test", 200},
		{"GET", "/api/v1/admin/tenants/1/pages/assets/assets/site.css", "test", 200},
		{"GET", "/api/v1/admin/tenants/1/pages/assets/assets/attack.html", "test", 404},
		{"GET", "/api/v1/admin/tenants/1/pages/assets/assets/fake.png", "test", 404},
		{"GET", "/api/v1/admin/tenants/1/pages/assets/assets/escape.css", "test", 404},
	} {
		req = httptest.NewRequest(tc.method, "https://admin.test"+tc.path, strings.NewReader(`{"title":"Draft","path":"/new","template_id":"shell","body_html":"<p>Unsaved</p>"}`))
		req.Header.Set("Authorization", tc.auth)
		rec = httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if tc.want == 200 && strings.HasSuffix(tc.path, "preview") && !strings.Contains(rec.Body.String(), "pages/assets/assets/site.css") {
			t.Error("preview lacks scoped asset path")
		}
	}
	// Bypass only the outer auth gate; the inner tenant hook must still deny.
	req = httptest.NewRequest("POST", "https://admin.test/api/v1/admin/tenants/2/pages", strings.NewReader(`{"path":"/leak","title":"Leak"}`))
	rec = httptest.NewRecorder()
	s.AdminAPI().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("inner membership bypass: %d", rec.Code)
	}
	// A current link cannot select another tenant's assets/templates, while
	// normal version links within the tenant remain supported by existing tests.
	_ = os.MkdirAll(filepath.Join(root, "b"), 0700)
	_ = os.Symlink(filepath.Join(root, "a", "current"), filepath.Join(root, "b", "current"))
	_, err := s.resolveTenantTemplate(context.Background(), b.ID, "shell")
	if err == nil {
		t.Fatal("tenant B current link reused A template")
	}
	if _, ok := resolveBundlePath(root, "b", "/assets/site.css"); ok {
		t.Fatal("tenant B current link reused A public asset")
	}
}
