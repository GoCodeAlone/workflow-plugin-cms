package internal

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/audit"
	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

// AdminAPI is the HTTP surface for the multisite admin.
//
// Per SPEC §I/T16: tenant CRUD, domain CRUD, page CRUD. Routes are
// mounted by the host at /api/v1/admin/* (see gocodealone-multisite
// app.yaml). The host wraps every endpoint with auth + authz
// middleware; this layer assumes the request is already authenticated.
//
// Tenant scope for page endpoints comes from the URL path
// (/api/v1/admin/tenants/:id/pages...) — V12 isolation is enforced
// by passing tenantID as the first arg after ctx into every store call.
//
// ReloadFunc is optional; when set, POST /api/v1/admin/reload invokes
// it to flush in-memory caches (tenant resolver, page renderer). See
// SPEC T31.
//
// PreviewBase is the FQDN suffix for auto-provisioned preview
// subdomains. When non-empty, CreateTenant also creates a
// `<slug>.<preview_base>` domain row (V18 / T32). Empty disables
// auto-provision.
type AdminAPI struct {
	tenants     store.TenantAdminStore
	pages       store.PageStore
	ReloadFunc  func() error
	PreviewBase string
	Audit       *audit.Logger
	AuditActor  func(*http.Request) string
}

// NewAdminAPI returns a handler using the given stores. Either store
// may be the in-memory implementation (default) or a postgres-backed
// production impl.
func NewAdminAPI(tenants store.TenantAdminStore, pages store.PageStore) *AdminAPI {
	return &AdminAPI{tenants: tenants, pages: pages}
}

