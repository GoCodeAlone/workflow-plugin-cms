// Package editorjs implements the alternative Editor.js WYSIWYG
// provider — example T23 / SPEC V10. Editor.js is a block-style editor
// from codex-team (Apache-2.0).
//
// This implementation handles the canonical Editor.js v2 block shape:
//
//   {
//     "time": 1701000000,
//     "blocks": [
//       {"type":"paragraph","data":{"text":"Hello"}},
//       {"type":"header","data":{"text":"Section","level":2}},
//       {"type":"list","data":{"style":"unordered","items":["a","b"]}},
//       {"type":"image","data":{"file":{"url":"/m/1/a.png"},"caption":"x"}}
//     ],
//     "version": "2.x"
//   }
//
// Rendering is intentionally narrow — Editor.js supports many block
// types via plugins; this covers paragraph + header + list + image.
package editorjs

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
)

// Provider implements editor.Provider for Editor.js.
type Provider struct{}

// New returns a fresh Provider.
func New() *Provider { return &Provider{} }

// Name implements editor.Provider.
func (p *Provider) Name() string { return "editorjs" }

// FrontendBundleID implements editor.Provider.
func (p *Provider) FrontendBundleID() string { return "editorjs" }

// EmptyBlocks implements editor.Provider.
func (p *Provider) EmptyBlocks() json.RawMessage {
	return json.RawMessage(`{"time":0,"blocks":[],"version":"2.x"}`)
}

type document struct {
	Blocks []block `json:"blocks"`
}

type block struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type paragraphData struct {
	Text string `json:"text"`
}

type headerData struct {
	Text  string `json:"text"`
	Level int    `json:"level"`
}

type listData struct {
	Style string   `json:"style"` // "ordered" | "unordered"
	Items []string `json:"items"`
}

type imageData struct {
	File    imageFile `json:"file"`
	Caption string    `json:"caption"`
	Alt     string    `json:"alt"`
}

type imageFile struct {
	URL string `json:"url"`
}

// Render implements editor.Provider.
func (p *Provider) Render(blocks json.RawMessage) (string, bool) {
	if len(blocks) == 0 {
		return "", true
	}
	var doc document
	if err := json.Unmarshal(blocks, &doc); err != nil {
		return "", false
	}
	var b strings.Builder
	for _, blk := range doc.Blocks {
		out, ok := renderBlock(blk)
		if !ok {
			return "", false
		}
		b.WriteString(out)
	}
	return b.String(), true
}

func renderBlock(blk block) (string, bool) {
	switch blk.Type {
	case "paragraph":
		var d paragraphData
		if err := json.Unmarshal(blk.Data, &d); err != nil {
			return "", false
		}
		return "<p>" + html.EscapeString(d.Text) + "</p>", true
	case "header":
		var d headerData
		if err := json.Unmarshal(blk.Data, &d); err != nil {
			return "", false
		}
		level := d.Level
		if level < 1 || level > 6 {
			level = 2
		}
		tag := fmt.Sprintf("h%d", level)
		return "<" + tag + ">" + html.EscapeString(d.Text) + "</" + tag + ">", true
	case "list":
		var d listData
		if err := json.Unmarshal(blk.Data, &d); err != nil {
			return "", false
		}
		tag := "ul"
		if d.Style == "ordered" {
			tag = "ol"
		}
		var b strings.Builder
		b.WriteString("<" + tag + ">")
		for _, item := range d.Items {
			b.WriteString("<li>" + html.EscapeString(item) + "</li>")
		}
		b.WriteString("</" + tag + ">")
		return b.String(), true
	case "image":
		var d imageData
		if err := json.Unmarshal(blk.Data, &d); err != nil {
			return "", false
		}
		alt := d.Alt
		if alt == "" {
			alt = d.Caption
		}
		out := `<figure><img src="` + html.EscapeString(d.File.URL) + `" alt="` + html.EscapeString(alt) + `">`
		if d.Caption != "" {
			out += `<figcaption>` + html.EscapeString(d.Caption) + `</figcaption>`
		}
		out += `</figure>`
		return out, true
	default:
		// Unknown block type — render an empty placeholder. Editor.js
		// is plugin-driven; the CMS engine flags unknown types via
		// Validate (separate call) rather than failing render.
		return "", true
	}
}

// Validate implements editor.Provider.
func (p *Provider) Validate(blocks json.RawMessage) error {
	if len(blocks) == 0 {
		return nil
	}
	var doc document
	if err := json.Unmarshal(blocks, &doc); err != nil {
		return fmt.Errorf("editorjs: invalid document: %w", err)
	}
	allowed := map[string]bool{"paragraph": true, "header": true, "list": true, "image": true}
	for i, blk := range doc.Blocks {
		if blk.Type == "" {
			return fmt.Errorf("editorjs: block %d missing type", i)
		}
		if !allowed[blk.Type] {
			return fmt.Errorf("editorjs: block %d type %q not supported (extend Validate to allowlist)", i, blk.Type)
		}
	}
	return nil
}
