package postgres

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/adminui"
	cms "github.com/GoCodeAlone/workflow-plugin-cms/internal"
	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

// Optional actual browser proof using already installed Playwright/Chrome. This
// fixture has a fresh PG schema and a task-owned ephemeral loopback listener;
// its authority and promotion helper exist only inside this test binary.
func TestPostgresEditorVersionBrowser(t *testing.T) {
	if os.Getenv("CMS_TEST_PLAYWRIGHT_PATH") == "" || os.Getenv("CMS_TEST_BROWSER_EXECUTABLE") == "" {
		t.Skip("existing browser tools not configured")
	}
	s := isolatedPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tenant := &store.Tenant{Slug: "browser-review", Label: "Isolated browser review"}
	if s.CreateTenant(ctx, tenant) != nil {
		t.Fatal("browser tenant fixture failed")
	}
	p := &store.Page{Path: "/browser-home", Title: "Browser initial", BodyHTML: "<p>Initial browser content</p>", Status: store.StatusDraft}
	if s.Create(ctx, tenant.ID, p) != nil {
		t.Fatal("browser page fixture failed")
	}
	api := cms.NewAdminAPI(s, s)
	api.RequestAccess = func(*http.Request) bool { return true }
	api.TenantAccess = func(_ *http.Request, id int64) bool { return id == tenant.ID }
	api.ListTemplates = func(context.Context, int64) ([]string, error) { return []string{}, nil }
	mux := http.NewServeMux()
	mux.Handle("/api/v1/admin/", api)
	mux.Handle("/admin/", http.StripPrefix("/admin", adminui.Handler()))
	mux.HandleFunc("/fixture/promote", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		state, err := s.ReadPageState(r.Context(), tenant.ID)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		current, err := s.Get(r.Context(), tenant.ID, p.ID)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		content := store.ContentOf(current)
		content.Title = "Approved fixture promotion"
		_, err = s.ApplyPageBatch(r.Context(), tenant.ID, store.PageBatch{TargetScope: state.Scope, BaselineRevision: state.Revision, Baseline: store.Baseline(state.Pages), Mutations: []store.PageMutation{{Key: "approved-fixture", Kind: "update", TargetID: p.ID, ExpectedVersion: current.Version, Content: &content}}})
		if err != nil {
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"promoted": true})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	command := exec.CommandContext(ctx, "node", "testdata/editor-version.cjs")
	command.Env = append(os.Environ(), "CMS_TEST_EDITOR_ORIGIN="+server.URL, "CMS_TEST_TENANT_ID="+strconv.FormatInt(tenant.ID, 10), "CMS_TEST_PAGE_ID="+strconv.FormatInt(p.ID, 10))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated editor browser proof failed: %s", output)
	}
	t.Log(string(output))
	got, err := s.Get(ctx, tenant.ID, p.ID)
	if err != nil || got.Title != "Fresh reviewed edit" || got.Version != 5 {
		t.Fatal("browser save did not persist across actual API/DB reload")
	}
}
