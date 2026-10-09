package host

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

// The editor is served by the actual CMS host/API. This fixture's local cookie
// gate models the host-owned auth hook; it is not evidence of production auth.
func TestEditorNormalizationBrowserScenario(t *testing.T) {
	if os.Getenv("CMS_NORMALIZATION_BROWSER") != "1" {
		t.Skip("set CMS_NORMALIZATION_BROWSER=1 and CMS_PLAYWRIGHT_MODULE")
	}
	ctx := context.Background()
	tenants, pages := store.NewMemoryTenantAdminStore(), store.NewMemoryPageStore()
	tenant := &store.Tenant{Slug: "normalization-fixture"}
	if err := tenants.CreateTenant(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	page := &store.Page{TenantID: tenant.ID, Path: "/", Title: "Safe normalization", BodyHTML: `<section><h2>Voice & music</h2><div data-event-list></div></section><section><p title='Quoted text'>Second section</p></section>`, Status: store.StatusDraft}
	if err := pages.Create(ctx, tenant.ID, page); err != nil {
		t.Fatal(err)
	}
	const cookieValue = "isolated-editor-browser-cookie"
	authorized := func(r *http.Request) bool {
		cookie, err := r.Cookie("cms-browser-fixture")
		return err == nil && cookie.Value == cookieValue
	}
	server := httptest.NewTLSServer(New(Config{TenantsAdmin: tenants, Pages: pages, AdminAuth: authorized, AdminTenantAccess: func(_ *http.Request, id int64) bool { return id == tenant.ID }, AdminPlatformAccess: func(*http.Request) bool { return false }}))
	t.Cleanup(server.Close)
	payload, _ := json.Marshal(map[string]any{"url": server.URL, "cookie": "cms-browser-fixture", "token": cookieValue, "tenant": tenant.ID, "page": page.ID, "body": page.BodyHTML})
	deadline, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	command := exec.CommandContext(deadline, "node", "testdata/editor-normalization-browser.cjs")
	command.Stdin = strings.NewReader(string(payload))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("native editor browser scenario failed: %v\n%s", err, output)
	}
	if saved, err := pages.Get(ctx, tenant.ID, page.ID); err != nil || !strings.Contains(saved.BodyHTML, "Edited safe content") {
		t.Fatal("native editor save did not reach page store")
	}
	t.Log(strings.TrimSpace(string(output)))
}
