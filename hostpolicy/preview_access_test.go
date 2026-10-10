package hostpolicy

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func previewAccessFixture(t *testing.T, callback func(*http.Request, Tenant) bool, transport TransportMode, queryTargets ...map[int64][]string) (*Gate, map[string]Tenant) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("human-review-fixture"), 10)
	if err != nil {
		t.Fatal(err)
	}
	hosts := map[string]Tenant{
		"primary.test": {1, "a", "vanity"}, "www.primary.test": {1, "a", "vanity"},
		"a.preview.test": {1, "a", "preview"}, "a.alternate.test": {1, "a", "vanity"},
		"b.preview.test": {2, "b", "preview"}, "unconfigured.preview.test": {3, "unconfigured", "preview"},
	}
	var queries map[int64][]string
	if len(queryTargets) == 1 {
		queries = queryTargets[0]
	}
	gate, err := New(Config{AdminHost: "admin.test", PlatformHost: "platform.test", Transport: transport,
		Resolve: func(_ context.Context, host string) (Tenant, bool) { tenant, ok := hosts[host]; return tenant, ok },
		Policies: map[string]Policy{
			"a": {Primary: "primary.test", RedirectAliases: []string{"www.primary.test"}, PasswordHash: string(hash)},
			"b": {PasswordHash: string(hash)},
		}, PreviewAccess: callback, PreviewAccessQueryTargets: queries,
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.Host, ".preview.test") && (r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "") {
			t.Error("preview credential reached content handler")
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000")
		w.Header().Set("X-Robots-Tag", "index")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Write([]byte(r.Method + " " + r.Host + " " + r.URL.RequestURI()))
	}))
	if err != nil {
		t.Fatal(err)
	}
	return gate, hosts
}

func TestPreviewAccessRechecksTenantGrantAndKeepsPrivateBoundary(t *testing.T) {
	grants, calls := map[int64]bool{1: true, 2: true}, 0
	gate, hosts := previewAccessFixture(t, func(r *http.Request, tenant Tenant) bool {
		calls++
		return grants[tenant.ID] && tenant.Kind == "preview" && r.Host == tenant.Slug+".preview.test" &&
			r.Header.Get("Authorization") == "Bearer fixture-"+tenant.Slug && (r.URL.Path == "/" || r.URL.Path == "/assets/site.css")
	}, TransportStrict)
	for _, slug := range []string{"a", "b"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, target := range []string{"/", "/assets/site.css"} {
				r := httptest.NewRequest(method, "https://"+slug+".preview.test"+target, nil)
				r.Header.Set("Authorization", "Bearer fixture-"+slug)
				r.Header.Set("Proxy-Authorization", "Bearer fixture-proxy")
				w := httptest.NewRecorder()
				gate.ServeHTTP(w, r)
				if w.Code != http.StatusOK || w.Body.String() != method+" "+slug+".preview.test "+target {
					t.Fatalf("approved %s %s: %d %s", method, target, w.Code, w.Body.String())
				}
				if w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Robots-Tag") != "noindex, nofollow, noarchive" ||
					w.Header().Get("Strict-Transport-Security") != "max-age=86400" || w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(w.Header().Get("Vary"), "Authorization") {
					t.Fatalf("approved preview lost protected headers: %v", w.Header())
				}
			}
		}
	}
	for _, tc := range []struct{ host, token string }{{"b.preview.test", "fixture-a"}, {"a.preview.test", "fixture-b"}, {"a.preview.test", ""}} {
		r := httptest.NewRequest("GET", "https://"+tc.host+"/", nil)
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("unapproved host/grant escaped: %s %d", tc.host, w.Code)
		}
	}
	grants[1] = false
	r := httptest.NewRequest("GET", "https://a.preview.test/", nil)
	r.Header.Set("Authorization", "Bearer fixture-a")
	w := httptest.NewRecorder()
	gate.ServeHTTP(w, r)
	if w.Code != 401 || calls != 12 {
		t.Fatalf("grant revocation bypassed or callback result cached: %d, calls %d", w.Code, calls)
	}
	r = httptest.NewRequest("GET", "https://b.preview.test/", nil)
	r.Header.Set("Authorization", "Bearer fixture-b")
	w = httptest.NewRecorder()
	gate.ServeHTTP(w, r)
	if w.Code != 200 || calls != 13 {
		t.Fatal("revoking tenant A affected tenant B")
	}
	hosts["a.preview.test"] = Tenant{2, "b", "preview"}
	r = httptest.NewRequest("GET", "https://a.preview.test/", nil)
	r.Header.Set("Authorization", "Bearer fixture-a")
	w = httptest.NewRecorder()
	gate.ServeHTTP(w, r)
	if w.Code != 401 || calls != 14 {
		t.Fatal("callback used stale tenant mapping after domain move")
	}
}

