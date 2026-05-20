package internal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

func newAdminTestAPI() *AdminAPI {
	return NewAdminAPI(store.NewMemoryTenantAdminStore(), store.NewMemoryPageStore())
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var got map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
	}
	return rec, got
}

func TestAdmin_CreateTenant(t *testing.T) {
	api := newAdminTestAPI()
	rec, got := doJSON(t, api, http.MethodPost, "/api/v1/admin/tenants",
		map[string]string{"slug": "acme", "label": "Acme Inc", "theme_id": "default"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%v", rec.Code, got)
	}
	if got["Slug"] != "acme" {
		t.Errorf("create: slug=%v want acme", got["Slug"])
	}
}

func TestAdmin_CreateTenant_BadRequest(t *testing.T) {
	api := newAdminTestAPI()
	// Missing slug.
	rec, got := doJSON(t, api, http.MethodPost, "/api/v1/admin/tenants",
		map[string]string{"label": "X"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing slug: status=%d body=%v", rec.Code, got)
	}
}

func TestAdmin_CreateTenant_DuplicateConflict(t *testing.T) {
	api := newAdminTestAPI()
	_, _ = doJSON(t, api, http.MethodPost, "/api/v1/admin/tenants", map[string]string{"slug": "dup"})
	rec, _ := doJSON(t, api, http.MethodPost, "/api/v1/admin/tenants", map[string]string{"slug": "dup"})
	if rec.Code != http.StatusConflict {
		t.Errorf("dup slug: status=%d want 409", rec.Code)
	}
}

func TestAdmin_ListTenants(t *testing.T) {
	api := newAdminTestAPI()
	_, _ = doJSON(t, api, http.MethodPost, "/api/v1/admin/tenants", map[string]string{"slug": "a"})
	_, _ = doJSON(t, api, http.MethodPost, "/api/v1/admin/tenants", map[string]string{"slug": "b"})
	rec, got := doJSON(t, api, http.MethodGet, "/api/v1/admin/tenants", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %v", rec.Code, got)
	}
	tenants, _ := got["tenants"].([]any)
	if len(tenants) != 2 {
		t.Errorf("list: %d tenants want 2", len(tenants))
	}
}

func TestAdmin_DomainCRUD(t *testing.T) {
	api := newAdminTestAPI()
	// Create tenant.
	rec, got := doJSON(t, api, http.MethodPost, "/api/v1/admin/tenants", map[string]string{"slug": "acme"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d %v", rec.Code, got)
	}
	id := int64(got["ID"].(float64))

	// Create domain.
	rec, got = doJSON(t, api, http.MethodPost, "/api/v1/admin/tenants/"+strconv.FormatInt(id, 10)+"/domains",
		map[string]string{"host": "acme.example", "kind": "vanity"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create domain: %d %v", rec.Code, got)
	}
	domainID := int64(got["ID"].(float64))

	// List.
	rec, got = doJSON(t, api, http.MethodGet, "/api/v1/admin/tenants/"+strconv.FormatInt(id, 10)+"/domains", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list domains: %d", rec.Code)
	}
	domains, _ := got["domains"].([]any)
	if len(domains) != 1 {
		t.Errorf("list: %d domains want 1", len(domains))
	}

	// Delete.
	rec, _ = doJSON(t, api, http.MethodDelete,
		"/api/v1/admin/tenants/"+strconv.FormatInt(id, 10)+"/domains/"+strconv.FormatInt(domainID, 10), nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d want 204", rec.Code)
	}
}

func TestAdmin_DomainCrossTenant_404(t *testing.T) {
	api := newAdminTestAPI()
	// Two tenants.
	_, gotA := doJSON(t, api, http.MethodPost, "/api/v1/admin/tenants", map[string]string{"slug": "a"})
	_, gotB := doJSON(t, api, http.MethodPost, "/api/v1/admin/tenants", map[string]string{"slug": "b"})
	aID := int64(gotA["ID"].(float64))
	bID := int64(gotB["ID"].(float64))

	// Domain belongs to A.
	_, gotD := doJSON(t, api, http.MethodPost,
		"/api/v1/admin/tenants/"+strconv.FormatInt(aID, 10)+"/domains",
		map[string]string{"host": "a.example"})
	dID := int64(gotD["ID"].(float64))

	// B tries to delete A's domain → 404 (no info leak).
	rec, _ := doJSON(t, api, http.MethodDelete,
		"/api/v1/admin/tenants/"+strconv.FormatInt(bID, 10)+"/domains/"+strconv.FormatInt(dID, 10), nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant delete: %d want 404", rec.Code)
	}
}

func TestAdmin_ListPages_TenantScoped(t *testing.T) {
	api := newAdminTestAPI()
	_, got := doJSON(t, api, http.MethodPost, "/api/v1/admin/tenants", map[string]string{"slug": "a"})
	id := int64(got["ID"].(float64))

	rec, body := doJSON(t, api, http.MethodGet,
		"/api/v1/admin/tenants/"+strconv.FormatInt(id, 10)+"/pages?subsite=main", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list pages: %d %v", rec.Code, body)
	}
	pages, _ := body["pages"].([]any)
	if len(pages) != 0 {
		t.Errorf("expected 0 pages for new tenant; got %d", len(pages))
	}
}

func TestAdmin_UnknownRoute_404(t *testing.T) {
	api := newAdminTestAPI()
	rec, _ := doJSON(t, api, http.MethodGet, "/api/v1/admin/nope", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown: %d want 404", rec.Code)
	}
}

func TestAdmin_RejectUnknownFields(t *testing.T) {
	api := newAdminTestAPI()
	body := strings.NewReader(`{"slug":"a","unknown":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tenants", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d want 400", rec.Code)
	}
}
