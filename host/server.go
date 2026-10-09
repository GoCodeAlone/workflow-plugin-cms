// Package host wires the workflow-plugin-cms components into a single
// http.Handler suitable for a standalone CMS host binary.
//
// The CMS plugin still ships as an external gRPC plugin (cmd/), but
// for standalone host deployments the engine runs in-process — this
// package exposes the same component set without the gRPC boundary.
//
// Per CMS host SPEC.md §I and T13/T14/T16/T28/T31/T32.
package host

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/adminui"
	"github.com/GoCodeAlone/workflow-plugin-cms/analytics"
	"github.com/GoCodeAlone/workflow-plugin-cms/audit"
	"github.com/GoCodeAlone/workflow-plugin-cms/bundle"
	"github.com/GoCodeAlone/workflow-plugin-cms/internal"
	"github.com/GoCodeAlone/workflow-plugin-cms/media"
	"github.com/GoCodeAlone/workflow-plugin-cms/store"
	"github.com/GoCodeAlone/workflow/telemetry"
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

	// AdminHost, when set, moves the admin UI to https://<AdminHost>/
	// and requires all admin API/media/UI requests to use that host.
	// This avoids exposing admin under public tenant paths such as
	// /admin on the marketing site.
	AdminHost string

	// AdminAuth gates admin UI/API/media requests. Production should
	// wire this to workflow-plugin-auth session/JWT validation. When
	// AdminHost is set and AdminAuth is nil, admin requests fail closed.
	AdminAuth func(*http.Request) bool

	// AdminTenantAccess adds membership enforcement after role authorization.
	// Hosts with tenant editors must supply it; nil preserves legacy operator-only wiring.
	AdminTenantAccess func(*http.Request, int64) bool
	// AdminPlatformAccess is mandatory for tenant creation, domain reads/writes
	// and global cache reload. Hosts must enforce durable platform authority.
	AdminPlatformAccess func(*http.Request) bool

	// OnIngest is called with the verified payload. Production wires
	// this to a Fetcher; tests typically pass a no-op.
	OnIngest func(bundle.IngestPayload) error

	// Stores. Defaults are in-memory; production passes postgres-backed
	// implementations.
	TenantsAdmin store.TenantAdminStore
	Pages        store.PageStore

	// PageTemplates maps CMS template IDs to HTML shells containing
	// <!--cms:body-->. Missing/empty template IDs render the page body only.
	PageTemplates map[string]string

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

	// MediaBackend persists tenant-scoped uploads. Nil disables the
	// upload endpoint (returns 503).
	MediaBackend media.Backend

	// AuditSignKey signs audit-chain entries. Empty disables audit
	// recording. Audit entries are emitted for tenant + page mutations.
	AuditSignKey string

	// AuditSink persists audit-chain entries. Nil uses an in-memory sink.
	// Ignored when AuditSignKey is empty.
	AuditSink audit.Sink

	// AuditActor returns the actor recorded for admin audit entries.
	// Empty return values fall back to "admin".
	AuditActor func(*http.Request) string
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
	cfg       Config
	admin     *internal.AdminAPI
	ingest    *bundle.IngestHandler
	media     *media.UploadHandler
	metrics   *requestCounters
	audit     *audit.Logger
	adminUI   http.Handler
	adminRoot http.Handler
}

// New builds a Server. Fills in memory-backed defaults for nil stores.
func New(cfg Config) *Server {
	if cfg.TenantsAdmin == nil {
		cfg.TenantsAdmin = store.NewMemoryTenantAdminStore()
	}
	if cfg.TenantResolverStore == nil {
		cfg.TenantResolverStore = tenantAdminResolver{admin: cfg.TenantsAdmin}
	}
	if cfg.Pages == nil {
		cfg.Pages = store.NewMemoryPageStore()
	}

	s := &Server{cfg: cfg}

	// AdminAPI exposes /api/v1/admin/*.
	api := internal.NewAdminAPI(cfg.TenantsAdmin, cfg.Pages)
	api.PreviewBase = cfg.PreviewSubdomainBase
	api.ReloadFunc = s.flushCaches
	api.TenantAccess = cfg.AdminTenantAccess
	api.PlatformAccess = cfg.AdminPlatformAccess
	api.RequestAccess = cfg.AdminAuth
	api.ResolveTemplate = s.resolveTenantTemplate
	api.ListTemplates = s.listTenantTemplates
	s.admin = api

	// IngestHandler exposes /api/v1/ingest/release.
	s.ingest = &bundle.IngestHandler{
		Secret:    cfg.HMACSecret,
		OnPayload: cfg.OnIngest,
	}

	// Optional surfaces.
	if cfg.MediaBackend != nil {
		s.media = &media.UploadHandler{Backend: cfg.MediaBackend}
	}
	s.metrics = newRequestCounters()
	if cfg.AuditSignKey != "" {
		s.audit = audit.New(cfg.AuditSignKey, cfg.AuditSink)
		s.admin.Audit = s.audit
		s.admin.AuditActor = cfg.AuditActor
	}
	s.adminRoot = adminui.Handler()
	s.adminUI = http.StripPrefix("/admin", s.adminRoot)

	return s
}