func TestPreviewAccessCeilingRejectsPrivilegedAliasesAndMutations(t *testing.T) {
	calls := 0
	gate, hosts := previewAccessFixture(t, func(*http.Request, Tenant) bool { calls++; return true }, TransportStrict)
	for _, target := range []string{
		"/api", "/api/v1/admin/tenants/1/pages", "/API/v1/content", "/admin", "/administrator", "/admin/cms/sites",
		"/auth/login", "/login", "/logout", "/register", "/oauth/callback", "/__multisite/live-edit/exchange",
		"/internal/status", "/control/status", "/debug/pprof", "/healthz", "/readyz", "/livez", "/metrics",
		"/.well-known/status", "/assets/.private", "/assets/../api/v1", "/assets/./site.css", "//api/v1", "/assets//site.css",
		"/%61pi/v1", "/%2fapi/v1", "/%252fapi/v1", "/assets%2fsite.css", "/assets/site%2ecss", "/assets%5capi",
		"/content;admin", "/?token=fixture", "/?", "/?page=2",
	} {
		r := httptest.NewRequest("GET", "https://a.preview.test"+target, nil)
		r.Header.Set("Authorization", "Bearer fixture")
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Errorf("callback opened blocked target %s: %d", target, w.Code)
		}
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE", "CONNECT", "get"} {
		r := httptest.NewRequest("GET", "https://a.preview.test/", nil)
		r.Method = method
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Errorf("callback opened method %s: %d", method, w.Code)
		}
	}
	for _, alter := range []func(*http.Request){
		func(r *http.Request) { r.URL.RawPath = "/%2f" },
		func(r *http.Request) { r.URL.Path = "/assets\\site.css" },
		func(r *http.Request) { r.URL.Fragment = "control" },
		func(r *http.Request) { r.URL.RawFragment = "control" },
		func(r *http.Request) { r.URL.Opaque = "/api/v1" },
		func(r *http.Request) { r.ContentLength = 1 },
		func(r *http.Request) { r.TransferEncoding = []string{"chunked"} },
		func(r *http.Request) { r.Header.Add("Authorization", "Bearer duplicate") },
	} {
		r := httptest.NewRequest("GET", "https://a.preview.test/", nil)
		r.Header.Set("Authorization", "Bearer fixture")
		alter(r)
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Errorf("callback opened ambiguous or body-bearing request: %d", w.Code)
		}
	}
	for _, tc := range []struct {
		host string
		want int
	}{
		{"primary.test", 200}, {"www.primary.test", 308}, {"a.alternate.test", 401},
		{"admin.test", 200}, {"platform.test", 404}, {"unknown.test", 404}, {"unconfigured.preview.test", 503},
	} {
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, httptest.NewRequest("GET", "https://"+tc.host+"/", nil))
		if w.Code != tc.want {
			t.Errorf("callback changed non-preview/default boundary %s: %d", tc.host, w.Code)
		}
	}
	hosts["primary.test"] = Tenant{2, "b", "vanity"}
	w := httptest.NewRecorder()
	gate.ServeHTTP(w, httptest.NewRequest("GET", "https://a.preview.test/", nil))
	if w.Code != 503 || calls != 0 {
		t.Fatalf("callback preceded domain ownership or target gate: %d calls %d", w.Code, calls)
	}
}

