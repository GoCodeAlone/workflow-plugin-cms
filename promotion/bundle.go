package promotion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	cms "github.com/GoCodeAlone/workflow-plugin-cms/internal"
	"github.com/GoCodeAlone/workflow-plugin-cms/store"
	"golang.org/x/net/html"
)

type BundleFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type BundleManifest struct {
	Files  []BundleFile `json:"files"`
	Digest string       `json:"digest"`
}

func (m BundleManifest) contentDigest() string { m.Digest = ""; return sumJSON(m) }
func cleanFile(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, "../") && !strings.ContainsAny(p, "\\\x00?#") && !strings.Contains(p, "/.") && !strings.HasPrefix(p, ".")
}
func (m BundleManifest) Validate() error {
	if len(m.Files) == 0 || len(m.Files) > 4096 || m.Digest != m.contentDigest() {
		return ErrBundle
	}
	previous := ""
	var total int64
	for _, f := range m.Files {
		b, err := hex.DecodeString(f.SHA256)
		if !cleanFile(f.Path) || f.Path <= previous || f.Size < 0 || f.Size > 64<<20 || err != nil || len(b) != sha256.Size || strings.ToLower(f.SHA256) != f.SHA256 {
			return ErrBundle
		}
		previous = f.Path
		total += f.Size
		if total > 64<<20 {
			return ErrBundle
		}
	}
	return nil
}

// InventoryBundle hashes exact regular files only. Call with an immutable
// version directory, not a current symlink. No file content is logged.
func InventoryBundle(root string) (BundleManifest, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return BundleManifest{}, ErrBundle
	}
	m := BundleManifest{Files: []BundleFile{}}
	var total int64
	err = filepath.WalkDir(root, func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.Type()&os.ModeSymlink != 0 {
			return ErrBundle
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
			return ErrBundle
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return ErrBundle
		}
		rel = filepath.ToSlash(rel)
		if !cleanFile(rel) {
			return ErrBundle
		}
		total += info.Size()
		if total > 64<<20 || len(m.Files) >= 4096 {
			return ErrBundle
		}
		file, err := os.Open(name)
		if err != nil {
			return ErrBundle
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(file, 64<<20+1))
		closeErr := file.Close()
		if err != nil || closeErr != nil || n != info.Size() {
			return ErrBundle
		}
		m.Files = append(m.Files, BundleFile{rel, hex.EncodeToString(h.Sum(nil)), n})
		return nil
	})
	if err != nil {
		return BundleManifest{}, ErrBundle
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	m.Digest = m.contentDigest()
	if err = m.Validate(); err != nil {
		return BundleManifest{}, err
	}
	return m, nil
}

var cssReferences = regexp.MustCompile(`(?i)url\(\s*["']?([^\s"')]+)["']?\s*\)|@import\s+["']([^"']+)["']`)