// Audit returns the audit Logger if configured. May be nil.
func (s *Server) Audit() *audit.Logger { return s.audit }

func (s *Server) EmitMetrics(_ context.Context, recorder telemetry.MetricRecorder) error {
	if s.metrics != nil {
		s.metrics.emit(recorder)
	}
	return nil
}

// flushCaches preserves the reload API. Tenant routing now reads authoritative
// store state on every request, so there is no host mapping cache to invalidate.
func (s *Server) flushCaches() error {
	return nil
}

// ServeHTTP routes a single request. Order of resolution:
//
//  1. /healthz (always served, even without a Host header match)
//  2. /api/v1/admin/* — handed to AdminAPI (+ media upload subroute)
//  3. /api/v1/ingest/release — handed to IngestHandler (HMAC)
//  4. Everything else → tenant resolve → static-serve → 404 if neither
//
// Every request increments the per-tenant request counter (V30) keyed
// on the resolved tenant slug (or "_unresolved" for admin/system
// routes).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	tenantSlug := ""

	defer func() {
		if s.metrics != nil {
			label := tenantSlug
			if label == "" {
				label = "_unresolved"
			}
			s.metrics.inc(label, rec.status)
		}
	}()

	path := r.URL.Path

	if path == "/healthz" {
		s.serveHealthz(rec, r)
		return
	}

	adminHost := s.isAdminHost(r.Host)
	if s.cfg.AdminHost != "" && strings.HasPrefix(path, "/admin") && !adminHost {
		http.NotFound(rec, r)
		return
	}

	// Media upload route: POST /api/v1/admin/tenants/:id/upload.
	if strings.HasPrefix(path, "/api/v1/admin/tenants/") && strings.HasSuffix(path, "/upload") {
		tid := extractTenantIDFromPath(path)
		if tid <= 0 || path != "/api/v1/admin/tenants/"+strconv.FormatInt(tid, 10)+"/upload" {
			http.NotFound(rec, r)
			return
		}
		if !s.authorizeAdmin(rec, r, adminHost) {
			return
		}
		if s.cfg.AdminTenantAccess != nil && !s.cfg.AdminTenantAccess(r, extractTenantIDFromPath(path)) {
			http.Error(rec, "forbidden", http.StatusForbidden)
			return
		}
		if s.media == nil {
			http.Error(rec, "media backend not configured", http.StatusServiceUnavailable)
			return
		}
		s.media.ServeForTenant(rec, r, tid)
		return
	}

	if strings.HasPrefix(path, "/api/v1/admin/") || path == "/api/v1/admin" {
		if !s.authorizeAdmin(rec, r, adminHost) {
			return
		}
		if strings.Contains(path, "/pages/assets/") {
			s.servePreviewAsset(rec, r)
			return
		}
		s.admin.ServeHTTP(rec, r)
		return
	}

	if path == "/api/v1/ingest/release" {
		s.ingest.ServeHTTP(rec, r)
		return
	}

	if adminHost {
		if !s.authorizeAdmin(rec, r, true) {
			return
		}
		if path == "/admin" || path == "/admin/" {
			http.Redirect(rec, r, "/", http.StatusMovedPermanently)
			return
		}
		s.adminRoot.ServeHTTP(rec, r)
		return
	}

	// Legacy path mount for hosts that have not moved admin to a
	// dedicated hostname yet. If AdminHost is set this branch is
	// unreachable because public /admin paths are rejected above.
	if path == "/admin" {
		http.Redirect(rec, r, "/admin/", http.StatusMovedPermanently)
		return
	}
	if strings.HasPrefix(path, "/admin/") {
		if !s.authorizeAdmin(rec, r, adminHost) {
			return
		}
		s.adminUI.ServeHTTP(rec, r)
		return
	}

	tenant, ok := s.resolveTenant(r.Context(), r.Host)
	if !ok {
		http.Error(rec, "not found", http.StatusNotFound)
		return
	}
	tenantSlug = tenant.TenantSlug
	if s.serveStaticExactBundle(rec, r, tenant) {
		return
	}
	if s.serveCMSPage(rec, r, tenant) {
		return
	}
	if s.serveSPAFallbackBundle(rec, r, tenant) {
		return
	}
	http.Error(rec, "tenant resolved but no content path mounted yet", http.StatusNotFound)
}

