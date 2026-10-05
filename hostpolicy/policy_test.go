package hostpolicy

import (
	"context"
	"crypto/tls"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestCompleteHostBoundary(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("fixture-only-password"), 10)
	if err != nil {
		t.Fatal(err)
	}
	hosts := map[string]Tenant{"primary.test": {1, "a", "vanity"}, "www.primary.test": {1, "a", "vanity"}, "a.preview.test": {1, "a", "preview"}, "review.test": {2, "review", "preview"}, "b.test": {3, "b", "vanity"}}
	cfg := Config{AdminHost: "admin.test", PlatformHost: "platform.test", Resolve: func(_ context.Context, h string) (Tenant, bool) { v, ok := hosts[h]; return v, ok }, Policies: map[string]Policy{"a": {Primary: "primary.test", RedirectAliases: []string{"www.primary.test"}, PasswordHash: string(hash)}, "review": {PasswordHash: string(hash)}, "b": {Primary: "b.test"}}, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/28")}}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000")
		if r.Header.Get("Authorization") != "" {
			t.Error("private credential reached app")
		}
		w.Write([]byte("content"))
	})
	gate, err := New(cfg, next)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host, path, user, password, proto, remote string
		tls                                       bool
		want                                      int
	}{
		{host: "primary.test", path: "/", want: 200},
		{host: "www.primary.test", path: "/x?token=secret&nonce=n&code=c&page=2", want: 308},
		{host: "a.preview.test", path: "/", tls: true, want: 401},
		{host: "a.preview.test", path: "/assets/site.css", tls: true, want: 401},
		{host: "a.preview.test", path: "/api/v1/admin/tenants/1/pages", tls: true, want: 401},
		{host: "a.preview.test", path: "/__multisite/live-edit/exchange", tls: true, want: 401},
		{host: "a.preview.test", path: "/healthz", tls: true, want: 401},
		{host: "a.preview.test", path: "/", user: "a", password: "wrong", tls: true, want: 401},
		{host: "a.preview.test", path: "/", user: "review", password: "fixture-only-password", tls: true, want: 401},
		{host: "a.preview.test", path: "/", user: "a", password: "fixture-only-password", tls: true, want: 200},
		{host: "review.test", path: "/", user: "a", password: "fixture-only-password", tls: true, want: 401},
		{host: "review.test", path: "/", user: "review", password: "fixture-only-password", tls: true, want: 200},
		{host: "review.test", path: "/", user: "review", password: "fixture-only-password", proto: "https", remote: "198.51.100.1:1234", want: 403},
		{host: "review.test", path: "/", user: "review", password: "fixture-only-password", proto: "https", remote: "192.0.2.2:1234", want: 200},
		{host: "review.test", path: "/", proto: "https,http", remote: "192.0.2.2:1234", want: 403},
		{host: "review.test", path: "/", want: 403},
		{host: "unknown.test", path: "/", tls: true, want: 404},
		{host: "platform.test", path: "/", tls: true, want: 404},
		{host: "platform.test", path: "/healthz", want: 200},
		{host: "primary.test", path: "/api/v1/ingest/release", want: 404},
		{host: "admin.test", path: "/api/v1/ingest/release", want: 200},
	} {
		r := httptest.NewRequest("GET", "http://"+tc.host+tc.path, nil)
		if tc.tls {
			r.TLS = &tls.ConnectionState{}
		}
		r.RemoteAddr = tc.remote
		r.Header.Set("X-Forwarded-Proto", tc.proto)
		r.Header.Set("X-Forwarded-Host", "primary.test")
		if tc.user != "" {
			r.SetBasicAuth(tc.user, tc.password)
		}
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s%s got %d want %d: %s", tc.host, tc.path, w.Code, tc.want, w.Body.String())
		}
		if tc.host != "admin.test" && !(tc.host == "primary.test" && tc.path == "/") {
			if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || !strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") || !strings.Contains(w.Header().Get("Vary"), "Authorization") {
				t.Errorf("private cache/robots headers lost on %s", tc.host)
			}
		}
		if tc.want == 308 && w.Header().Get("Location") != "https://primary.test/x?page=2" {
			t.Errorf("unsafe canonical redirect: %s", w.Header().Get("Location"))
		}
		if tc.host == "primary.test" && tc.path == "/" && (w.Header().Get("X-Robots-Tag") != "" || strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get("Vary") != "") {
			t.Error("public primary acquired private policy headers")
		}
	}
	hosts["primary.test"] = Tenant{3, "b", "vanity"}
	w := httptest.NewRecorder()
	gate.ServeHTTP(w, httptest.NewRequest("GET", "https://www.primary.test/", nil))
	if w.Code != 503 {
		t.Fatal("domain reassignment remained public")
	}
}

