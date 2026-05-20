package internal

import (
	"context"
	"net/http"
	"strings"
	"sync"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// tenantResolverModule resolves the tenant_id from the request Host header
// before any other middleware runs.
//
// Per SPEC.md (gocodealone-multisite):
//   V1: ∀ HTTP req → resolve tenant by Host header before any other routing.
//   V2: tenant ∉ db → 404 (no fall-through).
//   V16: unknown domain → 404 neutral; no info leak.
//   V22: subsite resolution — Host matches a subsite's domains[] → load
//        with subsite discriminator.
//
// Implementation provides an http.Handler middleware that:
//   1. Strips port from Host.
//   2. Looks up domain → (tenant_id, subsite?) in the TenantStore.
//   3. Falls back to preview-subdomain pattern: <slug>.<preview_base>.
//   4. On miss, writes a neutral 404 and short-circuits.
//   5. On hit, stores tenant + subsite in request context for downstream
//      middleware (static-serve, cms.engine).
//
// The TenantStore is an interface — production wires it to the database;
// tests inject an in-memory implementation. Stub default returns no
// tenants (every request 404s) so the host wires a real store at boot.
type tenantResolverModule struct {
	name                 string
	previewSubdomainBase string
	onUnknownDomain      string

	store TenantStore
	mu    sync.RWMutex // guards store swap
}

// TenantInfo is the resolved tenant + optional subsite for a request.
type TenantInfo struct {
	TenantID     int64
	TenantSlug   string
	SubsiteLabel string // empty if no subsite scope
	Kind         string // "vanity" | "preview" | "subdomain"
}

// TenantStore looks up tenants by domain.
// Host wires the production postgres-backed implementation at boot.
type TenantStore interface {
	// Lookup returns the tenant for an exact domain match. ok=false on miss.
	Lookup(ctx context.Context, host string) (TenantInfo, bool)
	// LookupBySlug resolves a preview subdomain pattern (<slug>.<base>).
	LookupBySlug(ctx context.Context, slug string) (TenantInfo, bool)
}

// emptyStore is the default; every lookup misses. Used until the host
// installs the real store via SetStore.
type emptyStore struct{}

func (emptyStore) Lookup(context.Context, string) (TenantInfo, bool)       { return TenantInfo{}, false }
func (emptyStore) LookupBySlug(context.Context, string) (TenantInfo, bool) { return TenantInfo{}, false }

func newTenantResolverModule(name string, config map[string]any) (sdk.ModuleInstance, error) {
	m := &tenantResolverModule{
		name:            name,
		onUnknownDomain: "404_neutral",
		store:           emptyStore{},
	}
	if v, ok := config["preview_subdomain_base"].(string); ok {
		m.previewSubdomainBase = v
	}
	if v, ok := config["on_unknown_domain"].(string); ok {
		m.onUnknownDomain = v
	}
	return m, nil
}

func (m *tenantResolverModule) Name() string                    { return m.name }
func (m *tenantResolverModule) Init() error                     { return nil }
func (m *tenantResolverModule) Start(ctx context.Context) error { return nil }
func (m *tenantResolverModule) Stop(ctx context.Context) error  { return nil }

// SetStore installs the tenant lookup backend. Host calls this at boot.
// Safe for concurrent callers.
func (m *tenantResolverModule) SetStore(s TenantStore) {
	if s == nil {
		s = emptyStore{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = s
}

// Middleware returns the http.Handler middleware that wraps downstream
// handlers with tenant resolution.
func (m *tenantResolverModule) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := stripPort(r.Host)
		if host == "" {
			m.unknownHost(w)
			return
		}

		m.mu.RLock()
		store := m.store
		m.mu.RUnlock()

		// 1. Exact domain match.
		if info, ok := store.Lookup(r.Context(), host); ok {
			r = r.WithContext(WithTenant(r.Context(), info))
			next.ServeHTTP(w, r)
			return
		}

		// 2. Preview-subdomain fallback: <slug>.<preview_base>.
		if slug, ok := matchPreviewSubdomain(host, m.previewSubdomainBase); ok {
			if info, ok := store.LookupBySlug(r.Context(), slug); ok {
				info.Kind = "preview"
				r = r.WithContext(WithTenant(r.Context(), info))
				next.ServeHTTP(w, r)
				return
			}
		}

		// 3. Unknown — 404 neutral (V16).
		m.unknownHost(w)
	})
}

func (m *tenantResolverModule) unknownHost(w http.ResponseWriter) {
	// Neutral 404 — ⊥ leak tenant list, version, or internals (V16).
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("Not Found"))
}

// stripPort returns the host portion of a "host:port" string. Returns
// the input unchanged if no port present.
func stripPort(hostport string) string {
	if i := strings.LastIndexByte(hostport, ':'); i >= 0 {
		// IPv6 brackets — leave alone unless port follows ']'
		if strings.HasPrefix(hostport, "[") {
			if j := strings.IndexByte(hostport, ']'); j >= 0 && i > j {
				return hostport[:i]
			}
			return hostport
		}
		return hostport[:i]
	}
	return hostport
}

// matchPreviewSubdomain extracts <slug> from "<slug>.<base>". Returns
// ("", false) if host does not match the pattern or base is empty.
func matchPreviewSubdomain(host, base string) (string, bool) {
	if base == "" || host == base {
		return "", false
	}
	suffix := "." + base
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(host, suffix)
	if slug == "" || strings.ContainsRune(slug, '.') {
		// Reject empty slug AND deeper sub-sub-domains
		// (V22: subsites are explicit, not implicit through nesting).
		return "", false
	}
	return slug, true
}

// --- Request context plumbing -------------------------------------------

type tenantCtxKey struct{}

// WithTenant returns a context carrying the resolved tenant.
func WithTenant(ctx context.Context, info TenantInfo) context.Context {
	return context.WithValue(ctx, tenantCtxKey{}, info)
}

// TenantFromContext returns the tenant resolved upstream, or ok=false if
// none was set (request would not have reached this point through the
// normal flow).
func TenantFromContext(ctx context.Context) (TenantInfo, bool) {
	v, ok := ctx.Value(tenantCtxKey{}).(TenantInfo)
	return v, ok
}