// ServeHTTP dispatches by method + path.
func (a *AdminAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/admin")
	switch {
	case path == "/reload" && r.Method == http.MethodPost:
		a.reload(w, r)

	case path == "/tenants" && r.Method == http.MethodGet:
		a.listTenants(w, r)
	case path == "/tenants" && r.Method == http.MethodPost:
		a.createTenant(w, r)

	case strings.HasPrefix(path, "/tenants/") && strings.HasSuffix(path, "/domains") && r.Method == http.MethodGet:
		a.listDomains(w, r, extractTenantID(path))
	case strings.HasPrefix(path, "/tenants/") && strings.HasSuffix(path, "/domains") && r.Method == http.MethodPost:
		a.createDomain(w, r, extractTenantID(path))
	case strings.HasPrefix(path, "/tenants/") && strings.Contains(path, "/domains/") && r.Method == http.MethodDelete:
		tid, did := extractTenantAndDomainID(path)
		a.deleteDomain(w, r, tid, did)

	case strings.HasPrefix(path, "/tenants/") && strings.HasSuffix(path, "/pages") && r.Method == http.MethodGet:
		a.listPages(w, r, extractTenantID(path))
	case strings.HasPrefix(path, "/tenants/") && strings.HasSuffix(path, "/pages") && r.Method == http.MethodPost:
		a.createPage(w, r, extractTenantID(path))
	case strings.HasPrefix(path, "/tenants/") && strings.Contains(path, "/pages/") && r.Method == http.MethodGet:
		tid, pid := extractTenantAndPageID(path)
		a.getPage(w, r, tid, pid)
	case strings.HasPrefix(path, "/tenants/") && strings.Contains(path, "/pages/") && r.Method == http.MethodPut:
		tid, pid := extractTenantAndPageID(path)
		a.updatePage(w, r, tid, pid)
	case strings.HasPrefix(path, "/tenants/") && strings.Contains(path, "/pages/") && r.Method == http.MethodDelete:
		tid, pid := extractTenantAndPageID(path)
		a.deletePage(w, r, tid, pid)

	case strings.HasPrefix(path, "/tenants/") && strings.HasSuffix(path, "/overlays/clone") && r.Method == http.MethodPost:
		a.cloneOverlay(w, r, extractTenantID(path))
	case strings.HasPrefix(path, "/tenants/") && strings.HasSuffix(path, "/overlays/publish") && r.Method == http.MethodPut:
		a.publishOverlay(w, r, extractTenantID(path))
	case strings.HasPrefix(path, "/tenants/") && strings.HasSuffix(path, "/overlays/disable") && r.Method == http.MethodPut:
		a.disableOverlay(w, r, extractTenantID(path))

	case strings.HasPrefix(path, "/tenants/") && strings.HasSuffix(path, "/nav/published") && r.Method == http.MethodPost:
		a.publishedNavigation(w, r, extractTenantID(path))
	case strings.HasPrefix(path, "/tenants/") && strings.HasSuffix(path, "/widgets/render") && r.Method == http.MethodPost:
		a.renderWidget(w, r, extractTenantID(path))
	case strings.HasPrefix(path, "/tenants/") && strings.HasSuffix(path, "/media/validate") && r.Method == http.MethodPost:
		a.validateMedia(w, r, extractTenantID(path))

	default:
		writeJSONError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

// --- Reload handler -----------------------------------------------------

func (a *AdminAPI) reload(w http.ResponseWriter, r *http.Request) {
	if a.ReloadFunc == nil {
		writeJSON(w, http.StatusOK, map[string]any{"reloaded": false, "reason": "no reload func installed"})
		return
	}
	if err := a.ReloadFunc(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reloaded": true})
}

// --- Tenant handlers ----------------------------------------------------

type tenantBody struct {
	Slug    string `json:"slug"`
	Label   string `json:"label"`
	ThemeID string `json:"theme_id"`
}

func (a *AdminAPI) listTenants(w http.ResponseWriter, r *http.Request) {
	tenants, err := a.tenants.ListTenants(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": tenants})
}

func (a *AdminAPI) createTenant(w http.ResponseWriter, r *http.Request) {
	var body tenantBody
	if err := decodeJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(body.Slug) == "" {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "slug required")
		return
	}
	t := &store.Tenant{Slug: body.Slug, Label: body.Label, ThemeID: body.ThemeID}
	if err := a.tenants.CreateTenant(r.Context(), t); err != nil {
		statusFromTenantErr(w, err)
		return
	}
	var previewDomain *store.Domain
	// Auto-provision preview subdomain (T32 / V18).
	if a.PreviewBase != "" {
		previewHost := strings.ToLower(t.Slug) + "." + strings.ToLower(strings.TrimPrefix(a.PreviewBase, "."))
		d := &store.Domain{
			TenantID: t.ID, Host: previewHost, Kind: "preview",
		}
		if err := a.tenants.CreateDomain(r.Context(), d); err == nil {
			previewDomain = d
		}
	}
	actor := a.auditActor(r)
	if !a.recordAudit(w, actor, t.ID, "tenant.create", "tenant:"+strconv.FormatInt(t.ID, 10), map[string]any{"slug": t.Slug}) {
		return
	}
	if previewDomain != nil && !a.recordAudit(w, actor, t.ID, "domain.create", "domain:"+strconv.FormatInt(previewDomain.ID, 10), map[string]any{"host": previewDomain.Host, "kind": previewDomain.Kind, "auto_preview": true}) {
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// --- Domain handlers ----------------------------------------------------

type domainBody struct {
	Host         string `json:"host"`
	SubsiteLabel string `json:"subsite_label"`
	Kind         string `json:"kind"`
}

func (a *AdminAPI) listDomains(w http.ResponseWriter, r *http.Request, tenantID int64) {
	if tenantID == 0 {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid tenant id")
		return
	}
	list, err := a.tenants.ListDomains(r.Context(), tenantID)
	if err != nil {
		statusFromTenantErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": list})
}

func (a *AdminAPI) createDomain(w http.ResponseWriter, r *http.Request, tenantID int64) {
	if tenantID == 0 {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid tenant id")
		return
	}
	var body domainBody
	if err := decodeJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(body.Host) == "" {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "host required")
		return
	}
	if body.Kind == "" {
		body.Kind = "vanity"
	}
	d := &store.Domain{
		TenantID:     tenantID,
		Host:         body.Host,
		SubsiteLabel: body.SubsiteLabel,
		Kind:         body.Kind,
	}
	if err := a.tenants.CreateDomain(r.Context(), d); err != nil {
		statusFromTenantErr(w, err)
		return
	}
	if !a.recordAudit(w, a.auditActor(r), tenantID, "domain.create", "domain:"+strconv.FormatInt(d.ID, 10), map[string]any{"host": d.Host, "kind": d.Kind}) {
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (a *AdminAPI) deleteDomain(w http.ResponseWriter, r *http.Request, tenantID, domainID int64) {
	if tenantID == 0 || domainID == 0 {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if err := a.tenants.DeleteDomain(r.Context(), tenantID, domainID); err != nil {
		statusFromTenantErr(w, err)
		return
	}
	if !a.recordAudit(w, a.auditActor(r), tenantID, "domain.delete", "domain:"+strconv.FormatInt(domainID, 10), nil) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Page handlers ------------------------------------------------------

func (a *AdminAPI) listPages(w http.ResponseWriter, r *http.Request, tenantID int64) {
	if tenantID == 0 {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid tenant id")
		return
	}
	subsite := r.URL.Query().Get("subsite")
	if a.pages == nil {
		writeJSON(w, http.StatusOK, map[string]any{"pages": []any{}})
		return
	}
	list, err := a.pages.List(r.Context(), tenantID, subsite)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": list})
}

// --- Page write handlers ------------------------------------------------

type pageBody struct {
	Subsite     string          `json:"subsite"`
	Path        string          `json:"path"`
	Title       string          `json:"title"`
	BodyHTML    string          `json:"body_html"`
	BodyBlocks  json.RawMessage `json:"body_blocks"`
	Status      string          `json:"status"`
	TemplateID  string          `json:"template_id"`
	PublishAt   *time.Time      `json:"publish_at"`
	UnpublishAt *time.Time      `json:"unpublish_at"`
}

type pageUpdateBody struct {
	Subsite     *string         `json:"subsite"`
	Path        *string         `json:"path"`
	Title       *string         `json:"title"`
	BodyHTML    *string         `json:"body_html"`
	BodyBlocks  json.RawMessage `json:"body_blocks"`
	Status      *string         `json:"status"`
	TemplateID  *string         `json:"template_id"`
	PublishAt   *time.Time      `json:"publish_at"`
	UnpublishAt *time.Time      `json:"unpublish_at"`
}

func (a *AdminAPI) createPage(w http.ResponseWriter, r *http.Request, tenantID int64) {
	if tenantID == 0 {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid tenant id")
		return
	}
	if a.pages == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "unavailable", "page store not configured")
		return
	}
	var body pageBody
	if err := decodeJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	p := &store.Page{
		TenantID:    tenantID,
		Subsite:     body.Subsite,
		Path:        body.Path,
		Title:       body.Title,
		BodyHTML:    body.BodyHTML,
		BodyBlocks:  body.BodyBlocks,
		Status:      store.PageStatus(body.Status),
		TemplateID:  body.TemplateID,
		PublishAt:   body.PublishAt,
		UnpublishAt: body.UnpublishAt,
	}
	if err := p.Validate(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := a.pages.Create(r.Context(), tenantID, p); err != nil {
		statusFromPageErr(w, err)
		return
	}
	if !a.recordAudit(w, a.auditActor(r), tenantID, "page.create", "page:"+strconv.FormatInt(p.ID, 10), map[string]any{"path": p.Path, "status": string(p.Status)}) {
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (a *AdminAPI) getPage(w http.ResponseWriter, r *http.Request, tenantID, pageID int64) {
	if tenantID == 0 || pageID == 0 {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if a.pages == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "unavailable", "page store not configured")
		return
	}
	p, err := a.pages.Get(r.Context(), tenantID, pageID)
	if err != nil {
		statusFromPageErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *AdminAPI) updatePage(w http.ResponseWriter, r *http.Request, tenantID, pageID int64) {
	if tenantID == 0 || pageID == 0 {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if a.pages == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "unavailable", "page store not configured")
		return
	}
	existing, err := a.pages.Get(r.Context(), tenantID, pageID)
	if err != nil {
		statusFromPageErr(w, err)
		return
	}
	var body pageUpdateBody
	if err := decodeJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if body.Path != nil {
		existing.Path = *body.Path
	}
	if body.Title != nil {
		existing.Title = *body.Title
	}
	if body.Subsite != nil {
		existing.Subsite = *body.Subsite
	}
	if body.BodyHTML != nil {
		existing.BodyHTML = *body.BodyHTML
	}
	if len(body.BodyBlocks) > 0 {
		existing.BodyBlocks = body.BodyBlocks
	}
	if body.Status != nil {
		existing.Status = store.PageStatus(*body.Status)
	}
	if body.TemplateID != nil {
		existing.TemplateID = *body.TemplateID
	}
	if body.PublishAt != nil {
		existing.PublishAt = body.PublishAt
	}
	if body.UnpublishAt != nil {
		existing.UnpublishAt = body.UnpublishAt
	}
	if err := existing.Validate(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := a.pages.Update(r.Context(), tenantID, existing); err != nil {
		statusFromPageErr(w, err)
		return
	}
	if !a.recordAudit(w, a.auditActor(r), tenantID, "page.update", "page:"+strconv.FormatInt(existing.ID, 10), map[string]any{"path": existing.Path, "status": string(existing.Status), "version": existing.Version}) {
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

func (a *AdminAPI) deletePage(w http.ResponseWriter, r *http.Request, tenantID, pageID int64) {
	if tenantID == 0 || pageID == 0 {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if a.pages == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "unavailable", "page store not configured")
		return
	}
	if err := a.pages.Delete(r.Context(), tenantID, pageID); err != nil {
		statusFromPageErr(w, err)
		return
	}
	if !a.recordAudit(w, a.auditActor(r), tenantID, "page.delete", "page:"+strconv.FormatInt(pageID, 10), nil) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Overlay handlers ---------------------------------------------------

type overlayPublishBody struct {
	Overlay           StaticPageOverlay `json:"overlay"`
	CurrentSourceHash string            `json:"current_source_hash"`
	Force             bool              `json:"force"`
}

type overlayDisableBody struct {
	Overlay StaticPageOverlay `json:"overlay"`
}

func (a *AdminAPI) cloneOverlay(w http.ResponseWriter, r *http.Request, tenantID int64) {
	if tenantID == 0 {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid tenant id")
		return
	}
	var body StaticPageOverlayInput
	if err := decodeJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	body.TenantID = tenantID
	overlay, err := NewStaticPageOverlay(body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !a.recordAudit(w, a.auditActor(r), tenantID, "overlay.clone", "overlay:"+overlay.SourcePath, map[string]any{"source_path": overlay.SourcePath, "source_hash": overlay.SourceHash}) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"overlay": overlay})
}

func (a *AdminAPI) publishOverlay(w http.ResponseWriter, r *http.Request, tenantID int64) {
	if tenantID == 0 {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid tenant id")
		return
	}
	var body overlayPublishBody
	if err := decodeJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if body.Overlay.TenantID != tenantID {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "overlay tenant mismatch")
		return
	}
	result, err := PublishOverlay(&body.Overlay, body.CurrentSourceHash, body.Force)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !a.recordAudit(w, a.auditActor(r), tenantID, "overlay.publish", "overlay:"+body.Overlay.SourcePath, map[string]any{"published": result.Published, "status": string(body.Overlay.Status), "forced": body.Force}) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"overlay": body.Overlay, "result": result})
}

func (a *AdminAPI) disableOverlay(w http.ResponseWriter, r *http.Request, tenantID int64) {
	if tenantID == 0 {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid tenant id")
		return
	}
	var body overlayDisableBody
	if err := decodeJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if body.Overlay.TenantID != tenantID {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "overlay tenant mismatch")
		return
	}
	DisableOverlay(&body.Overlay)
	if !a.recordAudit(w, a.auditActor(r), tenantID, "overlay.disable", "overlay:"+body.Overlay.SourcePath, map[string]any{"source_path": body.Overlay.SourcePath}) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"overlay": body.Overlay})
}

// --- Navigation/widget/media policy handlers ----------------------------

type navigationBody struct {
	Items []NavigationItem `json:"items"`
	Now   *time.Time       `json:"now"`
}

type widgetRenderBody struct {
	Instance WidgetInstance        `json:"instance"`
	Types    map[string]WidgetType `json:"types"`
}

type mediaValidateBody struct {
	Reference             string   `json:"reference"`
	AllowedObjectPrefixes []string `json:"allowed_object_prefixes"`
}

func (a *AdminAPI) publishedNavigation(w http.ResponseWriter, r *http.Request, tenantID int64) {
	if tenantID == 0 {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid tenant id")
		return
	}
	var body navigationBody
	if err := decodeJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	now := time.Now().UTC()
	if body.Now != nil {
		now = body.Now.UTC()
	}
	items, err := PublishedNavigation(body.Items, now)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *AdminAPI) renderWidget(w http.ResponseWriter, r *http.Request, tenantID int64) {
	if tenantID == 0 {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid tenant id")
		return
	}
	var body widgetRenderBody
	if err := decodeJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	html, err := RenderWidgetInstance(body.Instance, WidgetRegistry{Types: body.Types})
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"html": html})
}

func (a *AdminAPI) validateMedia(w http.ResponseWriter, r *http.Request, tenantID int64) {
	if tenantID == 0 {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid tenant id")
		return
	}
	var body mediaValidateBody
	if err := decodeJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	err := ValidatePublishedMediaReference(body.Reference, MediaPolicy{AllowedObjectPrefixes: body.AllowedObjectPrefixes})
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true})
}

func (a *AdminAPI) auditActor(r *http.Request) string {
	if a.AuditActor != nil {
		if actor := strings.TrimSpace(a.AuditActor(r)); actor != "" {
			return actor
		}
	}
	return "admin"
}

func (a *AdminAPI) recordAudit(w http.ResponseWriter, actor string, tenantID int64, action, subject string, meta map[string]any) bool {
	if a.Audit == nil {
		return true
	}
	if _, err := a.Audit.Record(actor, tenantID, action, subject, meta); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "audit_failed", err.Error())
		return false
	}
	return true
}

func statusFromPageErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, store.ErrPathConflict):
		writeJSONError(w, http.StatusConflict, "conflict", err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

func extractTenantAndPageID(path string) (int64, int64) {
	parts := strings.Split(path, "/")
	var tid, pid int64
	for i, p := range parts {
		if p == "tenants" && i+1 < len(parts) {
			tid, _ = strconv.ParseInt(parts[i+1], 10, 64)
		}
		if p == "pages" && i+1 < len(parts) {
			pid, _ = strconv.ParseInt(parts[i+1], 10, 64)
		}
	}
	return tid, pid
}

// --- helpers ------------------------------------------------------------

func extractTenantID(path string) int64 {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if p == "tenants" && i+1 < len(parts) {
			id, _ := strconv.ParseInt(parts[i+1], 10, 64)
			return id
		}
	}
	return 0
}

func extractTenantAndDomainID(path string) (int64, int64) {
	parts := strings.Split(path, "/")
	var tid, did int64
	for i, p := range parts {
		if p == "tenants" && i+1 < len(parts) {
			tid, _ = strconv.ParseInt(parts[i+1], 10, 64)
		}
		if p == "domains" && i+1 < len(parts) {
			did, _ = strconv.ParseInt(parts[i+1], 10, 64)
		}
	}
	return tid, did
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": code, "message": msg})
}

// statusFromTenantErr maps store sentinels onto HTTP codes.
func statusFromTenantErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrTenantNotFound), errors.Is(err, store.ErrDomainNotFound):
		writeJSONError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, store.ErrTenantSlugTaken), errors.Is(err, store.ErrDomainTaken):
		writeJSONError(w, http.StatusConflict, "conflict", err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}
