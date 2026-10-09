package host

import (
	"context"
	"errors"
	"github.com/GoCodeAlone/workflow-plugin-cms/internal"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var templateIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var tenantSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func (s *Server) resolveTenantTemplate(ctx context.Context, id int64, name string) (internal.PageTemplate, error) {
	if name == "" {
		return internal.PageTemplate{}, nil
	}
	if !templateIDPattern.MatchString(name) {
		return internal.PageTemplate{}, errors.New("invalid template")
	}
	if s.cfg.TenantsAdmin == nil {
		if value, ok := s.cfg.PageTemplates[name]; ok {
			return internal.PageTemplate{ID: name, HTML: value}, nil
		}
		return internal.PageTemplate{}, errors.New("tenant templates unavailable")
	}
	tenant, err := s.cfg.TenantsAdmin.GetTenant(ctx, id)
	if err != nil {
		if value, ok := s.cfg.PageTemplates[name]; ok {
			return internal.PageTemplate{ID: name, HTML: value}, nil
		}
		return internal.PageTemplate{}, err
	}
	root, err := tenantBundleRoot(s.cfg.BundleRoot, tenant.Slug)
	var file *os.File
	if err == nil {
		file, err = scopedFile(root, filepath.Join("cms", "templates", name+".html"))
	}
	if err != nil {
		// Explicit host templates remain supported for legacy hosts.
		if value, ok := s.cfg.PageTemplates[name]; ok {
			return internal.PageTemplate{ID: name, HTML: value}, nil
		}
		return internal.PageTemplate{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || strings.Count(string(data), "<!--cms:body-->") != 1 {
		return internal.PageTemplate{}, errors.New("invalid template shell")
	}
	return internal.PageTemplate{ID: name, HTML: string(data)}, nil
}

func (s *Server) listTenantTemplates(ctx context.Context, id int64) ([]string, error) {
	tenant, err := s.cfg.TenantsAdmin.GetTenant(ctx, id)
	if err != nil {
		return nil, err
	}
	names := []string{}
	root, rootErr := tenantBundleRoot(s.cfg.BundleRoot, tenant.Slug)
	var entries []os.DirEntry
	if rootErr == nil {
		entries, _ = os.ReadDir(filepath.Join(root, "cms", "templates"))
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".html")
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".html") && templateIDPattern.MatchString(name) {
			if _, err := s.resolveTenantTemplate(ctx, id, name); err == nil {
				names = append(names, name)
			}
		}
	}
	for name := range s.cfg.PageTemplates {
		if templateIDPattern.MatchString(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func scopedFile(root, rel string) (*os.File, error) {
	if filepath.IsAbs(rel) || rel == "" || strings.HasPrefix(filepath.Clean(rel), "..") {
		return nil, errors.New("invalid path")
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	target, err := filepath.EvalSymlinks(filepath.Join(root, rel))
	if err != nil {
		return nil, err
	}
	inside, err := filepath.Rel(realRoot, target)
	if err != nil || strings.HasPrefix(inside, "..") {
		return nil, errors.New("outside tenant bundle")
	}
	f, err := os.Open(target)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("not a regular file")
	}
	return f, nil
}

// The trusted boundary is the tenant directory, not its mutable current link.
// current may point to a version within that tenant, never a sibling tenant.
func tenantBundleRoot(bundleRoot, slug string) (string, error) {
	if bundleRoot == "" || !tenantSlugPattern.MatchString(slug) {
		return "", errors.New("invalid tenant bundle")
	}
	base, err := filepath.EvalSymlinks(bundleRoot)
	if err != nil {
		return "", err
	}
	scope := filepath.Join(base, slug)
	realScope, err := filepath.EvalSymlinks(scope)
	if err != nil || realScope != scope {
		return "", errors.New("tenant directory escapes bundle scope")
	}
	current, err := filepath.EvalSymlinks(filepath.Join(scope, "current"))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(scope, current)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("current bundle escapes tenant scope")
	}
	return current, nil
}

func (s *Server) servePreviewAsset(w http.ResponseWriter, r *http.Request) {
	id := extractTenantIDFromPath(r.URL.Path)
	prefix := "/api/v1/admin/tenants/" + strconv.FormatInt(id, 10) + "/pages/assets/"
	if id <= 0 || !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	if s.cfg.AdminTenantAccess != nil && !s.cfg.AdminTenantAccess(r, id) {
		http.Error(w, "forbidden", 403)
		return
	}
	tenant, err := s.cfg.TenantsAdmin.GetTenant(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, prefix)
	if !strings.HasPrefix(rel, "assets/") {
		http.NotFound(w, r)
		return
	}
	types := map[string]string{".css": "text/css; charset=utf-8", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".woff": "font/woff", ".woff2": "font/woff2", ".ttf": "font/ttf"}
	typ, ok := types[strings.ToLower(filepath.Ext(rel))]
	if !ok {
		http.NotFound(w, r)
		return
	}
	root, err := tenantBundleRoot(s.cfg.BundleRoot, tenant.Slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	assetRoot := filepath.Join(root, "assets")
	realAssets, err := filepath.EvalSymlinks(assetRoot)
	if err != nil || realAssets != assetRoot {
		http.NotFound(w, r)
		return
	}
	f, err := scopedFile(assetRoot, strings.TrimPrefix(rel, "assets/"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(typ, "image/") {
		got, _, _ := mime.ParseMediaType(http.DetectContentType(data))
		if got != typ {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}