func TestPreviewAccessRetainsBasicAuthenticationAndDefault(t *testing.T) {
	calls := 0
	gate, _ := previewAccessFixture(t, func(*http.Request, Tenant) bool { calls++; return true }, TransportStrict)
	for _, tc := range []struct {
		user, password string
		want           int
	}{
		{"a", "wrong", 401}, {"b", "human-review-fixture", 401}, {"a", "human-review-fixture", 200},
	} {
		for _, target := range []string{"/", "/?page=2", "/api/v1/content"} {
			r := httptest.NewRequest("GET", "https://a.preview.test"+target, nil)
			r.SetBasicAuth(tc.user, tc.password)
			w := httptest.NewRecorder()
			gate.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("Basic behavior changed %s %s: %d", tc.user, target, w.Code)
			}
		}
	}
	if calls != 0 {
		t.Fatal("preview callback intercepted Basic verification")
	}
	for _, authorization := range []string{"Basic", "Basic malformed", "basic malformed", "Basic\tmalformed"} {
		r := httptest.NewRequest("GET", "https://a.preview.test/", nil)
		r.Header.Set("Authorization", authorization)
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code != 401 || calls != 0 {
			t.Fatalf("callback intercepted malformed Basic authentication: %d", w.Code)
		}
	}
	gate, _ = previewAccessFixture(t, nil, TransportStrict)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "https://a.preview.test/", nil)
	r.Header.Set("Authorization", "Bearer fixture")
	gate.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("missing callback opened preview")
	}
}

func TestPreviewAccessRequiresExistingTransportAndImmutableTarget(t *testing.T) {
	calls := 0
	gate, _ := previewAccessFixture(t, func(r *http.Request, tenant Tenant) bool {
		calls++
		r.Method, r.Host, r.URL.Path = "POST", "admin.test", "/api/v1/admin/tenants"
		r.Header.Set("Authorization", "Bearer changed")
		return true
	}, TransportStrict)
	for _, tc := range []struct {
		name          string
		tls           bool
		proto, remote string
		want          int
	}{
		{"plain", false, "", "198.51.100.1:1234", 403},
		{"spoofed-forwarded", false, "https", "198.51.100.1:1234", 403},
		{"tls", true, "http", "198.51.100.1:1234", 200},
		{"trusted-proxy", false, "https", "192.0.2.1:1234", 200},
		{"repeated-forwarded", false, "https,http", "192.0.2.1:1234", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate.cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/28")}
			r := httptest.NewRequest("GET", "http://a.preview.test/assets/site.css", nil)
			r.RemoteAddr = tc.remote
			r.Header.Set("X-Forwarded-Proto", tc.proto)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			w := httptest.NewRecorder()
			gate.ServeHTTP(w, r)
			if w.Code != tc.want || tc.want == 200 && w.Body.String() != "GET a.preview.test /assets/site.css" || r.Method != "GET" || r.Host != "a.preview.test" || r.URL.Path != "/assets/site.css" {
				t.Fatalf("transport or target changed: %d %s", w.Code, w.Body.String())
			}
		})
	}
	if calls != 2 {
		t.Fatalf("callback preceded transport gate: calls %d", calls)
	}
	gate, _ = previewAccessFixture(t, func(*http.Request, Tenant) bool { calls++; return true }, TransportAppPlatformHTTPS)
	w := httptest.NewRecorder()
	gate.ServeHTTP(w, httptest.NewRequest("GET", "http://a.preview.test/", nil))
	if w.Code != 200 || calls != 3 {
		t.Fatal("managed HTTPS did not retain verified preview access")
	}
}

func TestPreviewAccessCannotBypassMissingOrUnsupportedPasswordHash(t *testing.T) {
	calls := 0
	gate, _ := previewAccessFixture(t, func(*http.Request, Tenant) bool { calls++; return true }, TransportStrict)
	policy := gate.cfg.Policies["a"]
	for _, hash := range []string{"", "invalid", strings.Replace(policy.PasswordHash, "$10$", "$09$", 1), strings.Replace(policy.PasswordHash, "$10$", "$15$", 1)} {
		changed := policy
		changed.PasswordHash = hash
		gate.cfg.Policies["a"] = changed
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, httptest.NewRequest("GET", "https://a.preview.test/", nil))
		if w.Code != 503 || calls != 0 {
			t.Fatalf("callback bypassed password policy: %d calls %d", w.Code, calls)
		}
	}
}

