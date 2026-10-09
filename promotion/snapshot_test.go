package promotion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

func fixture(t *testing.T) (Snapshot, string, *store.MemoryPageStore, *store.Page) {
	t.Helper()
	root := t.TempDir()
	write := func(p, s string) {
		name := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("assets/site.css", "body{color:#111;background:#fff}")
	write("assets/photo.jpg", "owned-photo-bytes")
	write("cms/templates/main.html", `<!doctype html><link rel="stylesheet" href="/assets/site.css"><main><!--cms:body--></main>`)
	pages := store.NewMemoryPageStore()
	p := &store.Page{Path: "/", Title: "Approved home", BodyHTML: `<img src="/assets/photo.jpg" alt="Portrait"><a href="/">Home</a>`, Status: store.StatusPublished, TemplateID: "main"}
	if err := pages.Create(context.Background(), 10, p); err != nil {
		t.Fatal(err)
	}
	s, err := Export(context.Background(), pages, 10, "test-review", []int64{p.ID}, root, time.Date(2026, 10, 8, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return s, root, pages, p
}

func TestSnapshotFreezeExplicitMappingAndDryRun(t *testing.T) {
	s, root, pages, review := fixture(t)
	b, _ := json.Marshal(s)
	decoded, err := Decode(bytes.NewReader(b))
	if err != nil || decoded.Digest != s.Digest {
		t.Fatal("strict roundtrip failed")
	}
	for _, needle := range []string{`"tenant_id"`, `"password"`, `"domains"`, `"editors"`, `"repository"`, `"token"`} {
		if bytes.Contains(b, []byte(needle)) {
			t.Fatal("authority field leaked into snapshot")
		}
	}
	review.Title = "Later unapproved draft"
	if err := pages.Update(context.Background(), 10, review); err != nil {
		t.Fatal(err)
	}
	if s.Pages[0].Content.Title == review.Title {
		t.Fatal("export was not frozen")
	}
	target := &store.Page{Path: "/", Title: "Old live home", Status: store.StatusPublished}
	if err := pages.Create(context.Background(), 20, target); err != nil {
		t.Fatal(err)
	}
	keep := &store.Page{Path: "/keep", Title: "Keep", Status: store.StatusDraft}
	if err := pages.Create(context.Background(), 20, keep); err != nil {
		t.Fatal(err)
	}
	current, _ := pages.ReadPageState(context.Background(), 20)
	mappings, err := SuggestedMapping(s, current)
	if err != nil || mappings[0].TargetID != target.ID || target.ID == s.Pages[0].SourceID {
		t.Fatal("path suggestion/mapping failed")
	}
	plan, err := DryRun(s, current, mappings, nil, s.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Batch.Mutations) != 1 || len(plan.Batch.Baseline) != 2 || plan.Diffs[0].Action != "update" {
		t.Fatal("dry run omitted baseline or updated omission")
	}
	before, _ := pages.Get(context.Background(), 20, target.ID)
	if before.Title != target.Title {
		t.Fatal("dry run wrote content")
	}
	receipt, err := pages.ApplyPageBatch(context.Background(), 20, plan.Batch)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := pages.Get(context.Background(), 20, target.ID)
	if got.Title != s.Pages[0].Content.Title {
		t.Fatal("apply used unapproved source edit")
	}
	if err := VerifyBundle(root, s.Bundle, []store.PageContent{store.ContentOf(got)}); err != nil {
		t.Fatal(err)
	}
	if _, err := pages.RollbackPageBatch(context.Background(), 20, receipt); err != nil {
		t.Fatal(err)
	}
	move := s
	move.Pages = append([]SnapshotPage(nil), s.Pages...)
	move.Pages[0].Content.Path = "/moved"
	move.Digest = move.ContentDigest()
	if _, err := DryRun(move, current, mappings, nil, s.Bundle); !errors.Is(err, ErrMapping) {
		t.Fatal("implicit move accepted")
	}
	if _, err := DryRun(s, current, nil, nil, s.Bundle); !errors.Is(err, ErrMapping) {
		t.Fatal("implicit target mapping accepted")
	}
}

func TestDecodeRejectsAuthorityUnknownDuplicateAndTrailingWithoutValues(t *testing.T) {
	s, _, _, _ := fixture(t)
	b, _ := json.Marshal(s)
	base := string(b)
	for _, payload := range []string{strings.Replace(base, `"schema":1`, `"schema":1,"credentials":"rejected_marker"`, 1), strings.Replace(base, `"schema":1`, `"schema":1,"schema":1`, 1), base + base, strings.Replace(base, `"title":"Approved home"`, `"title":"Approved home","domains":["rejected_marker"]`, 1), strings.Replace(base, `"size":`, `"extra":"rejected_marker","size":`, 1)} {
		_, err := Decode(strings.NewReader(payload))
		if err != ErrInvalid || strings.Contains(err.Error(), "rejected_marker") {
			t.Fatal("strict decoder leaked or accepted invalid payload")
		}
	}
}

func TestBundleHashesReferencesStaticCollisionsAndSymlinks(t *testing.T) {
	s, root, _, _ := fixture(t)
	contents := []store.PageContent{s.Pages[0].Content}
	if err := os.WriteFile(filepath.Join(root, "assets/photo.jpg"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if VerifyBundle(root, s.Bundle, contents) != ErrBundle {
		t.Fatal("changed bundle bytes accepted")
	}
	manifest, _ := InventoryBundle(root)
	for _, ref := range []string{`<img src="/media/10/hash.jpg">`, `<img src="https://example.com/media/10/hash.jpg">`, `<img src="/assets/missing.jpg">`, `<img src="/api/v1/admin/tenants/10/pages/assets/photo.jpg">`, `<a href="https://test-review.preview.example.com/">Review</a>`} {
		p := contents[0]
		p.BodyHTML = ref
		if VerifyBundle(root, manifest, []store.PageContent{p}) != ErrBundle {
			t.Fatal("unsupported reference accepted")
		}
	}
	for _, ref := range []string{"https://admin.gocodealone.tech/api/v1/admin/tenants/10/pages", "https://example.com/admin", "https://example.com/cms/templates/main.html", "https://example.com/%61pi/private", "https://example.com/assets/../api/private", "https://example.com/assets/%2e%2e/media/10/hash.jpg"} {
		p := contents[0]
		p.BodyHTML = `<a href="` + ref + `">Private dependency</a>`
		if VerifyBundle(root, manifest, []store.PageContent{p}) != ErrBundle {
			t.Fatal("absolute private path accepted")
		}
	}
	p := contents[0]
	p.BodyBlocks = json.RawMessage(`{"type":"doc","url":"\u002fmedia/10/hash.jpg"}`)
	if VerifyBundle(root, manifest, []store.PageContent{p}) != ErrBundle {
		t.Fatal("escaped upload reference accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("Static shadow"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, _ = InventoryBundle(root)
	if VerifyBundle(root, manifest, contents) != ErrBundle {
		t.Fatal("static homepage shadow accepted")
	}
	if err := os.Remove(filepath.Join(root, "index.html")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "assets/photo.jpg"), filepath.Join(root, "assets/alias.jpg")); err != nil {
		t.Fatal(err)
	}
	if _, err := InventoryBundle(root); err != ErrBundle {
		t.Fatal("bundle symlink accepted")
	}
}

func TestCanonicalBlockLinksUseActualRendererSemantics(t *testing.T) {
	s, root, pages, home := fixture(t)
	teaching := &store.Page{Path: "/teaching", Title: "Teaching", Status: store.StatusDraft, TemplateID: "main"}
	if pages.Create(context.Background(), 10, teaching) != nil {
		t.Fatal("teaching fixture failed")
	}
	links := []any{}
	for _, href := range []string{"/teaching", "teaching", "#voice", "mailto:tina@example.com", "https://example.com/music"} {
		links = append(links, map[string]any{"type": "link", "attrs": map[string]any{"href": href}, "content": []any{map[string]any{"type": "text", "text": "Navigation"}}})
	}
	links = append(links, map[string]any{"type": "text", "text": "/this is ordinary text, not an asset"})
	home.BodyBlocks, _ = json.Marshal(map[string]any{"type": "doc", "content": links})
	if pages.Update(context.Background(), 10, home) != nil {
		t.Fatal("blocks update failed")
	}
	if _, err := Export(context.Background(), pages, 10, "test-review", []int64{home.ID, teaching.ID}, root, s.CreatedAt); err != nil {
		t.Fatalf("canonical link export refused: %v", err)
	}
	for _, href := range []string{"/media/10/hash.jpg", "https://admin.gocodealone.tech/api/private", "javascript:alert(1)"} {
		home.BodyBlocks, _ = json.Marshal(map[string]any{"type": "doc", "content": []any{map[string]any{"type": "link", "attrs": map[string]any{"href": href}}}})
		if pages.Update(context.Background(), 10, home) != nil {
			t.Fatal("unsafe fixture failed")
		}
		if _, err := Export(context.Background(), pages, 10, "test-review", []int64{home.ID, teaching.ID}, root, s.CreatedAt); err != ErrBundle {
			t.Fatal("canonical private reference accepted")
		}
	}
}

func TestBundleHTMLResourceSurfacesRefusePrivateAndNestedDocuments(t *testing.T) {
	cases := []struct {
		name string
		html string
	}{
		{"iframe-srcdoc-upload", `<iframe srcdoc="&lt;img src='https://example.com/media/10/hash.jpg'&gt;"></iframe>`},
		{"iframe-srcdoc-api", `<iframe srcdoc="&lt;img src='https://admin.gocodealone.tech/api/private'&gt;"></iframe>`},
		{"iframe-srcdoc-nested", `<iframe srcdoc="&lt;iframe srcdoc=&quot;&amp;lt;img src='https://example.com/admin'&amp;gt;&quot;&gt;&lt;/iframe&gt;"></iframe>`},
		{"iframe-srcdoc-public-unsupported", `<iframe srcdoc="&lt;p&gt;Public nested document&lt;/p&gt;"></iframe>`},
		{"object-upload", `<object data="https://example.com/media/10/hash.jpg"></object>`},
		{"object-api", `<object data="https://admin.gocodealone.tech/api/private"></object>`},
		{"object-bundle-unsupported", `<object data="/assets/photo.jpg"></object>`},
		{"embed-admin", `<embed src="https://example.com/admin/private">`},
		{"formaction-upload", `<form action="https://example.com/contact"><button formaction="https://example.com/media/10/hash.jpg">Submit</button></form>`},
		{"formaction-api", `<input type="submit" formaction="https://admin.gocodealone.tech/api/private">`},
		{"formaction-admin", `<button formaction="https://example.com/admin">Submit</button>`},
		{"formaction-encoded-api", `<button formaction="https://example.com/%61pi/private">Submit</button>`},
		{"formaction-relative-upload", `<button formaction="/media/10/hash.jpg">Submit</button>`},
		{"formaction-missing-local", `<button formaction="/assets/missing.html">Submit</button>`},
		{"meta-refresh-unsupported", `<meta http-equiv=" Refresh " content="0; url=https://example.com/api/private">`},
		{"svg-xml-base", `<svg xml:base="https://example.com/media/10/"><image href="/assets/photo.jpg"></image></svg>`},
		{"imagesrcset-upload", `<link rel="preload" as="image" imagesrcset="/assets/photo.jpg 1x, https://example.com/media/10/hash.jpg 2x">`},
		{"ping-api", `<a href="/" ping="https://example.com/audit https://example.com/api/private">Home</a>`},
		{"legacy-background-upload", `<table background="https://example.com/media/10/hash.jpg"></table>`},
		{"document-manifest-admin", `<html manifest="https://example.com/admin/private"></html>`},
		{"longdesc-api", `<img src="/assets/photo.jpg" longdesc="https://example.com/api/private">`},
		{"cite-admin", `<blockquote cite="https://example.com/admin/private">Text</blockquote>`},
	}
	for _, surface := range []string{"page-body", "cms-template", "static-html"} {
		t.Run(surface, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					s, root, _, _ := fixture(t)
					p := s.Pages[0].Content
					switch surface {
					case "page-body":
						p.BodyHTML = tc.html
					case "cms-template":
						if err := os.WriteFile(filepath.Join(root, "cms/templates/main.html"), []byte(tc.html+`<main><!--cms:body--></main>`), 0600); err != nil {
							t.Fatal(err)
						}
					case "static-html":
						if err := os.WriteFile(filepath.Join(root, "resource.html"), []byte(tc.html), 0600); err != nil {
							t.Fatal(err)
						}
					}
					manifest, err := InventoryBundle(root)
					if err != nil {
						t.Fatal(err)
					}
					if err := VerifyBundle(root, manifest, []store.PageContent{p}); err != ErrBundle {
						t.Fatalf("unsupported HTML resource surface accepted: %v", err)
					}
				})
			}
		})
	}
}

func TestBundleSupportedEmbedsAndResourceOverridesRemainUsable(t *testing.T) {
	s, root, _, _ := fixture(t)
	p := s.Pages[0].Content
	p.BodyHTML = `<meta charset="utf-8">
<iframe src="https://www.youtube.com/embed/public-video" title="Public performance"></iframe>
<form action="https://example.com/contact"><button formaction="https://example.com/contact/voice">Submit</button></form>
<button formaction="/assets/submit.html">Local submit</button>
<img src="/assets/photo.jpg" srcset="/assets/photo.jpg 1x, https://example.com/photos/owned.jpg 2x" longdesc="/">
<link rel="preload" as="image" imagesrcset="/assets/photo.jpg 1x">
<a href="/" ping="https://example.com/audit https://example.com/audit/second">Home</a>
<blockquote cite="https://example.com/music">Text</blockquote>`
	if err := os.WriteFile(filepath.Join(root, "assets/submit.html"), []byte(`<p>Submission endpoint fixture</p>`), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := InventoryBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyBundle(root, manifest, []store.PageContent{p}); err != nil {
		t.Fatalf("supported public embed or validated resource override refused: %v", err)
	}
}

func TestSelectedMediaRouteLinksPreserveUploadAndPrivateRefusal(t *testing.T) {
	cases := []struct {
		name     string
		html     string
		selected bool
		allowed  bool
	}{
		{"selected-relative-link", `<a href="/media">Media</a>`, true, true},
		{"selected-relative-path", `<a href="media">Media</a>`, true, true},
		{"selected-link-query-anchor", `<a href="/media?view=photos#portraits">Media</a>`, true, true},
		{"unselected-route-link", `<a href="/media">Media</a>`, false, false},
		{"unselected-relative-path", `<a href="media">Media</a>`, false, false},
		{"encoded-media-index", `<a href="/%6dedia">Media</a>`, true, false},
		{"dot-media-index", `<a href="/assets/../media">Media</a>`, true, false},
		{"encoded-dot-media-index", `<a href="/assets/%2e%2e/media">Media</a>`, true, false},
		{"root-dot-media-index", `<a href="/./media">Media</a>`, true, false},
		{"relative-dot-media-index", `<a href="./media">Media</a>`, true, false},
		{"trailing-slash-media-index", `<a href="/media/">Media</a>`, true, false},
		{"selected-route-image-source", `<img src="/media">`, true, false},
		{"selected-route-stylesheet-resource", `<link rel="stylesheet" href="/media">`, true, false},
		{"selected-route-svg-image-resource", `<svg><image href="/media"></image></svg>`, true, false},
		{"selected-route-svg-use-resource", `<svg><use href="/media"></use></svg>`, true, false},
		{"selected-route-description-reference", `<img src="/assets/photo.jpg" longdesc="/media">`, true, false},
		{"selected-route-iframe-source", `<iframe src="/media"></iframe>`, true, false},
		{"selected-route-form-action", `<form action="/media"></form>`, true, false},
		{"selected-route-form-override", `<button formaction="/media">Submit</button>`, true, false},
		{"selected-route-source-set", `<img srcset="/media 1x">`, true, false},
		{"selected-route-style-resource", `<p style="background:url(/media)">Text</p>`, true, false},
		{"uploaded-child-link", `<a href="/media/10/hash.jpg">Upload</a>`, true, false},
		{"uploaded-child-source", `<img src="/media/10/hash.jpg">`, true, false},
		{"encoded-upload-child", `<a href="/%6dedia/10/hash.jpg">Upload</a>`, true, false},
		{"encoded-upload-separator", `<a href="/media%2f10/hash.jpg">Upload</a>`, true, false},
		{"dot-upload-child", `<a href="/assets/../media/10/hash.jpg">Upload</a>`, true, false},
		{"encoded-dot-upload-child", `<a href="/assets/%2e%2e/media/10/hash.jpg">Upload</a>`, true, false},
		{"absolute-media-index", `<a href="https://example.com/media">Private</a>`, true, false},
		{"absolute-media-upload", `<a href="https://example.com/media/10/hash.jpg">Private</a>`, true, false},
		{"protocol-relative-media", `<a href="//example.com/media">Private</a>`, true, false},
		{"api-namespace-link", `<a href="/api">Private</a>`, true, false},
		{"admin-namespace-link", `<a href="/admin">Private</a>`, true, false},
		{"cms-namespace-link", `<a href="/cms">Private</a>`, true, false},
	}
	for _, surface := range []string{"page-body", "cms-template", "static-html"} {
		t.Run(surface, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					s, root, _, _ := fixture(t)
					p := s.Pages[0].Content
					switch surface {
					case "page-body":
						p.BodyHTML = tc.html
					case "cms-template":
						if err := os.WriteFile(filepath.Join(root, "cms/templates/main.html"), []byte(tc.html+`<main><!--cms:body--></main>`), 0600); err != nil {
							t.Fatal(err)
						}
					case "static-html":
						if err := os.WriteFile(filepath.Join(root, "resource.html"), []byte(tc.html), 0600); err != nil {
							t.Fatal(err)
						}
					}
					contents := []store.PageContent{p}
					if tc.selected {
						media := s.Pages[0].Content
						media.Path = "/media"
						media.BodyHTML = "<p>Public media page.</p>"
						contents = append(contents, media)
					}
					manifest, err := InventoryBundle(root)
					if err != nil {
						t.Fatal(err)
					}
					err = VerifyBundle(root, manifest, contents)
					if tc.allowed && err != nil || !tc.allowed && err != ErrBundle {
						t.Fatalf("selected media route policy mismatch: %v", err)
					}
				})
			}
		})
	}
	// Canonical block hrefs and raw HTML must share the same route policy.
	for _, selected := range []bool{false, true} {
		for _, href := range []string{"/media", "media", "/%6dedia", "/assets/../media", "/assets/%2e%2e/media", "/./media", "./media", "/media/", "/media/10/hash.jpg", "/%6dedia/10/hash.jpg", "https://example.com/media", "/api"} {
			t.Run("canonical-"+href+"-selected-"+strconv.FormatBool(selected), func(t *testing.T) {
				s, root, _, _ := fixture(t)
				p := s.Pages[0].Content
				p.BodyBlocks, _ = json.Marshal(map[string]any{"type": "doc", "content": []any{map[string]any{"type": "link", "attrs": map[string]any{"href": href}, "content": []any{map[string]any{"type": "text", "text": "Media"}}}}})
				contents := []store.PageContent{p}
				if selected {
					media := s.Pages[0].Content
					media.Path = "/media"
					media.BodyHTML = "<p>Public media page.</p>"
					contents = append(contents, media)
				}
				allowed := selected && (href == "/media" || href == "media")
				err := VerifyBundle(root, s.Bundle, contents)
				if allowed && err != nil || !allowed && err != ErrBundle {
					t.Fatalf("canonical media route policy mismatch: %v", err)
				}
			})
		}
	}
}
