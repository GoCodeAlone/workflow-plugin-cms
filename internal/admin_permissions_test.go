package internal

import (
	"net/http"
	"strings"
	"testing"
)

func TestPlatformHookFailsClosedEvenWithoutOuterMiddleware(t *testing.T) {
	a := newAdminTestAPI()
	a.PlatformAccess = nil
	for _, tc := range []struct{ method, path string }{{"POST", "/api/v1/admin/tenants"}, {"POST", "/api/v1/admin/reload"}, {"GET", "/api/v1/admin/tenants/1/domains"}, {"POST", "/api/v1/admin/tenants/1/domains"}, {"DELETE", "/api/v1/admin/tenants/1/domains/1"}} {
		w, _ := doJSON(t, a, tc.method, tc.path, map[string]string{"slug": "must-not-create"})
		if w.Code != 403 {
			t.Errorf("%s %s: %d", tc.method, tc.path, w.Code)
		}
	}
	w, data := doJSON(t, a, "GET", "/api/v1/admin/tenants", nil)
	if w.Code != 200 || len(data["tenants"].([]any)) != 0 {
		t.Fatal("denied platform write created a tenant")
	}
}
func TestPermissionProjectionUsesWriteCallbacksWithoutWriting(t *testing.T) {
	a := newAdminTestAPI()
	var calls []string
	a.RequestAccess = func(r *http.Request) bool {
		calls = append(calls, r.Method+" "+r.URL.Path)
		return r.Method == "GET" || r.Method == "PUT"
	}
	a.TenantAccess = func(r *http.Request, tid int64) bool { return tid == 1 }
	w, data := doJSON(t, a, "GET", "/api/v1/admin/tenants/1/pages/permissions?page_id=9", nil)
	if w.Code != 200 || data["create"] != false || data["edit"] != true || data["delete"] != false {
		t.Fatalf("projection %d %v", w.Code, data)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "PUT /api/v1/admin/tenants/1/pages/9") {
		t.Fatal("did not probe exact page path")
	}
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("permission projection cacheable")
	}
	w, _ = doJSON(t, a, "GET", "/api/v1/admin/tenants/2/pages/permissions?page_id=9", nil)
	if w.Code != 403 {
		t.Fatal("wrong tenant projection accepted")
	}
	w, _ = doJSON(t, a, "GET", "/api/v1/admin/tenants/1/pages/permissions?page_id=09", nil)
	if w.Code != 400 {
		t.Fatal("noncanonical page accepted")
	}
	a.RequestAccess = nil
	w, _ = doJSON(t, a, "GET", "/api/v1/admin/tenants/1/pages/permissions", nil)
	if w.Code != 503 {
		t.Fatal("missing projection hook must fail closed")
	}
}