func TestHostPolicyRejectsCrossTenantCanonicalAndUnboundedProxy(t *testing.T) {
	cfg := Config{AdminHost: "admin.test", PlatformHost: "platform.test", Resolve: func(_ context.Context, h string) (Tenant, bool) { return Tenant{2, "other", "vanity"}, true }, Policies: map[string]Policy{"a": {Primary: "other.test"}}}
	if _, err := New(cfg, http.NotFoundHandler()); err == nil {
		t.Fatal("cross-tenant primary accepted")
	}
	cfg.Policies = nil
	cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}
	if _, err := New(cfg, http.NotFoundHandler()); err == nil {
		t.Fatal("unbounded proxy accepted")
	}
}

func TestMissingHashFailsClosedAndFailureChecksAreBounded(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("fixture"), 10)
	cfg := Config{AdminHost: "admin.test", PlatformHost: "platform.test", Resolve: func(_ context.Context, h string) (Tenant, bool) { return Tenant{1, "a", "preview"}, true }, Policies: map[string]Policy{"a": {PasswordHash: string(hash)}}}
	gate, err := New(cfg, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 21; i++ {
		r := httptest.NewRequest("GET", "https://a.preview.test/", nil)
		r.SetBasicAuth("a", "wrong")
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		want := 401
		if i == 20 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("attempt %d = %d", i, w.Code)
		}
	}
	cfg.Policies = map[string]Policy{"a": {PasswordHash: "invalid"}}
	gate, _ = New(cfg, http.NotFoundHandler())
	w := httptest.NewRecorder()
	gate.ServeHTTP(w, httptest.NewRequest("GET", "https://a.preview.test/", nil))
	if w.Code != 503 {
		t.Fatal("malformed hash did not fail closed")
	}
}

