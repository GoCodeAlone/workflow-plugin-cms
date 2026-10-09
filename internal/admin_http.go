package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	tenants      store.TenantAdminStore
	pages        store.PageStore
	ReloadFunc   func() error
	PreviewBase  string
	Audit        *audit.Logger
	AuditActor   func(*http.Request) string
	TenantAccess func(*http.Request, int64) bool
	// PlatformAccess must authorize current platform authority, including reads.
	// Missing configuration fails closed for global and domain administration.
	PlatformAccess func(*http.Request) bool
	// RequestAccess projects page permissions without dispatching a write and
	// repeats the host's authorization inside this handler when configured.
	RequestAccess   func(*http.Request) bool
	ResolveTemplate func(context.Context, int64, string) (PageTemplate, error)
	ListTemplates   func(context.Context, int64) ([]string, error)
}

// NewAdminAPI returns a handler using the given stores. Either store
// may be the in-memory implementation (default) or a postgres-backed
// production impl.
func NewAdminAPI(tenants store.TenantAdminStore, pages store.PageStore) *AdminAPI {
	return &AdminAPI{tenants: tenants, pages: pages}
}

// ServeHTTP dispatches by method + path.
func (a *AdminAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, tid, rid := parseAdminRoute(r.URL.Path)
	if route == "" {
		writeJSONError(w, 404, "not_found", "route not found")
		return
	}
	platform := route == "/reload" || (route == "/tenants" && r.Method != http.MethodGet) || strings.HasPrefix(route, "/domains")
	if platform && (a.PlatformAccess == nil || !a.PlatformAccess(r)) {
		writeJSONError(w, 403, "forbidden", "platform access denied")
		return
	}
	if a.RequestAccess != nil && !a.RequestAccess(r) {
		writeJSONError(w, 403, "forbidden", "request access denied")
		return
	}
	if tid > 0 && a.TenantAccess != nil && !a.TenantAccess(r, tid) {
		writeJSONError(w, 403, "forbidden", "tenant access denied")
		return
	}
	if a.AuditActor != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete) {
		r = r.WithContext(store.WithPageWriteActor(r.Context(), a.AuditActor(r)))
	}
	switch {
	case route == "/reload" && r.Method == "POST":
		a.reload(w, r)
	case route == "/tenants" && r.Method == "GET":
		a.listTenants(w, r)
	case route == "/tenants" && r.Method == "POST":
		a.createTenant(w, r)
	case route == "/domains" && r.Method == "GET":
		a.listDomains(w, r, tid)
	case route == "/domains" && r.Method == "POST":
		a.createDomain(w, r, tid)
	case route == "/domains/id" && r.Method == "DELETE":
		a.deleteDomain(w, r, tid, rid)
	case route == "/pages" && r.Method == "GET":
		a.listPages(w, r, tid)
	case route == "/pages" && r.Method == "POST":
		a.createPage(w, r, tid)
	case route == "/pages/id" && r.Method == "GET":
		a.getPage(w, r, tid, rid)
	case route == "/pages/id" && r.Method == "PUT":
		a.updatePage(w, r, tid, rid)
	case route == "/pages/id" && r.Method == "DELETE":
		a.deletePage(w, r, tid, rid)
	case route == "/pages/preview" && r.Method == "POST":
		a.previewPage(w, r, tid)
	case route == "/pages/templates" && r.Method == "GET":
		a.pageTemplates(w, r, tid)
	case route == "/pages/permissions" && r.Method == "GET":
		a.pagePermissions(w, r, tid)
	case route == "/pages/history" && r.Method == "GET":
		a.pageHistory(w, r, tid)
	case route == "/overlays/clone" && r.Method == "POST":
		a.cloneOverlay(w, r, tid)
	case route == "/overlays/publish" && r.Method == "PUT":
		a.publishOverlay(w, r, tid)
	case route == "/overlays/disable" && r.Method == "PUT":
		a.disableOverlay(w, r, tid)
	case route == "/nav/published" && r.Method == "POST":
		a.publishedNavigation(w, r, tid)
	case route == "/widgets/render" && r.Method == "POST":
		a.renderWidget(w, r, tid)
	case route == "/media/validate" && r.Method == "POST":
		a.validateMedia(w, r, tid)
	default:
		writeJSONError(w, 404, "not_found", "route not found")
	}
}

// Parse once: authorization and dispatch consume the same ID. No suffix routes,
// repeated tenant/page segments, empty segments or noncanonical numeric IDs.
func parseAdminRoute(path string) (route string, tid, rid int64) {
	const prefix = "/api/v1/admin/"
	if !strings.HasPrefix(path, prefix) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) == 1 && (parts[0] == "tenants" || parts[0] == "reload") {
		return "/" + parts[0], 0, 0
	}
	if len(parts) < 3 || len(parts) > 4 || parts[0] != "tenants" {
		return
	}
	tid, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || tid <= 0 || strconv.FormatInt(tid, 10) != parts[1] {
		return "", 0, 0
	}
	if len(parts) == 3 && (parts[2] == "pages" || parts[2] == "domains") {
		return "/" + parts[2], tid, 0
	}
	if len(parts) != 4 {
		return "", 0, 0
	}
	route = "/" + parts[2] + "/" + parts[3]
	switch route {
	case "/pages/preview", "/pages/templates", "/pages/permissions", "/pages/history", "/overlays/clone", "/overlays/publish", "/overlays/disable", "/nav/published", "/widgets/render", "/media/validate":
		return route, tid, 0
	}
	if parts[2] != "pages" && parts[2] != "domains" {
		return "", 0, 0
	}
	rid, err = strconv.ParseInt(parts[3], 10, 64)
	if err != nil || rid <= 0 || strconv.FormatInt(rid, 10) != parts[3] {
		return "", 0, 0
	}
	return "/" + parts[2] + "/id", tid, rid
}

