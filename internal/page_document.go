package internal

import (
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

const cmsBodySlot = "<!--cms:body-->"

// PageTemplate is the shell used to wrap a CMS page body.
type PageTemplate struct {
	ID   string
	HTML string
}

// RenderPageDocument renders a public CMS page. It returns rendered=false for
// non-public states so callers can continue to fallback routing without
// exposing draft or future content.
func RenderPageDocument(page *store.Page, template PageTemplate, now time.Time) (string, bool, error) {
	if !PagePubliclyRenderable(page, now) {
		return "", false, nil
	}
	body, err := renderPageBody(page)
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(template.HTML) == "" {
		return body, true, nil
	}
	if strings.Contains(template.HTML, cmsBodySlot) {
		return strings.Replace(template.HTML, cmsBodySlot, body, 1), true, nil
	}
	return template.HTML + body, true, nil
}

// PagePubliclyRenderable reports whether a page should be served publicly at
// now. Admin preview flows should use a separate path and explicit authz.
func PagePubliclyRenderable(page *store.Page, now time.Time) bool {
	if page == nil {
		return false
	}
	if page.Status != store.StatusPublished && page.Status != store.StatusScheduled {
		return false
	}
	if page.PublishAt != nil && now.Before(page.PublishAt.UTC()) {
		return false
	}
	if page.Status == store.StatusScheduled && page.PublishAt == nil {
		return false
	}
	if page.UnpublishAt != nil && !now.Before(page.UnpublishAt.UTC()) {
		return false
	}
	return true
}

func renderPageBody(page *store.Page) (string, error) {
	if len(page.BodyBlocks) > 0 {
		body, err := renderBlockDocument(page.BodyBlocks)
		if err != nil {
			return "", err
		}
		return body, nil
	}
	return page.BodyHTML, nil
}

type blockNode struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Attrs   blockAttrs      `json:"attrs"`
	Content []blockNode     `json:"content"`
	HTML    json.RawMessage `json:"html"`
}

type blockAttrs struct {
	Level int    `json:"level"`
	Href  string `json:"href"`
}

func renderBlockDocument(raw json.RawMessage) (string, error) {
	var root blockNode
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", fmt.Errorf("render page blocks: %w", err)
	}
	if root.Type == "" && root.Text == "" && len(root.Content) == 0 {
		return "", nil
	}
	return renderBlockNode(root), nil
}

func renderBlockNode(node blockNode) string {
	switch node.Type {
	case "doc":
		return renderInlineNodes(node.Content)
	case "paragraph":
		return "<p>" + renderInlineNodes(node.Content) + "</p>"
	case "heading":
		level := node.Attrs.Level
		if level < 1 || level > 6 {
			level = 2
		}
		tag := "h" + strconv.Itoa(level)
		return "<" + tag + ">" + renderInlineNodes(node.Content) + "</" + tag + ">"
	case "text":
		return html.EscapeString(node.Text)
	case "hardBreak":
		return "<br>"
	case "bulletList":
		return "<ul>" + renderInlineNodes(node.Content) + "</ul>"
	case "orderedList":
		return "<ol>" + renderInlineNodes(node.Content) + "</ol>"
	case "listItem":
		return "<li>" + renderInlineNodes(node.Content) + "</li>"
	case "blockquote":
		return "<blockquote>" + renderInlineNodes(node.Content) + "</blockquote>"
	case "link":
		href := html.EscapeString(node.Attrs.Href)
		if href == "" {
			return renderInlineNodes(node.Content)
		}
		return `<a href="` + href + `">` + renderInlineNodes(node.Content) + "</a>"
	default:
		return renderInlineNodes(node.Content)
	}
}

func renderInlineNodes(nodes []blockNode) string {
	var out strings.Builder
	for _, node := range nodes {
		out.WriteString(renderBlockNode(node))
	}
	return out.String()
}