// VerifyBundle re-hashes the supplied directory, validates local references and
// rejects exact static route shadowing. Uploaded tenant media and admin paths
// remain unsupported; copying their URLs would leak review tenant identity.
func VerifyBundle(root string, manifest BundleManifest, pages []store.PageContent) error {
	actual, err := InventoryBundle(root)
	if err != nil || manifest.Validate() != nil || sumJSON(actual) != sumJSON(manifest) {
		return ErrBundle
	}
	files := map[string]bool{}
	routes := map[string]bool{}
	for _, f := range manifest.Files {
		files["/"+f.Path] = true
	}
	for _, p := range pages {
		if p.Validate() != nil {
			return ErrBundle
		}
		routes[p.Path] = true
		exact := strings.TrimPrefix(p.Path, "/")
		if exact == "" {
			exact = "index.html"
		}
		if files["/"+exact] {
			return ErrBundle
		}
		if p.TemplateID != "" && (!cleanFile(p.TemplateID) || strings.Contains(p.TemplateID, "/") || !files["/cms/templates/"+p.TemplateID+".html"]) {
			return ErrBundle
		}
	}
	checkRef := func(raw, base string, link bool) error {
		if raw == "" || strings.HasPrefix(raw, "#") {
			return nil
		}
		u, err := url.Parse(raw)
		if err != nil || u.User != nil {
			return ErrBundle
		}
		if strings.ContainsAny(u.Path, "\\\x00") {
			return ErrBundle
		}
		resolved := path.Clean(u.Path)
		if u.Scheme == "" && !strings.HasPrefix(u.Path, "/") {
			resolved = path.Join(path.Dir(base), u.Path)
		}
		if forbiddenPath(resolved) {
			return ErrBundle
		}
		if u.Scheme != "" {
			if link && (u.Scheme == "mailto" || u.Scheme == "tel") {
				return nil
			}
			if u.Scheme != "https" || u.Host == "" || strings.Contains(strings.ToLower(u.Hostname()), "preview.") || strings.Contains(strings.ToLower(u.Hostname()), "-review.") {
				return ErrBundle
			}
			return nil
		}
		if u.Host != "" {
			return ErrBundle
		}
		if files[resolved] || link && routes[resolved] {
			return nil
		}
		return ErrBundle
	}
	checkCSS := func(css, base string) error {
		for _, m := range cssReferences.FindAllStringSubmatch(css, -1) {
			ref := m[1]
			if ref == "" {
				ref = m[2]
			}
			if err := checkRef(ref, base, false); err != nil {
				return err
			}
		}
		return nil
	}
	checkHTML := func(source, base string) error {
		doc, err := html.Parse(strings.NewReader(source))
		if err != nil {
			return ErrBundle
		}
		var visit func(*html.Node) error
		visit = func(n *html.Node) error {
			if n.Type == html.ElementNode {
				switch n.Data {
				case "base", "object", "embed":
					// These elements can change reference resolution or load
					// nested/plugin documents outside the checked HTML tree.
					return ErrBundle
				}
			}
			for _, a := range n.Attr {
				switch a.Key {
				case "srcdoc", "xml:base":
					// srcdoc is decoded into an attribute value, not visited
					// children. Nested inline documents and XML reference
					// base overrides are unsupported.
					return ErrBundle
				case "base":
					if a.Namespace == "xml" {
						return ErrBundle
					}
				case "http-equiv":
					if n.Data == "meta" && strings.EqualFold(strings.TrimSpace(a.Val), "refresh") {
						return ErrBundle
					}
				case "href", "src", "poster", "action", "formaction", "background", "manifest":
					if err := checkRef(a.Val, base, a.Key == "href"); err != nil {
						return err
					}
				case "cite", "longdesc":
					if err := checkRef(a.Val, base, true); err != nil {
						return err
					}
				case "ping":
					for _, ref := range strings.Fields(a.Val) {
						if err := checkRef(ref, base, false); err != nil {
							return err
						}
					}
				case "srcset", "imagesrcset":
					for _, ref := range strings.Split(a.Val, ",") {
						parts := strings.Fields(ref)
						if len(parts) == 0 {
							return ErrBundle
						}
						if err := checkRef(parts[0], base, false); err != nil {
							return err
						}
					}
				case "style":
					if err := checkCSS(a.Val, base); err != nil {
						return err
					}
				}
			}
			if n.Type == html.ElementNode && n.Data == "style" {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.TextNode {
						if err := checkCSS(c.Data, base); err != nil {
							return err
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if err := visit(c); err != nil {
					return err
				}
			}
			return nil
		}
		return visit(doc)
	}
	for _, p := range pages {
		canonical, err := cms.RenderPageBody(p.Page(1, 1, 1))
		if err != nil {
			return ErrBundle
		}
		if err = checkHTML(canonical, p.Path); err != nil {
			return err
		}
		if len(p.BodyBlocks) > 0 {
			var blocks any
			if json.Unmarshal(p.BodyBlocks, &blocks) != nil {
				return ErrBundle
			}
			var checkBlock func(any) error
			checkBlock = func(value any) error {
				switch v := value.(type) {
				case []any:
					for _, item := range v {
						if err := checkBlock(item); err != nil {
							return err
						}
					}
				case map[string]any:
					for key, item := range v {
						if ref, ok := item.(string); ok {
							switch key {
							case "href", "src", "url", "poster":
								if err := checkRef(ref, p.Path, key == "href"); err != nil {
									return err
								}
							}
						}

						if err := checkBlock(item); err != nil {
							return err
						}
					}
				}
				return nil
			}
			if err := checkBlock(blocks); err != nil {
				return err
			}
		}
		if err := checkHTML(p.BodyHTML, p.Path); err != nil {
			return err
		}
	}
	for _, f := range manifest.Files {
		if strings.HasSuffix(f.Path, ".css") || strings.HasSuffix(f.Path, ".html") {
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
			if err != nil {
				return ErrBundle
			}
			if strings.HasSuffix(f.Path, ".css") {
				if err := checkCSS(string(b), "/"+f.Path); err != nil {
					return err
				}
			} else {
				if strings.HasPrefix(f.Path, "cms/templates/") && strings.Count(string(b), "<!--cms:body-->") != 1 {
					return ErrBundle
				}
				// Shell relative references are interpreted at public page paths.
				if strings.HasPrefix(f.Path, "cms/templates/") {
					for _, p := range pages {
						if p.TemplateID+".html" == path.Base(f.Path) {
							if err := checkHTML(string(b), p.Path); err != nil {
								return err
							}
						}
					}
				} else if err := checkHTML(string(b), "/"+f.Path); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func forbiddenPath(p string) bool {
	for _, prefix := range []string{"/media", "/api", "/admin", "/cms"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}