func TestPreviewAccessRejectsMalformedCostValidBcryptHashes(t *testing.T) {
	calls := 0
	gate, _ := previewAccessFixture(t, func(*http.Request, Tenant) bool { calls++; return true }, TransportStrict)
	policy := gate.cfg.Policies["a"]
	valid := policy.PasswordHash
	alphabet := "./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	noncanonical := func(value byte) byte { return alphabet[strings.IndexByte(alphabet, value)|1] }
	for _, tc := range []struct{ name, hash string }{
		{"invalid-payload-alphabet", "$2a$10$" + strings.Repeat("!", 53)},
		{"invalid-salt-alphabet", valid[:7] + strings.Repeat("!", 22) + valid[29:]},
		{"invalid-checksum-alphabet", valid[:29] + strings.Repeat("!", 31)},
		{"short-checksum", valid[:59]},
		{"long-checksum", valid + "A"},
		{"unsupported-major", "$1a$" + valid[4:]},
		{"unsupported-minor", "$2x$" + valid[4:]},
		{"wrong-version-delimiter", "$2a!" + valid[4:]},
		{"wrong-cost-delimiter", valid[:6] + "!" + valid[7:]},
		{"noncanonical-salt-bits", valid[:28] + string(noncanonical(valid[28])) + valid[29:]},
		{"noncanonical-checksum-bits", valid[:59] + string(noncanonical(valid[59]))},
		{"newline-in-salt", valid[:17] + "\n" + valid[18:]},
		{"newline-in-checksum", valid[:40] + "\n" + valid[41:]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if cost, err := bcrypt.Cost([]byte(tc.hash)); err != nil || cost != 10 {
				t.Fatalf("regression fixture must pass existing cost check: %d %v", cost, err)
			}
			changed := policy
			changed.PasswordHash = tc.hash
			gate.cfg.Policies["a"] = changed
			r := httptest.NewRequest("GET", "https://a.preview.test/", nil)
			r.Header.Set("Authorization", "Bearer fixture")
			w := httptest.NewRecorder()
			gate.ServeHTTP(w, r)
			if w.Code != 401 || calls != 0 {
				t.Fatalf("malformed cost-valid hash reached callback: %d calls %d", w.Code, calls)
			}
		})
	}
	for _, version := range []string{"$2a$", "$2b$", "$2y$"} {
		changed := policy
		changed.PasswordHash = version + valid[4:]
		gate.cfg.Policies["a"] = changed
		// The positive is a real generated cost-10 hash, not a syntactic fake.
		if err := bcrypt.CompareHashAndPassword([]byte(changed.PasswordHash), []byte("human-review-fixture")); err != nil {
			t.Fatalf("supported generated hash does not verify: %v", err)
		}
		r := httptest.NewRequest("GET", "https://a.preview.test/", nil)
		r.Header.Set("Authorization", "Bearer fixture")
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("supported generated hash denied callback: %d", w.Code)
		}
	}
	if calls != 3 {
		t.Fatalf("unexpected valid callback count: %d", calls)
	}
	// Malformed cost-valid settings retain Basic's rejection with or without a
	// callback configured; neither mode delegates a Basic request.
	callback := gate.cfg.PreviewAccess
	policy.PasswordHash = "$2a$10$" + strings.Repeat("!", 53)
	gate.cfg.Policies["a"] = policy
	for _, configured := range []func(*http.Request, Tenant) bool{callback, nil} {
		gate.cfg.PreviewAccess = configured
		r := httptest.NewRequest("GET", "https://a.preview.test/", nil)
		r.SetBasicAuth("a", "human-review-fixture")
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code != 401 || calls != 3 {
			t.Fatalf("Basic malformed-hash rejection changed: %d calls %d", w.Code, calls)
		}
	}
}