// pagePermissions invokes the same callbacks as writes, but never dispatches
// them. A page ID affects the probe target only; it grants no authority.
func (a *AdminAPI) pagePermissions(w http.ResponseWriter, r *http.Request, tid int64) {
	w.Header().Set("Cache-Control", "private, no-store")
	if a.RequestAccess == nil || a.TenantAccess == nil {
		writeJSONError(w, 503, "unavailable", "permission projection not configured")
		return
	}
	pid := int64(0)
	if values, present := r.URL.Query()["page_id"]; present {
		if len(values) != 1 {
			writeJSONError(w, 400, "bad_request", "canonical page_id required")
			return
		}
		var err error
		pid, err = strconv.ParseInt(values[0], 10, 64)
		if err != nil || pid <= 0 || strconv.FormatInt(pid, 10) != values[0] {
			writeJSONError(w, 400, "bad_request", "canonical page_id required")
			return
		}
	}
	base := "/api/v1/admin/tenants/" + strconv.FormatInt(tid, 10) + "/pages"
	probe := func(method, path string) bool {
		p := r.Clone(r.Context())
		u := *r.URL
		u.Path, u.RawPath, u.RawQuery = path, "", ""
		p.URL, p.Method, p.RequestURI, p.Body, p.ContentLength = &u, method, "", http.NoBody, 0
		return a.RequestAccess(p) && a.TenantAccess(p, tid)
	}
	edit, remove := false, false
	if pid > 0 {
		path := base + "/" + strconv.FormatInt(pid, 10)
		edit, remove = probe(http.MethodPut, path), probe(http.MethodDelete, path)
	}
	writeJSON(w, 200, map[string]bool{"create": probe(http.MethodPost, base), "edit": edit, "delete": remove})
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
	if a.TenantAccess != nil {
		visible := make([]*store.Tenant, 0, len(tenants))
		for _, tenant := range tenants {
			if a.TenantAccess(r, tenant.ID) {
				visible = append(visible, tenant)
			}
		}
		tenants = visible
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
	ExpectedVersion int             `json:"expected_version"`
	Subsite         *string         `json:"subsite"`
	Path            *string         `json:"path"`
	Title           *string         `json:"title"`
	BodyHTML        *string         `json:"body_html"`
	BodyBlocks      json.RawMessage `json:"body_blocks"`
	Status          *string         `json:"status"`
	TemplateID      *string         `json:"template_id"`
	PublishAt       json.RawMessage `json:"publish_at"`
	UnpublishAt     json.RawMessage `json:"unpublish_at"`
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
	if strings.TrimSpace(string(body.BodyBlocks)) == "null" {
		body.BodyBlocks = nil
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
	body, err := renderPageBody(p)
	if err != nil {
		writeJSONError(w, 400, "bad_request", "invalid page blocks")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		*store.Page
		RenderedBodyHTML string
	}{p, body})
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
	if body.ExpectedVersion <= 0 {
		writeJSONError(w, http.StatusPreconditionRequired, "precondition_required", "expected_version required; reload the page")
		return
	}
	if body.ExpectedVersion != existing.Version {
		statusFromPageErr(w, store.ErrVersionConflict)
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
		if len(body.BodyBlocks) == 0 {
			existing.BodyBlocks = nil
		}
	}
	if len(body.BodyBlocks) > 0 {
		if string(body.BodyBlocks) == "null" {
			existing.BodyBlocks = nil
		} else {
			existing.BodyBlocks = body.BodyBlocks
		}
	}
	if body.Status != nil {
		existing.Status = store.PageStatus(*body.Status)
	}
	if body.TemplateID != nil {
		existing.TemplateID = *body.TemplateID
	}
	if len(body.PublishAt) > 0 {
		if err := json.Unmarshal(body.PublishAt, &existing.PublishAt); err != nil {
			writeJSONError(w, 400, "bad_request", "invalid publish_at")
			return
		}
	}
	if len(body.UnpublishAt) > 0 {
		if err := json.Unmarshal(body.UnpublishAt, &existing.UnpublishAt); err != nil {
			writeJSONError(w, 400, "bad_request", "invalid unpublish_at")
			return
		}
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
	var body struct {
		ExpectedVersion int `json:"expected_version"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "invalid JSON request")
		return
	}
	if body.ExpectedVersion <= 0 {
		writeJSONError(w, http.StatusPreconditionRequired, "precondition_required", "expected_version required; reload the page")
		return
	}
	if err := a.pages.Delete(r.Context(), tenantID, pageID, body.ExpectedVersion); err != nil {
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
	case errors.Is(err, store.ErrVersionConflict):
		writeJSONError(w, http.StatusConflict, "version_conflict", err.Error())
	case errors.Is(err, store.ErrPathConflict):
		writeJSONError(w, http.StatusConflict, "conflict", err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// --- helpers ------------------------------------------------------------

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, (8<<20)+1))
	if err != nil || len(b) > 8<<20 {
		return errors.New("invalid JSON request")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("invalid JSON request")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("invalid JSON request")
	}
	return nil
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
