package adminui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSurfacesAreSeparateAndDeepLinksLoadAbsoluteAssets(t *testing.T) {
	editor := HandlerWithOptions(Options{Mode: Editor, BasePath: "/admin/cms/sites"})
	platform := HandlerWithOptions(Options{Mode: Platform, BasePath: "/admin/cms/platform"})
	for _, tc := range []struct {
		h                      http.Handler
		path, include, exclude string
	}{{editor, "/tenants/2/pages/4/content/2", "Content sections", "new-tenant-form"}, {platform, "/tenants/2/domains", "new-domain-form", "data-rich-editor"}} {
		w := httptest.NewRecorder()
		tc.h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), tc.include) || strings.Contains(w.Body.String(), tc.exclude) {
			t.Fatalf("wrong surface %s: %d", tc.path, w.Code)
		}
		if strings.Contains(w.Body.String(), `src="admin.js"`) {
			t.Fatal("deep route has relative asset")
		}
	}
	for _, path := range []string{"/tenants/02/pages", "/tenants/2/pages/4/content/0", "/tenants/2/domains", "/tenants/2/pages/4/preview/2", "/tenants/2/pages/4/content/2/escape"} {
		w := httptest.NewRecorder()
		editor.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Errorf("invalid route %s: %d", path, w.Code)
		}
	}
}
