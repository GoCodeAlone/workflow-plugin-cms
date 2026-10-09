package internal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

func historyAPI() *AdminAPI {
	api := newAdminTestAPI()
	api.RequestAccess = func(*http.Request) bool { return true }
	api.TenantAccess = func(_ *http.Request, id int64) bool { return id == 234 }
	api.AuditActor = func(*http.Request) string { return "user:authenticated-owner" }
	return api
}

func historyResponse(t *testing.T, api *AdminAPI, path string) (*httptest.ResponseRecorder, store.PageHistory) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	var h store.PageHistory
	if w.Code == 200 && json.Unmarshal(w.Body.Bytes(), &h) != nil {
		t.Fatal("invalid history JSON")
	}
	return w, h
}

func TestAdminSavedPageHistoryAndTrustedActor(t *testing.T) {
	api := historyAPI()
	base := "/api/v1/admin/tenants/234/pages"
	w, p := doJSON(t, api, http.MethodPost, base, map[string]any{"path": "/rock-metal", "title": "Rock", "body_html": "<p>Saved</p>", "status": "draft"})
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	w, _ = doJSON(t, api, http.MethodPut, base+"/1", map[string]any{"expected_version": p["Version"], "title": "Updated"})
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w, h := historyResponse(t, api, base+"/history?limit=1")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" || len(h.Entries) != 1 || !h.HasMore || h.CurrentRevision != 2 || h.Entries[0].Actor != "user:authenticated-owner" || h.Entries[0].After[0].Content.BodyHTML != "<p>Saved</p>" {
		t.Fatal("protected saved history incorrect")
	}
	w, h = historyResponse(t, api, base+"/history?after_revision=1&limit=1")
	if w.Code != 200 || len(h.Entries) != 1 || h.Entries[0].Before[0].Content.Title != "Rock" || h.Entries[0].After[0].Content.Title != "Updated" {
		t.Fatal("saved update readback incorrect")
	}
	// Neither JSON nor an arbitrary actor header can supply trusted identity.
	w, _ = doJSON(t, api, http.MethodPut, base+"/1", map[string]any{"expected_version": 2, "title": "Forged", "actor": "superadmin"})
	if w.Code != 400 {
		t.Fatal("JSON actor accepted")
	}
	api.AuditActor = nil
	r := httptest.NewRequest(http.MethodPut, base+"/1", strings.NewReader("{\"expected_version\":2,\"title\":\"Header ignored\"}"))
	r.Header.Set("X-Actor", "superadmin")
	w = httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w, h = historyResponse(t, api, base+"/history?after_revision=2")
	if w.Code != 200 || len(h.Entries) != 1 || h.Entries[0].Actor != "unattributed" {
		t.Fatal("untrusted header fabricated identity")
	}
	w, _ = doJSON(t, api, http.MethodDelete, base+"/1", map[string]any{"expected_version": 3})
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	w, h = historyResponse(t, api, base+"/history?after_revision=3")
	if w.Code != 200 || len(h.Entries) != 1 || h.Entries[0].Operation != "delete" || h.Entries[0].Before[0].Content.Title != "Header ignored" {
		t.Fatal("deleted page history missing")
	}
}

func TestAdminHistoryFailsClosedAndCanonicalRoutes(t *testing.T) {
	path := "/api/v1/admin/tenants/234/pages/history"
	for _, missing := range []string{"request", "tenant"} {
		api := historyAPI()
		if missing == "request" {
			api.RequestAccess = nil
		} else {
			api.TenantAccess = nil
		}
		w, _ := historyResponse(t, api, path)
		if w.Code != 503 {
			t.Fatal("unconfigured history authority allowed")
		}
	}
	api := historyAPI()
	api.RequestAccess = func(*http.Request) bool { return false }
	if w, _ := historyResponse(t, api, path); w.Code != 403 {
		t.Fatal("denied request allowed")
	}
	api = historyAPI()
	if w, _ := historyResponse(t, api, "/api/v1/admin/tenants/235/pages/history"); w.Code != 403 {
		t.Fatal("foreign tenant history allowed")
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=01", "?after_revision=-1", "?after_revision=01", "?limit=1&limit=2", "?actor=x", "?limit=1;bad", "?limit=%zz"} {
		if w, _ := historyResponse(t, api, path+query); w.Code != 400 {
			t.Fatalf("noncanonical query allowed: %s", query)
		}
	}
	for _, bad := range []string{"/api/v1/admin/tenants/0234/pages/history", path + "/1", path + "/history"} {
		if w, _ := historyResponse(t, api, bad); w.Code != 404 {
			t.Fatal("noncanonical history route allowed")
		}
	}
}
