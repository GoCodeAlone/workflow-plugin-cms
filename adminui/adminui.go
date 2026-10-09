// Package adminui provides separately rendered tenant editing and platform
// administration surfaces. The host must authorize each mount and tenant route.
package adminui

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
)

//go:embed static
var staticFS embed.FS

type Mode string

const (
	Editor   Mode = "editor"
	Platform Mode = "platform"
)

type Options struct {
	Mode     Mode
	BasePath string
}

// Route is the canonical UI context, also used by the host's authorization gate.
type Route struct {
	TenantID, PageID int64
	View             string
	Section          int
	NewPage          bool
}

// ParseRoute accepts only declared view routes. Numeric IDs must be canonical;
// assets and arbitrary suffixes are never interpreted as tenant views.
func ParseRoute(mode Mode, path string) (Route, bool) {
	if path == "/" || path == "" {
		return Route{View: "tenants"}, true
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if mode == Platform {
		if path == "/tenants" || path == "/tenants/new" || path == "/cache" {
			return Route{View: strings.TrimPrefix(path, "/")}, true
		}
		if len(parts) == 3 && parts[0] == "tenants" && parts[2] == "domains" {
			id, ok := canonicalID(parts[1])
			return Route{TenantID: id, View: "domains"}, ok
		}
		return Route{}, false
	}
	if mode != Editor || len(parts) < 3 || parts[0] != "tenants" || parts[2] != "pages" {
		return Route{}, false
	}
	tid, ok := canonicalID(parts[1])
	if !ok {
		return Route{}, false
	}
	rt := Route{TenantID: tid, View: "pages"}
	if len(parts) == 3 {
		return rt, true
	}
	if len(parts) < 5 || len(parts) > 6 {
		return Route{}, false
	}
	if parts[3] == "new" {
		rt.NewPage = true
	} else {
		rt.PageID, ok = canonicalID(parts[3])
		if !ok {
			return Route{}, false
		}
	}
	switch parts[4] {
	case "content", "appearance", "publishing", "html", "preview":
		rt.View = parts[4]
	default:
		return Route{}, false
	}
	if len(parts) == 6 {
		if rt.View != "content" {
			return Route{}, false
		}
		n, valid := canonicalID(parts[5])
		if !valid || n > 10000 {
			return Route{}, false
		}
		rt.Section = int(n)
	}
	return rt, true
}
func canonicalID(value string) (int64, bool) {
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0 && strconv.FormatInt(id, 10) == value
}

// Handler defaults to content editing. Platform controls require an explicit
// Platform mode mount; they are absent from the editor document entirely.
func Handler() http.Handler { return HandlerWithOptions(Options{Mode: Editor, BasePath: "/admin"}) }
func HandlerWithOptions(options Options) http.Handler {
	if options.Mode == "" {
		options.Mode = Editor
	}
	if options.Mode != Editor && options.Mode != Platform {
		panic("invalid CMS UI mode")
	}
	options.BasePath = strings.TrimSuffix(options.BasePath, "/")
	if options.BasePath == "" || !strings.HasPrefix(options.BasePath, "/") || strings.HasPrefix(options.BasePath, "//") || strings.ContainsAny(options.BasePath, "?\\#\"<>") {
		panic("invalid CMS UI base path")
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	name := "index.html"
	if options.Mode == Platform {
		name = "platform.html"
	}
	raw, err := fs.ReadFile(sub, name)
	if err != nil {
		panic(err)
	}
	index := template.Must(template.New(name).Parse(string(raw)))
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(405)
			return
		}
		switch r.URL.Path {
		case "/admin.css", "/admin.js", "/purify.min.js":
			files.ServeHTTP(w, r)
			return
		}
		if _, ok := ParseRoute(options.Mode, r.URL.Path); !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method != http.MethodHead {
			_ = index.Execute(w, options)
		}
	})
}
