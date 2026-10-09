package internal

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestHTMLUpdateClearsCanonicalBlocksAndSchedule(t *testing.T) {
	a := newAdminTestAPI()
	_, tenant := doJSON(t, a, "POST", "/api/v1/admin/tenants", map[string]any{"slug": "review"})
	base := "/api/v1/admin/tenants/" + strconv.Itoa(int(tenant["ID"].(float64))) + "/pages"
	_, p := doJSON(t, a, "POST", base, map[string]any{"path": "/", "title": "Home", "body_blocks": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "old"}}}}}, "publish_at": "2026-01-01T00:00:00Z"})
	rec, p := doJSON(t, a, "PUT", base+"/"+strconv.Itoa(int(p["ID"].(float64))), map[string]any{"expected_version": p["Version"], "body_html": "<p>Edited</p>", "body_blocks": nil, "publish_at": nil, "unpublish_at": nil})
	if rec.Code != http.StatusOK || p["BodyBlocks"] != nil || p["PublishAt"] != nil {
		t.Fatalf("stale canonical content or schedule: %d %#v", rec.Code, p)
	}
}

func TestMalformedTenantRoutesCannotAuthorizeOneTenantAndDispatchAnother(t *testing.T) {
	a := newAdminTestAPI()
	a.TenantAccess = func(r *http.Request, id int64) bool { return id == 1 }
	for _, path := range []string{
		"/api/v1/admin/tenants/1/tenants/2/pages/9",
		"/api/v1/admin/x/tenants/2/pages/preview",
		"/api/v1/admin/x/tenants/2/pages/templates",
		"/api/v1/admin/tenants/1/domains/2/domains/9",
		"/api/v1/admin/tenants/01/pages",
	} {
		r := httptest.NewRequest("PUT", path, strings.NewReader(`{"body_html":"cross tenant"}`))
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Errorf("malformed route reached dispatch: %s status %d", path, w.Code)
		}
	}
}
