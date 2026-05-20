package internal

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// staticServeModule serves static files for the resolved tenant BEFORE
// any dynamic CMS resolution. Static wins on path collisions.
//
// Per gocodealone-multisite SPEC.md:
//   V3: ∀ path → static file lookup FIRST; dynamic CMS lookup only on
//       static-miss (C7).
//
// Bundle layout (per tenant):
//
//   <bundle_root>/<tenant_slug>/current → symlink to active version dir
//   <bundle_root>/<tenant_slug>/<version>/...
//
// The middleware:
//   1. Reads TenantInfo from request context (set by tenantResolverModule).
//   2. Computes the static file path:
//        <bundle_root>/<tenant_slug>/current/<requested_path>
//      Trailing-slash paths resolve to /index.html.
//   3. If the file exists: serves it (200) and short-circuits.
//   4. If the file does not exist: passes through to next handler.
//   5. Refuses any cleaned path that escapes the tenant's current dir
//      (directory traversal guard).
type staticServeModule struct {
	name       string
	bundleRoot string
}

func newStaticServeModule(name string, config map[string]any) (sdk.ModuleInstance, error) {
	m := &staticServeModule{name: name}
	if v, ok := config["bundle_root"].(string); ok {
		m.bundleRoot = v
	}
	return m, nil
}

func (m *staticServeModule) Name() string                    { return m.name }
func (m *staticServeModule) Init() error                     { return nil }
func (m *staticServeModule) Start(ctx context.Context) error { return nil }
func (m *staticServeModule) Stop(ctx context.Context) error  { return nil }

// Middleware wraps the downstream handler. On static-file hit, serves
// directly. On miss, passes through.
func (m *staticServeModule) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Tenant must already be resolved upstream.
		info, ok := TenantFromContext(r.Context())
		if !ok || info.TenantSlug == "" {
			next.ServeHTTP(w, r)
			return
		}

		// 2. Compute target file path, guarding against traversal.
		filePath, ok := m.resolvePath(info.TenantSlug, r.URL.Path)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		// 3. Open the file (handles existence + directory in one go).
		f, err := os.Open(filePath)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil || fi.IsDir() {
			next.ServeHTTP(w, r)
			return
		}

		// 4. Serve via ServeContent — avoids http.ServeFile's
		// auto-redirect behaviour on /index.html → / which would break
		// downstream tests + actual request paths.
		http.ServeContent(w, r, filepath.Base(filePath), fi.ModTime(), f)
	})
}

// resolvePath returns the on-disk path for the requested URL path scoped
// to the tenant. Returns ok=false when the path tries to escape the
// tenant's current dir (directory traversal).
func (m *staticServeModule) resolvePath(slug, urlPath string) (string, bool) {
	if m.bundleRoot == "" || slug == "" {
		return "", false
	}

	tenantRoot := filepath.Join(m.bundleRoot, slug, "current")

	// Strip leading slash; default empty to index.html.
	rel := strings.TrimPrefix(urlPath, "/")
	if rel == "" || strings.HasSuffix(urlPath, "/") {
		rel = filepath.Join(rel, "index.html")
	}

	// Clean the relative path. filepath.Clean collapses ".." segments
	// but allows them to escape; we explicitly reject any clean result
	// that starts with ".." (escape detected).
	cleaned := filepath.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "..\\") {
		return "", false
	}

	full := filepath.Join(tenantRoot, cleaned)

	// Defense-in-depth: ensure the final resolved path is still under
	// tenantRoot (catches edge cases on platforms where Join collapses
	// trailing ".."). Using filepath.Rel because Abs(tenantRoot) and
	// Abs(full) may differ under symlinks but Rel checks the relative
	// form.
	rel2, err := filepath.Rel(tenantRoot, full)
	if err != nil || strings.HasPrefix(rel2, "..") {
		return "", false
	}

	return full, true
}
