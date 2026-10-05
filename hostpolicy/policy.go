// Package hostpolicy gates all content on resolved alternate hosts. Compose it
// outside every application wrapper; it neither provisions tenants nor grants
// CMS access. Credentials are owner supplied and stored only as bcrypt hashes.
package hostpolicy

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Tenant struct {
	ID         int64
	Slug, Kind string
}
type Policy struct {
	Primary         string   `json:"primary"`
	RedirectAliases []string `json:"redirect_aliases"`
	PasswordHash    string   `json:"-"`
}

// TransportMode describes the deployment's HTTPS boundary, never client input.
type TransportMode string

const (
	TransportStrict           TransportMode = "strict"
	TransportAppPlatformHTTPS TransportMode = "app-platform-https"
)

type Config struct {
	AdminHost, PlatformHost string
	Policies                map[string]Policy
	Resolve                 func(context.Context, string) (Tenant, bool)
	TrustedProxies          []netip.Prefix
	Transport               TransportMode
}
type success struct{ until time.Time }
type attempts struct {
	count int
	until time.Time
}
type Gate struct {
	cfg      Config
	next     http.Handler
	mu       sync.Mutex
	cache    map[[32]byte]success
	failures map[string]attempts
	kdf      chan struct{}
}

var hostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)

func New(cfg Config, next http.Handler) (*Gate, error) {
	if cfg.Resolve == nil || next == nil {
		return nil, errors.New("host policy requires resolver and handler")
	}
	switch cfg.Transport {
	case "", TransportStrict:
		cfg.Transport = TransportStrict
	case TransportAppPlatformHTTPS:
		if len(cfg.TrustedProxies) != 0 {
			return nil, errors.New("managed HTTPS cannot be combined with proxy trust")
		}
	default:
		return nil, errors.New("invalid host policy transport mode")
	}
	for _, prefix := range cfg.TrustedProxies {
		if !prefix.IsValid() || prefix.Bits() == 0 {
			return nil, errors.New("unbounded trusted proxy is forbidden")
		}
	}
	for _, name := range []string{cfg.AdminHost, cfg.PlatformHost} {
		if name == "" || normalize(name) != name {
			return nil, errors.New("exact admin and platform hosts required")
		}
	}
	if cfg.AdminHost == cfg.PlatformHost {
		return nil, errors.New("admin and platform hosts must differ")
	}
	owners := map[string]string{}
	copyPolicies := map[string]Policy{}
	for slug, p := range cfg.Policies {
		p.RedirectAliases = append([]string(nil), p.RedirectAliases...)
		names := append([]string{}, p.RedirectAliases...)
		if p.Primary != "" {
			names = append(names, p.Primary)
		} else if len(names) > 0 {
			return nil, errors.New("review-only tenant cannot have redirect aliases")
		}
		var primaryID int64
		for _, name := range names {
			if normalize(name) != name || name == "" || name == cfg.AdminHost || name == cfg.PlatformHost {
				return nil, errors.New("invalid canonical host")
			}
			if _, ok := owners[name]; ok {
				return nil, errors.New("duplicate canonical host")
			}
			owners[name] = slug
			tenant, ok := cfg.Resolve(context.Background(), name)
			if !ok || tenant.ID <= 0 || tenant.Slug != slug || tenant.Kind == "preview" {
				return nil, errors.New("canonical domain must belong to the same tenant")
			}
			if primaryID == 0 {
				primaryID = tenant.ID
			} else if primaryID != tenant.ID {
				return nil, errors.New("canonical tenant mismatch")
			}
		}
		copyPolicies[slug] = p
	}
	cfg.Policies = copyPolicies
	cfg.TrustedProxies = append([]netip.Prefix(nil), cfg.TrustedProxies...)
	return &Gate{cfg: cfg, next: next, cache: map[[32]byte]success{}, failures: map[string]attempts{}, kdf: make(chan struct{}, 2)}, nil
}

func normalize(raw string) string {
	if strings.ContainsAny(raw, "/\\@, \t\r\n") {
		return ""
	}
	host := raw
	if value, _, err := net.SplitHostPort(raw); err == nil {
		host = value
	}
	host = strings.ToLower(host)
	if !hostPattern.MatchString(host) || strings.Contains(host, "..") || strings.HasSuffix(host, ".") {
		return ""
	}
	return host
}

