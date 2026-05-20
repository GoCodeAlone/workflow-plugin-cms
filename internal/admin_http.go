package internal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

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
type AdminAPI struct {
	tenants store.TenantAdminStore
	pages   store.PageStore
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
	default:
		writeJSONError(w, http.StatusNotFound, "not_found", "route not found")
	}
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

// Ensure context import is used.
var _ = context.Background
