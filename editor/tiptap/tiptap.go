// Package tiptap implements the TipTap WYSIWYG editor.Provider for the
// CMS engine.
//
// TipTap is a MIT-licensed ProseMirror-based editor for the JS side. On
// the Go side, we serialize block trees as the canonical JSON shape
// that ProseMirror documents use:
//
//	{
//	  "type": "doc",
//	  "content": [
//	    { "type": "paragraph", "content": [ {"type": "text", "text": "Hi"} ] }
//	  ]
//	}
//
// This implementation handles the common subset: doc / paragraph /
// text / heading / bullet_list / ordered_list / list_item / blockquote.
// Marks supported on text: bold, italic, code, link.
//
// Per gocodealone-multisite SPEC.md V10, V14: deterministic server-side
// render; ⊥ client-side JS fetch unless explicit.
package tiptap

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
)

// Provider implements editor.Provider for TipTap.
type Provider struct{}

// New returns a zero-config TipTap provider.
func New() *Provider { return &Provider{} }

func (p *Provider) Name() string             { return "tiptap" }
func (p *Provider) FrontendBundleID() string { return "tiptap-editor-bundle" }

func (p *Provider) EmptyBlocks() json.RawMessage {
	return json.RawMessage(`{"type":"doc","content":[]}`)
}

// node mirrors the ProseMirror JSON shape.
type node struct {
	Type    string          `json:"type"`
	Attrs   json.RawMessage `json:"attrs,omitempty"`
	Content []node          `json:"content,omitempty"`
	Text    string          `json:"text,omitempty"`
	Marks   []mark          `json:"marks,omitempty"`
}

type mark struct {
	Type  string          `json:"type"`
	Attrs json.RawMessage `json:"attrs,omitempty"`
}

// Validate returns nil iff blocks parses to a doc node with a known top
// type. Deeper validation (child legality) is intentionally permissive;
// Render gracefully handles unknown nodes by skipping them.
func (p *Provider) Validate(blocks json.RawMessage) error {
	if len(blocks) == 0 {
		return fmt.Errorf("empty blocks")
	}
	var n node
	if err := json.Unmarshal(blocks, &n); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if n.Type != "doc" {
		return fmt.Errorf("root must be type=doc, got %q", n.Type)
	}
	return nil
}

// Render walks the document tree and emits HTML.
func (p *Provider) Render(blocks json.RawMessage) (string, bool) {
	if err := p.Validate(blocks); err != nil {
		return "", false
	}
	var n node
	if err := json.Unmarshal(blocks, &n); err != nil {
		return "", false
	}
	var b strings.Builder
	for _, c := range n.Content {
		renderNode(&b, c)
	}
	return b.String(), true
}

func renderNode(b *strings.Builder, n node) {
	switch n.Type {
	case "paragraph":
		b.WriteString("<p>")
		renderChildren(b, n.Content)
		b.WriteString("</p>")

	case "heading":
		level := headingLevel(n.Attrs)
		fmt.Fprintf(b, "<h%d>", level)
		renderChildren(b, n.Content)
		fmt.Fprintf(b, "</h%d>", level)

	case "bullet_list":
		b.WriteString("<ul>")
		renderChildren(b, n.Content)
		b.WriteString("</ul>")

	case "ordered_list":
		b.WriteString("<ol>")
		renderChildren(b, n.Content)
		b.WriteString("</ol>")

	case "list_item":
		b.WriteString("<li>")
		renderChildren(b, n.Content)
		b.WriteString("</li>")

	case "blockquote":
		b.WriteString("<blockquote>")
		renderChildren(b, n.Content)
		b.WriteString("</blockquote>")

	case "text":
		renderText(b, n)

	default:
		// Unknown nodes — skip silently but render their children if any.
		renderChildren(b, n.Content)
	}
}

func renderChildren(b *strings.Builder, nodes []node) {
	for _, n := range nodes {
		renderNode(b, n)
	}
}

func renderText(b *strings.Builder, n node) {
	// Apply marks in a stable order: link wraps everything, then
	// strong (bold) → em (italic) → code.
	text := html.EscapeString(n.Text)

	openInner, closeInner := marksToHTML(n.Marks, false)
	openLink, closeLink := linkMarkToHTML(n.Marks)

	b.WriteString(openLink)
	b.WriteString(openInner)
	b.WriteString(text)
	b.WriteString(closeInner)
	b.WriteString(closeLink)
}

func marksToHTML(marks []mark, _ bool) (open string, close string) {
	var ob, cb strings.Builder
	for _, m := range marks {
		switch m.Type {
		case "bold":
			ob.WriteString("<strong>")
			cb.WriteString("</strong>")
		case "italic":
			ob.WriteString("<em>")
			cb.WriteString("</em>")
		case "code":
			ob.WriteString("<code>")
			cb.WriteString("</code>")
		}
	}
	// close marks in reverse order.
	return ob.String(), reverseHTMLClose(cb.String())
}

func reverseHTMLClose(s string) string {
	// Split close tags + reverse. Tags are simple "</x>" — split on '>'
	// boundary then reassemble in reverse.
	parts := []string{}
	for s != "" {
		i := strings.IndexByte(s, '>')
		if i < 0 {
			parts = append(parts, s)
			break
		}
		parts = append(parts, s[:i+1])
		s = s[i+1:]
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "")
}

func linkMarkToHTML(marks []mark) (open string, close string) {
	for _, m := range marks {
		if m.Type != "link" {
			continue
		}
		var attrs struct {
			Href   string `json:"href"`
			Target string `json:"target,omitempty"`
		}
		_ = json.Unmarshal(m.Attrs, &attrs)
		if attrs.Href == "" {
			continue
		}
		// Defensive: escape href as an attribute.
		href := html.EscapeString(attrs.Href)
		target := ""
		if attrs.Target != "" {
			target = ` target="` + html.EscapeString(attrs.Target) + `" rel="noopener noreferrer"`
		}
		return `<a href="` + href + `"` + target + `>`, `</a>`
	}
	return "", ""
}

func headingLevel(attrs json.RawMessage) int {
	if len(attrs) == 0 {
		return 2
	}
	var a struct {
		Level int `json:"level"`
	}
	_ = json.Unmarshal(attrs, &a)
	if a.Level < 1 || a.Level > 6 {
		return 2
	}
	return a.Level
}