func (g *Gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := normalize(r.Host)
	if host == g.cfg.AdminHost {
		g.next.ServeHTTP(w, r)
		return
	}
	private := &privateWriter{ResponseWriter: w}
	if host == g.cfg.PlatformHost {
		if r.URL.Path == "/healthz" && (r.Method == "GET" || r.Method == "HEAD") {
			private.headers()
			g.next.ServeHTTP(private, r)
			if !private.wrote {
				private.headers()
			}
		} else {
			http.NotFound(private, r)
		}
		return
	}
	tenant, ok := g.cfg.Resolve(r.Context(), host)
	if host == "" || !ok || tenant.ID <= 0 {
		http.NotFound(private, r)
		return
	}
	p := g.cfg.Policies[tenant.Slug]
	// Canonical configuration is validated again against the current mapping.
	// Domain moves cannot turn a former canonical domain into a public bypass.
	if p.Primary != "" {
		canonical, ok := g.cfg.Resolve(r.Context(), p.Primary)
		if !ok || canonical.ID != tenant.ID || canonical.Slug != tenant.Slug || canonical.Kind == "preview" {
			http.Error(private, "host policy unavailable", 503)
			return
		}
		if host == p.Primary {
			if r.URL.Path == "/api/v1/ingest/release" {
				http.NotFound(private, r)
				return
			}
			g.next.ServeHTTP(w, r)
			return
		}
		private.canonical = "https://" + p.Primary + safeTarget(r)
		for _, alias := range p.RedirectAliases {
			if host == alias && tenant.Kind != "preview" {
				if r.URL.Path == "/api/v1/ingest/release" {
					http.NotFound(private, r)
					return
				}
				http.Redirect(private, r, private.canonical, http.StatusPermanentRedirect)
				return
			}
		}
	}
	private.hsts = g.secure(r)
	if r.URL.Path == "/api/v1/ingest/release" {
		http.NotFound(private, r)
		return
	}
	if !private.hsts {
		http.Error(private, "HTTPS required", http.StatusForbidden)
		return
	}
	hash := p.PasswordHash
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil || cost < 10 || cost > 14 {
		http.Error(private, "alternate host access is not configured", http.StatusServiceUnavailable)
		return
	}
	username, password, ok := r.BasicAuth()
	if !ok || username != tenant.Slug || len(password) == 0 || len(password) > 72 {
		challenge(private)
		return
	}
	key := sha256.Sum256([]byte(tenant.Slug + "\x00" + hash + "\x00" + password))
	now := time.Now()
	g.mu.Lock()
	cached := g.cache[key].until.After(now)
	limit := g.failures[tenant.Slug]
	g.mu.Unlock()
	if !cached {
		if limit.until.After(now) && limit.count >= 20 {
			private.Header().Set("Retry-After", "60")
			http.Error(private, "try again later", 429)
			return
		}
		select {
		case g.kdf <- struct{}{}:
		default:
			http.Error(private, "try again later", 503)
			return
		}
		err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
		<-g.kdf
		g.mu.Lock()
		if err != nil {
			limit = g.failures[tenant.Slug]
			if !limit.until.After(now) {
				limit = attempts{until: now.Add(time.Minute)}
			}
			limit.count++
			g.failures[tenant.Slug] = limit
		} else {
			if len(g.cache) >= 256 {
				for k, v := range g.cache {
					if !v.until.After(now) {
						delete(g.cache, k)
					}
				}
				if len(g.cache) >= 256 {
					for k := range g.cache {
						delete(g.cache, k)
						break
					}
				}
			}
			g.cache[key] = success{until: now.Add(time.Minute)}
		}
		g.mu.Unlock()
		if err != nil {
			challenge(private)
			return
		}
	}
	safe := r.Clone(r.Context())
	safe.Header = r.Header.Clone()
	safe.Header.Del("Authorization")
	safe.Header.Del("Proxy-Authorization")
	private.headers()
	g.next.ServeHTTP(private, safe)
	if !private.wrote {
		private.headers()
	}
}

func (g *Gate) secure(r *http.Request) bool {
	if g.cfg.Transport == TransportAppPlatformHTTPS {
		// App Platform upgrades every external HTTP request before forwarding it
		// to this HTTP listener. This explicit provider invariant says nothing
		// about tenant authority: the protected Host still always needs Basic.
		return true
	}
	if r.TLS != nil {
		return true
	}
	if len(r.Header.Values("X-Forwarded-Proto")) != 1 || strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))) != "https" {
		return false
	}
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(remote)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range g.cfg.TrustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
func safeTarget(r *http.Request) string {
	query := r.URL.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "nonce") || lower == "code" || lower == "authorization" {
			query.Del(key)
		}
	}
	path := r.URL.EscapedPath()
	if !strings.HasPrefix(path, "/") {
		path = "/"
	}
	target := path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	return target
}
func challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Private site review", charset="UTF-8"`)
	http.Error(w, "authentication required", 401)
}

type privateWriter struct {
	http.ResponseWriter
	canonical string
	wrote     bool
	hsts      bool
}

func (w *privateWriter) headers() {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if !strings.Contains(strings.ToLower(w.Header().Get("Vary")), "authorization") {
		w.Header().Add("Vary", "Authorization")
	}
	if w.canonical != "" {
		w.Header().Set("Link", fmt.Sprintf("<%s>; rel=\"canonical\"", w.canonical))
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if w.hsts {
		w.Header().Set("Strict-Transport-Security", "max-age=86400")
	}
}
func (w *privateWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.headers()
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *privateWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(data)
}
func (w *privateWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *privateWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(200)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// ParseTrustedProxies accepts explicit CIDRs only; never trust forwarded host.
func ParseTrustedProxies(raw string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	out := []netip.Prefix{}
	for _, item := range strings.Split(raw, ",") {
		p, err := netip.ParsePrefix(strings.TrimSpace(item))
		if err != nil || p.Bits() == 0 {
			return nil, errors.New("invalid trusted proxy CIDR")
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// CanonicalURL is intended for callers that need a safe public canonical hint.
func CanonicalURL(primary string, path *url.URL) string {
	return "https://" + primary + safeTarget(&http.Request{URL: path})
}