func (s *Server) isAdminHost(host string) bool {
	if s.cfg.AdminHost == "" {
		return false
	}
	got := strings.ToLower(strings.TrimSpace(strings.Split(host, ":")[0]))
	want := strings.ToLower(strings.TrimSpace(s.cfg.AdminHost))
	return got == want
}

func (s *Server) authorizeAdmin(w http.ResponseWriter, r *http.Request, adminHost bool) bool {
	if s.cfg.AdminHost != "" && !adminHost {
		http.NotFound(w, r)
		return false
	}
	if s.cfg.AdminHost != "" && s.cfg.AdminAuth == nil {
		http.Error(w, "admin auth is not configured", http.StatusServiceUnavailable)
		return false
	}
	if s.cfg.AdminAuth != nil && !s.cfg.AdminAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *Server) serveStaticExactBundle(w http.ResponseWriter, r *http.Request, tenant TenantInfo) bool {
	filePath, ok := resolveBundlePath(s.cfg.BundleRoot, tenant.TenantSlug, r.URL.Path)
	if !ok {
		return false
	}
	return s.serveBundleFile(w, r, filePath, tenant)
}

func (s *Server) serveSPAFallbackBundle(w http.ResponseWriter, r *http.Request, tenant TenantInfo) bool {
	filePath, ok := resolveSPAFallbackPath(s.cfg.BundleRoot, tenant.TenantSlug, r)
	if !ok {
		return false
	}
	return s.serveBundleFile(w, r, filePath, tenant)
}

func (s *Server) serveBundleFile(w http.ResponseWriter, r *http.Request, filePath string, tenant TenantInfo) bool {
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	if shouldInjectAnalyticsForFile(filePath) {
		return s.serveOpenedHTMLFile(w, r, filePath, f, tenant)
	}
	return serveOpenedFile(w, r, filePath, f)
}

func serveOpenedFile(w http.ResponseWriter, r *http.Request, filePath string, f *os.File) bool {
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	http.ServeContent(w, r, filepath.Base(filePath), info.ModTime(), f)
	return true
}

func (s *Server) serveOpenedHTMLFile(w http.ResponseWriter, r *http.Request, filePath string, f *os.File, tenant TenantInfo) bool {
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	body, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}
	body = analytics.InjectGtag(body, s.analyticsConfigForTenant(tenant.TenantID))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, filepath.Base(filePath), info.ModTime(), bytes.NewReader(body))
	return true
}

func (s *Server) analyticsConfigForTenant(tenantID int64) analytics.TenantConfig {
	if s.cfg.AnalyticsConfigForTenant == nil {
		return analytics.TenantConfig{}
	}
	return s.cfg.AnalyticsConfigForTenant(tenantID)
}

func shouldInjectAnalyticsForFile(filePath string) bool {
	return strings.EqualFold(filepath.Ext(filePath), ".html")
}

func (s *Server) serveCMSPage(w http.ResponseWriter, r *http.Request, tenant TenantInfo) bool {
	if s.cfg.Pages == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	candidates := []string{tenant.SubsiteLabel}
	if tenant.SubsiteLabel != "" {
		candidates = append(candidates, "")
	}
	for _, subsite := range candidates {
		p, err := s.cfg.Pages.GetByPath(r.Context(), tenant.TenantID, subsite, r.URL.Path)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			http.Error(w, "page lookup failed", http.StatusInternalServerError)
			return true
		}
		if !internal.PagePubliclyRenderable(p, time.Now().UTC()) {
			return false
		}
		template, err := s.resolveTenantTemplate(r.Context(), tenant.TenantID, p.TemplateID)
		if err != nil {
			http.Error(w, "page template unavailable", http.StatusServiceUnavailable)
			return true
		}
		html, rendered, err := internal.RenderPageDocument(p, template, time.Now().UTC())
		if err != nil {
			http.Error(w, "page render failed", http.StatusInternalServerError)
			return true
		}
		if !rendered {
			return false
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method != http.MethodHead {
			body := analytics.InjectGtag([]byte(html), s.analyticsConfigForTenant(tenant.TenantID))
			_, _ = w.Write(body)
		}
		return true
	}
	return false
}

func (s *Server) pageTemplate(id string) internal.PageTemplate {
	if id == "" || len(s.cfg.PageTemplates) == 0 {
		return internal.PageTemplate{}
	}
	return internal.PageTemplate{ID: id, HTML: s.cfg.PageTemplates[id]}
}

