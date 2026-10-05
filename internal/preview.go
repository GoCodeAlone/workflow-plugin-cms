package internal

import (
	"bytes"
	"github.com/GoCodeAlone/workflow-plugin-cms/store"
	"golang.org/x/net/html"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *AdminAPI) pageTemplates(w http.ResponseWriter, r *http.Request, id int64) {
	if id <= 0 || a.ListTemplates == nil {
		writeJSONError(w, 404, "not_found", "templates unavailable")
		return
	}
	names, err := a.ListTemplates(r.Context(), id)
	if err != nil {
		writeJSONError(w, 503, "unavailable", "templates unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"templates": names})
}

func (a *AdminAPI) previewPage(w http.ResponseWriter, r *http.Request, id int64) {
	if id <= 0 || a.ResolveTemplate == nil {
		writeJSONError(w, 404, "not_found", "preview unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body pageBody
	if err := decodeJSON(r, &body); err != nil {
		writeJSONError(w, 400, "bad_request", "invalid preview document")
		return
	}
	if strings.TrimSpace(string(body.BodyBlocks)) == "null" {
		body.BodyBlocks = nil
	}
	template, err := a.ResolveTemplate(r.Context(), id, body.TemplateID)
	if err != nil {
		writeJSONError(w, 503, "unavailable", "template unavailable")
		return
	}
	p := &store.Page{TenantID: id, Title: body.Title, Path: body.Path, BodyHTML: body.BodyHTML, BodyBlocks: body.BodyBlocks, Status: store.StatusPublished}
	if err := p.Validate(); err != nil {
		writeJSONError(w, 400, "bad_request", err.Error())
		return
	}
	doc, _, err := RenderPageDocument(p, template, time.Now().UTC())
	if err != nil {
		writeJSONError(w, 400, "bad_request", "invalid page blocks")
		return
	}
	prefix := "/api/v1/admin/tenants/" + strconv.FormatInt(id, 10) + "/pages/assets"
	csp := "default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self'; font-src 'self'; base-uri 'none'; form-action 'none'; frame-src 'none'; script-src 'none'; connect-src 'none'"
	doc = previewAssetPaths(doc, prefix, csp)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", csp+"; sandbox")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(doc))
}

func previewAssetPaths(doc, prefix, csp string) string {
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return ""
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "head" {
			meta := &html.Node{Type: html.ElementNode, Data: "meta", Attr: []html.Attribute{{Key: "http-equiv", Val: "Content-Security-Policy"}, {Key: "content", Val: csp}}}
			n.InsertBefore(meta, n.FirstChild)
		}
		for i, a := range n.Attr {
			if a.Key == "src" || a.Key == "href" {
				if strings.HasPrefix(a.Val, "/assets/") {
					n.Attr[i].Val = prefix + a.Val
				} else if strings.HasPrefix(a.Val, "assets/") {
					n.Attr[i].Val = prefix + "/" + a.Val
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	var b bytes.Buffer
	_ = html.Render(&b, root)
	return b.String()
}
