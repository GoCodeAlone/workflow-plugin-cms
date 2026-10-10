package hostpolicy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreviewQueryTargetCeilingIsFiniteTenantBoundAndImmutable(t *testing.T) {
	target := "/contact?interest=voice-study"
	declarations := map[int64][]string{1: {target}}
	calls := 0
	current := true
	gate, _ := previewAccessFixture(t, func(r *http.Request, tenant Tenant) bool {
		calls++
		return current && tenant.ID == 1 && r.Host == "a.preview.test" && r.Header.Get("Authorization") == "Bearer fixture-a" && r.URL.RequestURI() == target
	}, TransportStrict, declarations)
	for _, method := range []string{"GET", "HEAD"} {
		r := httptest.NewRequest(method, "https://a.preview.test"+target, nil)
		r.Header.Set("Authorization", "Bearer fixture-a")
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != method+" a.preview.test "+target || w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
			t.Fatal("declared query lost its exact target, private response or stripped credentials")
		}
	}
	// Caller-owned declarations cannot change the constructed gate's ceiling.
	declarations[1][0] = "/contact?interest=booking"
	declarations[2] = []string{target}
	before := calls
	for _, bad := range []string{
		"/contact?interest=booking", "/contact?interest=voice-study&other=1", "/contact?interest=voice-study&interest=voice-study",
		"/contact?%69nterest=voice-study", "/contact?interest=%76oice-study", "/contact?interest=voice+study", "/contact?interest=voice-study&",
		"/contact?interest=voice-study;other=1", "/contact?", "/contact", "/%63ontact?interest=voice-study", "/admin?interest=voice-study",
	} {
		r := httptest.NewRequest("GET", "https://a.preview.test"+bad, nil)
		r.Header.Set("Authorization", "Bearer fixture-a")
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("undeclared query opened: %s", bad)
		}
	}
	// A bare allowed content path can still reach the callback, which denies it;
	// all queried variants above must be rejected before callback invocation.
	if calls != before+1 {
		t.Fatalf("query ceiling called the callback for undeclared variants: %d", calls-before)
	}
	r := httptest.NewRequest("GET", "https://b.preview.test"+target, nil)
	r.Header.Set("Authorization", "Bearer fixture-a")
	w := httptest.NewRecorder()
	gate.ServeHTTP(w, r)
	if w.Code != 401 || calls != before+1 {
		t.Fatal("query target leaked into another tenant's ceiling")
	}
	for _, alter := range []func(*http.Request){
		func(r *http.Request) { r.Method = "POST" },
		func(r *http.Request) { r.ContentLength = 1 },
		func(r *http.Request) { r.TransferEncoding = []string{"chunked"} },
		func(r *http.Request) { r.URL.Fragment = "control" },
		func(r *http.Request) { r.URL.ForceQuery = true },
		func(r *http.Request) { r.Header.Add("Authorization", "Bearer duplicate") },
	} {
		r := httptest.NewRequest("GET", "https://a.preview.test"+target, nil)
		r.Header.Set("Authorization", "Bearer fixture-a")
		alter(r)
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code != 401 || calls != before+1 {
			t.Fatal("ambiguous or mutating query request reached callback")
		}
	}
	current = false
	r = httptest.NewRequest("GET", "https://a.preview.test"+target, nil)
	r.Header.Set("Authorization", "Bearer fixture-a")
	w = httptest.NewRecorder()
	gate.ServeHTTP(w, r)
	if w.Code != 401 || calls != before+2 {
		t.Fatal("declared query bypassed fresh callback revocation")
	}
}

func TestPreviewQueryTargetDeclarationsRefuseInvalidConfiguration(t *testing.T) {
	gate, _ := previewAccessFixture(t, func(*http.Request, Tenant) bool { return true }, TransportStrict)
	for _, target := range []string{
		"/contact", "/contact?", "/contact?interest=", "/contact?interest", "/contact?interest=voice-study&", "/contact?interest=voice-study&interest=voice-study",
		"/contact?interest=%76oice-study", "/contact?%69nterest=voice-study", "/contact?interest=voice+study", "/contact?interest=voice-study#x",
		"/contact?interest=voice-study;other=1", "/contact?z=1&a=2", "/contact?interest=" + strings.Repeat("a", 129), "/admin?interest=voice-study", "/contact/?interest=voice-study",
	} {
		cfg := gate.cfg
		cfg.PreviewAccessQueryTargets = map[int64][]string{1: {target}}
		if _, err := New(cfg, gate.next); err == nil {
			t.Fatalf("unsafe query declaration accepted: %s", target)
		}
	}
	for _, declaration := range []map[int64][]string{{0: {"/contact?interest=voice-study"}}, {1: {}}, {1: {"/contact?interest=voice-study", "/contact?interest=voice-study"}}} {
		cfg := gate.cfg
		cfg.PreviewAccessQueryTargets = declaration
		if _, err := New(cfg, gate.next); err == nil {
			t.Fatal("invalid or duplicate tenant query declaration accepted")
		}
	}
	cfg := gate.cfg
	cfg.PreviewAccess = nil
	cfg.PreviewAccessQueryTargets = map[int64][]string{1: {"/contact?interest=voice-study"}}
	if _, err := New(cfg, gate.next); err == nil {
		t.Fatal("query declaration accepted without current-grant callback")
	}
}
