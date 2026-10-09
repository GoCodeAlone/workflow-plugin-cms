package promotion

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

func TestBundleInspectsDocumentsAsActuallyServed(t *testing.T) {
	for _, tc := range []struct{ name, content, mediaType string }{
		{"resource", `<!doctype html><img src="/media/234/photo.jpg">`, "text/html"},
		{"resource.unknown-extension", `<html><img src="/media/234/photo.jpg"></html>`, "text/html"},
		{"resource.svg", `<svg xmlns="http://www.w3.org/2000/svg"><image href="/media/234/photo.jpg"/></svg>`, "image/svg+xml"},
		{"resource.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><img src="/media/234/photo.jpg"/></html>`, "application/xhtml+xml"},
		{"resource-xml", `<?xml version="1.0"?><svg><image href="/media/234/photo.jpg"/></svg>`, "text/xml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			http.ServeContent(response, httptest.NewRequest("GET", "/"+tc.name, nil), tc.name, time.Time{}, strings.NewReader(tc.content))
			if !strings.HasPrefix(response.Header().Get("Content-Type"), tc.mediaType) {
				t.Fatal("expected served document classification missing")
			}
			s, root, _, _ := fixture(t)
			p := s.Pages[0].Content
			p.BodyHTML = `<iframe src="/` + tc.name + `"></iframe>`
			writeAdmissionFile(t, root, tc.name, tc.content)
			m, err := InventoryBundle(root)
			if err != nil {
				t.Fatal(err)
			}
			if VerifyBundle(root, m, []store.PageContent{p}) != ErrBundle {
				t.Fatal("unchecked served document admitted")
			}
		})
	}
}