func resolveSPAFallbackPath(bundleRoot, slug string, r *http.Request) (string, bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return "", false
	}
	if path.Ext(r.URL.Path) != "" {
		return "", false
	}
	return resolveBundlePath(bundleRoot, slug, "/")
}

func resolveBundlePath(bundleRoot, slug, urlPath string) (string, bool) {
	if bundleRoot == "" || slug == "" {
		return "", false
	}
	tenantRoot, err := tenantBundleRoot(bundleRoot, slug)
	if err != nil {
		return "", false
	}
	rel := strings.TrimPrefix(urlPath, "/")
	if rel == "" || strings.HasSuffix(urlPath, "/") {
		rel = filepath.Join(rel, "index.html")
	}
	cleaned := filepath.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "..\\") {
		return "", false
	}
	full := filepath.Join(tenantRoot, cleaned)
	relToRoot, err := filepath.Rel(tenantRoot, full)
	if err != nil || strings.HasPrefix(relToRoot, "..") {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", false
	}
	relToRoot, err = filepath.Rel(tenantRoot, resolved)
	if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", false
	}
	return resolved, true
}

// extractTenantIDFromPath parses /api/v1/admin/tenants/:id/upload →
// :id. Returns 0 on parse failure (handler returns 400).
func extractTenantIDFromPath(path string) int64 {
	const prefix = "/api/v1/admin/tenants/"
	if !strings.HasPrefix(path, prefix) {
		return 0
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 {
		return 0
	}
	id, _ := strconv.ParseInt(parts[0], 10, 64)
	return id
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

// ResolveTenant uses authoritative domain/tenant lookup. Host-policy decisions
// and public rendering share this lookup; an indefinitely cached mapping could
// serve the wrong tenant after a domain move. Resolver stores may optimize reads.
func (s *Server) ResolveTenant(ctx context.Context, host string) (TenantInfo, bool) {
	return s.resolveTenant(ctx, host)
}

func (s *Server) resolveTenant(ctx context.Context, host string) (TenantInfo, bool) {
	host = strings.ToLower(strings.Split(host, ":")[0])
	if s.cfg.TenantResolverStore != nil {
		if t, ok := s.cfg.TenantResolverStore.Lookup(ctx, host); ok {
			return t, true
		}
	}
	if s.cfg.PreviewSubdomainBase != "" {
		base := strings.ToLower(strings.TrimPrefix(s.cfg.PreviewSubdomainBase, "."))
		suffix := "." + base
		if strings.HasSuffix(host, suffix) {
			slug := strings.TrimSuffix(host, suffix)
			if slug != "" && !strings.Contains(slug, ".") && s.cfg.TenantResolverStore != nil {
				if t, ok := s.cfg.TenantResolverStore.LookupBySlug(ctx, slug); ok {
					t.Kind = "preview"
					return t, true
				}
			}
		}
	}
	return TenantInfo{}, false
}

// AdminAPI returns the underlying admin handler (for tests + advanced
// wiring).
func (s *Server) AdminAPI() *internal.AdminAPI { return s.admin }

type tenantAdminResolver struct {
	admin store.TenantAdminStore
}

func (r tenantAdminResolver) Lookup(ctx context.Context, host string) (TenantInfo, bool) {
	if r.admin == nil {
		return TenantInfo{}, false
	}
	host = strings.ToLower(strings.TrimSpace(strings.Split(host, ":")[0]))
	tenants, err := r.admin.ListTenants(ctx)
	if err != nil {
		return TenantInfo{}, false
	}
	for _, tenant := range tenants {
		domains, err := r.admin.ListDomains(ctx, tenant.ID)
		if err != nil {
			continue
		}
		for _, domain := range domains {
			if strings.EqualFold(domain.Host, host) {
				return TenantInfo{
					TenantID:     tenant.ID,
					TenantSlug:   tenant.Slug,
					SubsiteLabel: domain.SubsiteLabel,
					Kind:         domain.Kind,
				}, true
			}
		}
	}
	return TenantInfo{}, false
}

func (r tenantAdminResolver) LookupBySlug(ctx context.Context, slug string) (TenantInfo, bool) {
	if r.admin == nil {
		return TenantInfo{}, false
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	tenants, err := r.admin.ListTenants(ctx)
	if err != nil {
		return TenantInfo{}, false
	}
	for _, tenant := range tenants {
		if strings.EqualFold(tenant.Slug, slug) {
			return TenantInfo{TenantID: tenant.ID, TenantSlug: tenant.Slug, Kind: "preview"}, true
		}
	}
	return TenantInfo{}, false
}
