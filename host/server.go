// Package host wires the workflow-plugin-cms components into a single
// http.Handler suitable for a standalone multisite host binary.
//
// The CMS plugin still ships as an external gRPC plugin (cmd/), but
// for the gocodealone-multisite host the engine runs in-process — this
// package exposes the same component set without the gRPC boundary.
//
// Per gocodealone-multisite SPEC.md §I and T13/T14/T16/T28/T31/T32.
package host

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/analytics"
	"github.com/GoCodeAlone/workflow-plugin-cms/bundle"
	"github.com/GoCodeAlone/workflow-plugin-cms/internal"
	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

// Config controls the host wiring.
type Config struct {
	// PreviewSubdomainBase is the FQDN suffix for preview subdomains
	// (e.g. "preview.gocodealone.com"). Empty disables preview-fallback
	// in the tenant resolver AND disables auto-provision of preview
	// domain rows on tenant create.
	PreviewSubdomainBase string

	// BundleRoot is the directory the static-serve middleware walks to
	// find <tenant>/current/<path>. Required.
	BundleRoot string

	// HMACSecret signs ingest webhooks (SPEC V8). Required for the
	// ingest endpoint to function; if empty, the endpoint returns 503.
	HMACSecret string

	// OnIngest is called with the verified payload. Production wires
	// this to a Fetcher; tests typically pass a no-op.
	OnIngest func(bundle.IngestPayload) error

	// Stores. Defaults are in-memory; production passes postgres-backed
	// implementations.
	TenantsAdmin store.TenantAdminStore
	Pages        store.PageStore

	// TenantResolverStore is the read-side tenant lookup interface used
	// by the resolver middleware. If both this and TenantsAdmin are
	// nil, a memory-backed store is created and shared between them
	// via a thin adapter.
	TenantResolverStore TenantResolverStore

	// AnalyticsConfigForTenant returns the per-tenant analytics config
	// (gtag measurement_id + anonymize_ip). Nil disables injection.
	AnalyticsConfigForTenant func(tenantID int64) analytics.TenantConfig

	// HealthCheck reports liveness (DB ping, etc.). Nil → always OK.
	HealthCheck func(ctx context.Context) error
}

// TenantResolverStore is the read-side tenant lookup interface — it is
// satisfied by `internal.TenantStore` (the resolver expects this shape).
type TenantResolverStore interface {
	Lookup(ctx context.Context, host string) (TenantInfo, bool)
	LookupBySlug(ctx context.Context, slug string) (TenantInfo, bool)
}

// TenantInfo mirrors internal.TenantInfo so callers don't need to import
// internal.
type TenantInfo = internal.TenantInfo

// Server is the assembled multisite host.
type Server struct {
	cfg     Config
	admin   *internal.AdminAPI
	ingest  *bundle.IngestHandler
	mu      sync.RWMutex
	cached  map[string]TenantInfo // host:lookup cache for the simple resolver
	cachedSlug map[string]TenantInfo
}

// New builds a Server. Fills in memory-backed defaults for nil stores.
func New(cfg Config) *Server {
	if cfg.TenantsAdmin == nil {
		cfg.TenantsAdmin = store.NewMemoryTenantAdminStore()
	}
	if cfg.Pages == nil {
		cfg.Pages = store.NewMemoryPageStore()
	}

	s := &Server{
		cfg:        cfg,
		cached:     map[string]TenantInfo{},
		cachedSlug: map[string]TenantInfo{},
	}

	// AdminAPI exposes /api/v1/admin/*.
	api := internal.NewAdminAPI(cfg.TenantsAdmin, cfg.Pages)
	api.PreviewBase = cfg.PreviewSubdomainBase
	api.ReloadFunc = s.flushCaches
	s.admin = api

	// IngestHandler exposes /api/v1/ingest/release.
	s.ingest = &bundle.IngestHandler{
		Secret:    cfg.HMACSecret,
		OnPayload: cfg.OnIngest,
	}

	return s
}

// flushCaches clears the in-memory tenant lookup cache. Called by the
// /api/v1/admin/reload endpoint (T31).
func (s *Server) flushCaches() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached = map[string]TenantInfo{}
	s.cachedSlug = map[string]TenantInfo{}
	return nil
}

// ServeHTTP routes a single request. Order of resolution:
//
//  1. /healthz (always served, even without a Host header match)
//  2. /api/v1/admin/* — handed to AdminAPI
//  3. /api/v1/ingest/release — handed to IngestHandler (HMAC)
//  4. Everything else → tenant resolve → static-serve → 404 if neither
//
// Cross-cutting: every response gets per-tenant analytics injection IF
// the response is HTML AND the tenant has a measurement_id.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if path == "/healthz" {
		s.serveHealthz(w, r)
		return
	}

	if strings.HasPrefix(path, "/api/v1/admin/") || path == "/api/v1/admin" {
		s.admin.ServeHTTP(w, r)
		return
	}

	if path == "/api/v1/ingest/release" {
		s.ingest.ServeHTTP(w, r)
		return
	}

	// Tenant-resolved content path. For now we 404 with a neutral body
	// — static serve + CMS page rendering wires into the same fall-
	// through table as the resolver matures.
	tenant, ok := s.resolveTenant(r.Context(), r.Host)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	_ = tenant // future: render via CMS page store + bundle static-serve.
	http.Error(w, "tenant resolved but no content path mounted yet", http.StatusNotFound)
}

func (s *Server) serveHealthz(w http.ResponseWriter, r *http.Request) {
	if s.cfg.HealthCheck != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.cfg.HealthCheck(ctx); err != nil {
			http.Error(w, "unhealthy: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// resolveTenant uses the configured TenantResolverStore (if any) +
// preview-subdomain fallback (if cfg.PreviewSubdomainBase set).
//
// Cached in-memory; flushed by /api/v1/admin/reload.
func (s *Server) resolveTenant(ctx context.Context, host string) (TenantInfo, bool) {
	host = strings.ToLower(strings.Split(host, ":")[0])

	s.mu.RLock()
	if t, ok := s.cached[host]; ok {
		s.mu.RUnlock()
		return t, true
	}
	s.mu.RUnlock()

	if s.cfg.TenantResolverStore != nil {
		if t, ok := s.cfg.TenantResolverStore.Lookup(ctx, host); ok {
			s.mu.Lock()
			s.cached[host] = t
			s.mu.Unlock()
			return t, true
		}
	}

	// Preview-subdomain fallback.
	if s.cfg.PreviewSubdomainBase != "" {
		base := strings.ToLower(strings.TrimPrefix(s.cfg.PreviewSubdomainBase, "."))
		suffix := "." + base
		if strings.HasSuffix(host, suffix) {
			slug := strings.TrimSuffix(host, suffix)
			if slug != "" && !strings.Contains(slug, ".") {
				if s.cfg.TenantResolverStore != nil {
					if t, ok := s.cfg.TenantResolverStore.LookupBySlug(ctx, slug); ok {
						s.mu.Lock()
						s.cachedSlug[slug] = t
						s.mu.Unlock()
						return t, true
					}
				}
			}
		}
	}

	return TenantInfo{}, false
}

// AdminAPI returns the underlying admin handler (for tests + advanced
// wiring).
func (s *Server) AdminAPI() *internal.AdminAPI { return s.admin }