func TestBundleLinksRespectSubsiteAndRootFallback(t *testing.T) {
	for _, destination := range []string{"/beta-only", "/media"} {
		for _, surface := range []string{"body", "blocks", "template"} {
			for _, targetSubsite := range []string{"beta", "alpha", ""} {
				t.Run(destination+"/"+surface+"/"+targetSubsite, func(t *testing.T) {
					s, root, _, _ := fixture(t)
					source := s.Pages[0].Content
					source.Path, source.Subsite, source.BodyHTML = "/alpha-home", "alpha", "<p>Alpha.</p>"
					link := `<a href="` + destination + `">Destination</a>`
					target := source
					target.Path, target.Subsite = destination, targetSubsite
					target.TemplateID = ""
					switch surface {
					case "body":
						source.BodyHTML = link
					case "blocks":
						source.BodyBlocks = []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"link","attrs":{"href":"` + destination + `"},"content":[{"type":"text","text":"Destination"}]}]}]}`)
					case "template":
						writeAdmissionFile(t, root, "cms/templates/main.html", link+`<!--cms:body-->`)
					}
					m, err := InventoryBundle(root)
					if err != nil {
						t.Fatal(err)
					}
					err = VerifyBundle(root, m, []store.PageContent{source, target})
					if (err == nil) != (targetSubsite != "beta") {
						t.Fatalf("cross-subsite link admission mismatch: %v", err)
					}
				})
			}
		}
	}
}

func writeAdmissionFile(t *testing.T, root, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestBundleRefusesAlternateBrowserResourceSyntax(t *testing.T) {
	for _, markup := range []string{
		`<p style="background:u\72l(/media/234/photo.jpg)">Text</p>`,
		`<style>body{background:image-set("/media/234/photo.jpg" 1x)}</style>`,
		`<style>body{background:-webkit-image-set("/media/234/photo.jpg" 1x)}</style>`,
		`<style>body{background:image("/media/234/photo.jpg")}</style>`,
		`<style>body{background:url(" /media/234/photo.jpg ")}</style>`,
		`<style>@import/**/"/media/234/photo.jpg";</style>`,
		`<noscript><img src="/media/234/photo.jpg"></noscript>`,
	} {
		for _, surface := range []string{"body", "selected-shell", "unselected-shell", "static.HTML"} {
			t.Run(surface+"/"+markup, func(t *testing.T) {
				s, root, _, _ := fixture(t)
				p := s.Pages[0].Content
				switch surface {
				case "body":
					p.BodyHTML = markup
				case "selected-shell":
					writeAdmissionFile(t, root, "cms/templates/main.html", markup+`<!--cms:body-->`)
				case "unselected-shell":
					writeAdmissionFile(t, root, "cms/templates/alternate.html", markup+`<!--cms:body-->`)
				default:
					writeAdmissionFile(t, root, surface, markup)
				}
				m, err := InventoryBundle(root)
				if err != nil {
					t.Fatal(err)
				}
				if VerifyBundle(root, m, []store.PageContent{p}) != ErrBundle {
					t.Fatal("browser-resolved private resource admitted")
				}
			})
		}
	}
}

func TestBundleScansUppercaseStylesheetsAndHTM(t *testing.T) {
	for name, contents := range map[string]string{
		"assets/site.CSS": `body{background:url(/media/234/photo.jpg)}`,
		"fallback.HTM":    `<img src="/media/234/photo.jpg">`,
	} {
		t.Run(name, func(t *testing.T) {
			s, root, _, _ := fixture(t)
			writeAdmissionFile(t, root, name, contents)
			m, err := InventoryBundle(root)
			if err != nil {
				t.Fatal(err)
			}
			if VerifyBundle(root, m, []store.PageContent{s.Pages[0].Content}) != ErrBundle {
				t.Fatal("case or HTML extension bypass admitted")
			}
		})
	}
}

func TestBundleTemplateAdmissionMatchesRuntime(t *testing.T) {
	for _, name := range []string{"Main", "1main", strings.Repeat("a", 65)} {
		t.Run(name, func(t *testing.T) {
			s, root, _, _ := fixture(t)
			p := s.Pages[0].Content
			p.TemplateID = name
			writeAdmissionFile(t, root, "cms/templates/"+name+".html", `<!--cms:body-->`)
			m, err := InventoryBundle(root)
			if err != nil {
				t.Fatal(err)
			}
			if VerifyBundle(root, m, []store.PageContent{p}) != ErrBundle {
				t.Fatal("runtime-invalid template admitted")
			}
		})
	}
	for _, selected := range []bool{true, false} {
		s, root, _, _ := fixture(t)
		name := "alternate"
		if selected {
			name = "main"
		}
		writeAdmissionFile(t, root, "cms/templates/"+name+".html", `<!--cms:body-->`+strings.Repeat(" ", 1<<20))
		m, err := InventoryBundle(root)
		if err != nil {
			t.Fatal(err)
		}
		if VerifyBundle(root, m, []store.PageContent{s.Pages[0].Content}) != ErrBundle {
			t.Fatal("oversized shell admitted")
		}
	}
}

func TestBundleUnselectedShellRequiresKnownReferenceBase(t *testing.T) {
	for _, ref := range []string{"assets/photo.jpg", "../assets/photo.jpg", "/assets/photo.jpg"} {
		s, root, _, _ := fixture(t)
		writeAdmissionFile(t, root, "cms/templates/alternate.html", `<img src="`+ref+`"><!--cms:body-->`)
		m, err := InventoryBundle(root)
		if err != nil {
			t.Fatal(err)
		}
		err = VerifyBundle(root, m, []store.PageContent{s.Pages[0].Content})
		if (err == nil) != strings.HasPrefix(ref, "/") {
			t.Fatalf("unselected shell base admission mismatch: %v", err)
		}
	}
}

func TestBundleRefusesHostReservedPageRoutes(t *testing.T) {
	for _, route := range []string{"/healthz", "/admin", "/admin/voice", "/administrator", "/%61dmin/voice", "/%68ealthz", "/api/v1/ingest/release", "/cms/templates/main", "/media/234/photo.jpg", "/media", "/public"} {
		s, root, _, _ := fixture(t)
		p := s.Pages[0].Content
		p.Path = route
		p.BodyHTML = "<p>Page.</p>"
		err := VerifyBundle(root, s.Bundle, []store.PageContent{p})
		allowed := route == "/media" || route == "/public"
		if (err == nil) != allowed {
			t.Fatalf("reserved route admission mismatch for %s: %v", route, err)
		}
	}
}

func TestRestrictedCSSSupportsOrdinaryReferences(t *testing.T) {
	for _, css := range []string{
		`body{background:URL(" /assets/photo.jpg ");font-family:"A font"}`,
		`@import/**/"/assets/site.css"; body{background:url(/assets/photo.jpg)}`,
		`/* u\72l(/media/private) is inert */body{color:#fff}`,
	} {
		if err := verifyCSSReferences(css, func(ref string) error {
			if ref != "/assets/photo.jpg" && ref != "/assets/site.css" {
				t.Fatal("incorrect resource token")
			}
			return nil
		}); err != nil {
			t.Fatalf("supported CSS refused: %v", err)
		}
	}
}
