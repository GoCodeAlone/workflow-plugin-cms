package host

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

func TestServerSavedHistoryActorWithoutOptionalAuditLogger(t *testing.T) {
	s := New(Config{
		Pages:             store.NewMemoryPageStore(),
		AdminAuth:         func(*http.Request) bool { return true },
		AdminTenantAccess: func(_ *http.Request, tid int64) bool { return tid == 234 },
		AuditActor:        func(*http.Request) string { return "user:verified-owner" },
	})
	if s.Audit() != nil {
		t.Fatal("fixture unexpectedly enabled signed logger")
	}
	base := "/api/v1/admin/tenants/234/pages"
	r := httptest.NewRequest(http.MethodPost, base, strings.NewReader("{\"path\":\"/saved\",\"title\":\"Saved\",\"status\":\"draft\"}"))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("actual host create: %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"/history", nil))
	var h store.PageHistory
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &h) != nil || len(h.Entries) != 1 || h.Entries[0].Actor != "user:verified-owner" {
		t.Fatal("host lost verified history actor without separate signed logger")
	}
}
