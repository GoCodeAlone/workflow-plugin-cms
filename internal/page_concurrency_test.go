package internal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

func TestAdminPageSaveDeleteClientPreconditions(t *testing.T) {
	pages := store.NewMemoryPageStore()
	api := NewAdminAPI(store.NewMemoryTenantAdminStore(), pages)
	p := &store.Page{Path: "/", Title: "Initial", Status: store.StatusDraft}
	if pages.Create(context.Background(), 1, p) != nil {
		t.Fatal("create failed")
	}
	url := "/api/v1/admin/tenants/1/pages/" + strconv.FormatInt(p.ID, 10)
	for _, method := range []string{"PUT", "DELETE"} {
		w, _ := doJSON(t, api, method, url, map[string]any{})
		if w.Code != http.StatusPreconditionRequired {
			t.Fatal("missing loaded version accepted")
		}
	}
	content := store.ContentOf(p)
	content.Title = "Approved promotion"
	state, _ := pages.ReadPageState(context.Background(), 1)
	_, err := pages.ApplyPageBatch(context.Background(), 1, store.PageBatch{TargetScope: state.Scope, BaselineRevision: state.Revision, Baseline: store.Baseline(state.Pages), Mutations: []store.PageMutation{{Key: "approved", Kind: "update", TargetID: p.ID, ExpectedVersion: p.Version, Content: &content}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		payload := map[string]any{"expected_version": p.Version}
		if method == "PUT" {
			payload["title"] = "Stale browser draft"
		}
		w, _ := doJSON(t, api, method, url, payload)
		if w.Code != http.StatusConflict {
			t.Fatal("stale browser operation accepted")
		}
	}
	got, _ := pages.Get(context.Background(), 1, p.ID)
	if got.Title != content.Title {
		t.Fatal("API used freshly fetched version to overwrite approved content")
	}
	w, saved := doJSON(t, api, "PUT", url, map[string]any{"expected_version": got.Version, "title": "Fresh browser save"})
	if w.Code != http.StatusOK {
		t.Fatal("fresh version refused")
	}
	w, _ = doJSON(t, api, "DELETE", url, map[string]any{"expected_version": saved["Version"]})
	if w.Code != http.StatusNoContent {
		t.Fatal("fresh delete refused")
	}
}

func TestAdminJSONRejectedFieldsNeverEchoValues(t *testing.T) {
	api := newAdminTestAPI()
	for _, body := range []string{`{"path":"/","title":"Page","rejected_marker":"credential_fixture"}`, `{"path":"/","title":"Page"} {"rejected_marker":"credential_fixture"}`} {
		w := httptest.NewRecorder()
		api.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/admin/tenants/1/pages", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "credential_fixture") || strings.Contains(w.Body.String(), "rejected_marker") {
			t.Fatal("invalid request leaked rejected fields")
		}
	}
}