func TestAppPlatformModeKeepsAuthenticationIndependentOfForwardingHeaders(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("review-fixture-only"), 10)
	if err != nil {
		t.Fatal(err)
	}
	hosts := map[string]Tenant{
		"primary.test": {1, "live", "vanity"}, "www.primary.test": {1, "live", "vanity"},
		"review.test": {2, "review", "preview"}, "unconfigured.test": {3, "unconfigured", "preview"},
	}
	called := 0
	gate, err := New(Config{AdminHost: "admin.test", PlatformHost: "platform.test", Transport: TransportAppPlatformHTTPS,
		Resolve:  func(_ context.Context, h string) (Tenant, bool) { v, ok := hosts[h]; return v, ok },
		Policies: map[string]Policy{"live": {Primary: "primary.test", RedirectAliases: []string{"www.primary.test"}}, "review": {PasswordHash: string(hash)}},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("sensitive credential reached inner app")
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000")
		w.Header().Set("X-Robots-Tag", "index")
		if normalize(r.Host) == "review.test" {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			w.Write([]byte("review-only-content"))
		} else {
			w.Write([]byte("public-or-health-content"))
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/assets/portrait.jpg", "/media/recording", "/api/v1/admin/tenants/2/pages", "/__multisite/live-edit/exchange", "/healthz"} {
		for _, proto := range []string{"", "http", "https", "https,http", "invalid"} {
			// Successful authentication first populates the verifier cache. An anonymous
			// request for the identical URL must never reach the app afterwards.
			for _, authorized := range []bool{true, false} {
				r := httptest.NewRequest("GET", "http://REVIEW.test:443"+path, nil)
				r.RemoteAddr = "198.51.100.99:3333"
				r.Header.Set("X-Forwarded-Proto", proto)
				r.Header.Add("X-Forwarded-Proto", "client-second-value")
				r.Header.Set("X-Forwarded-Host", "primary.test")
				r.Header.Set("Forwarded", "host=primary.test;proto=https")
				r.Header.Set("Proxy-Authorization", "Basic fixture-proxy-credential")
				if authorized {
					r.SetBasicAuth("review", "review-fixture-only")
				}
				before := called
				w := httptest.NewRecorder()
				gate.ServeHTTP(w, r)
				if authorized {
					if w.Code != 200 || w.Body.String() != "review-only-content" || called != before+1 {
						t.Fatalf("authenticated %s proto=%q: %d %s", path, proto, w.Code, w.Body.String())
					}
				} else if w.Code != 401 || called != before || strings.Contains(w.Body.String(), "review-only-content") {
					t.Fatalf("anonymous after auth %s escaped: %d", path, w.Code)
				}
				if w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Robots-Tag") != "noindex, nofollow, noarchive" || w.Header().Get("Strict-Transport-Security") != "max-age=86400" || !strings.Contains(w.Header().Get("Vary"), "Authorization") {
					t.Fatalf("protected headers lost: %#v", w.Header())
				}
			}
		}
	}
	for _, tc := range []struct {
		host, path, user, password string
		want                       int
		body                       string
	}{
		{"review.test", "/", "live", "review-fixture-only", 401, ""},
		{"review.test", "/", "review", "wrong", 401, ""},
		{"unconfigured.test", "/", "review", "review-fixture-only", 503, ""},
		{"unknown.test", "/", "review", "review-fixture-only", 404, ""},
		{"platform.test", "/", "review", "review-fixture-only", 404, ""},
		{"platform.test", "/healthz", "", "", 200, "public-or-health-content"},
		{"primary.test", "/assets/review-secret", "", "", 200, "public-or-health-content"},
		{"www.primary.test", "/", "", "", 308, ""},
	} {
		r := httptest.NewRequest("GET", "http://"+tc.host+tc.path, nil)
		r.Header.Set("X-Forwarded-Host", "review.test")
		r.Header.Set("Forwarded", "host=review.test;proto=https")
		r.Header.Set("X-Forwarded-Proto", "https")
		if tc.user != "" {
			r.SetBasicAuth(tc.user, tc.password)
		}
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code != tc.want || tc.body != "" && w.Body.String() != tc.body || tc.want != 200 && strings.Contains(w.Body.String(), "review-only-content") {
			t.Errorf("Host %s forwarding selected authority: %d %s", tc.host, w.Code, w.Body.String())
		}
		if tc.host == "primary.test" || tc.host == "www.primary.test" || tc.host == "platform.test" {
			if w.Header().Get("Strict-Transport-Security") != "" {
				t.Error("HSTS leaked outside protected host")
			}
		}
	}
}

func TestTransportConfigurationFailsClosed(t *testing.T) {
	cfg := Config{AdminHost: "admin.test", PlatformHost: "platform.test", Resolve: func(context.Context, string) (Tenant, bool) { return Tenant{1, "review", "preview"}, true }}
	for _, mode := range []TransportMode{"auto", "app-platform", "APP-PLATFORM-HTTPS"} {
		cfg.Transport = mode
		if _, err := New(cfg, http.NotFoundHandler()); err == nil {
			t.Fatalf("invalid mode %q accepted", mode)
		}
	}
	cfg.Transport = TransportAppPlatformHTTPS
	cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("192.0.2.1/32")}
	if _, err := New(cfg, http.NotFoundHandler()); err == nil {
		t.Fatal("managed mode combined with forwarding trust")
	}
	cfg.Transport = ""
	cfg.TrustedProxies = nil
	gate, err := New(cfg, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://review.test/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	gate.ServeHTTP(w, r)
	if w.Code != 403 || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("default strict mode accepted client TLS assertion or challenged on HTTP")
	}
	cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("192.0.2.1/32")}
	gate, err = New(cfg, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	r.RemoteAddr = "192.0.2.1:1234"
	r.Header.Add("X-Forwarded-Proto", "http")
	w = httptest.NewRecorder()
	gate.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("strict mode accepted repeated forwarding values")
	}
}
