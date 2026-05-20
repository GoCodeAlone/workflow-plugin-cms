// Package adminui serves the minimum-viable admin web app for the
// multisite host. Embedded static assets — no JS build chain required.
//
// Per gocodealone-multisite SPEC T15. The shipped surface covers:
//
//   - Tenant list + create
//   - Domain list per tenant
//   - Page list + create + edit + delete per tenant
//   - Reload tenants cache
//
// Auth + WebAuthn enrolment land in a follow-up once the auth library
// (workflow-plugin-auth#23) exposes an embeddable handler.
package adminui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var staticFS embed.FS

// Handler returns an http.Handler that serves the admin UI under the
// path prefix passed to http.StripPrefix. Pass "/admin" to mount under
// /admin/*.
func Handler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Embed errors are compile-time — this branch is unreachable.
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}
