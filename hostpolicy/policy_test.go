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
