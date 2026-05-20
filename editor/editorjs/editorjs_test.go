package editorjs

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-cms/editor"
)

// Compile-time assertion that *Provider satisfies editor.Provider — the
// whole point of T23 is proving the swap surface.
var _ editor.Provider = (*Provider)(nil)

func TestProvider_Metadata(t *testing.T) {
	p := New()
	if p.Name() != "editorjs" {
		t.Errorf("name = %q", p.Name())
	}
	if p.FrontendBundleID() != "editorjs" {
		t.Errorf("bundle = %q", p.FrontendBundleID())
	}
}

func TestProvider_RenderParagraph(t *testing.T) {
	p := New()
	in := json.RawMessage(`{"blocks":[{"type":"paragraph","data":{"text":"Hello & welcome"}}]}`)
	got, ok := p.Render(in)
	if !ok {
		t.Fatal("render failed")
	}
	if got != "<p>Hello &amp; welcome</p>" {
		t.Errorf("render = %q", got)
	}
}

func TestProvider_RenderHeader(t *testing.T) {
	p := New()
	in := json.RawMessage(`{"blocks":[{"type":"header","data":{"text":"Section","level":3}}]}`)
	got, _ := p.Render(in)
	if got != "<h3>Section</h3>" {
		t.Errorf("render = %q", got)
	}
}

func TestProvider_RenderListUnordered(t *testing.T) {
	p := New()
	in := json.RawMessage(`{"blocks":[{"type":"list","data":{"style":"unordered","items":["a","b"]}}]}`)
	got, _ := p.Render(in)
	if got != "<ul><li>a</li><li>b</li></ul>" {
		t.Errorf("render = %q", got)
	}
}

func TestProvider_RenderListOrdered(t *testing.T) {
	p := New()
	in := json.RawMessage(`{"blocks":[{"type":"list","data":{"style":"ordered","items":["x"]}}]}`)
	got, _ := p.Render(in)
	if !strings.HasPrefix(got, "<ol>") {
		t.Errorf("ordered list missing <ol>: %q", got)
	}
}

func TestProvider_RenderImageWithCaption(t *testing.T) {
	p := New()
	in := json.RawMessage(`{"blocks":[{"type":"image","data":{"file":{"url":"/m/1/a.png"},"caption":"Cap"}}]}`)
	got, _ := p.Render(in)
	if !strings.Contains(got, `<img src="/m/1/a.png"`) {
		t.Errorf("image src missing: %q", got)
	}
	if !strings.Contains(got, "<figcaption>Cap</figcaption>") {
		t.Errorf("caption missing: %q", got)
	}
}

func TestProvider_RenderEscapesHTML(t *testing.T) {
	p := New()
	in := json.RawMessage(`{"blocks":[{"type":"paragraph","data":{"text":"<script>alert(1)</script>"}}]}`)
	got, _ := p.Render(in)
	if strings.Contains(got, "<script>") {
		t.Errorf("XSS leak: %q", got)
	}
}

func TestProvider_RenderEmpty(t *testing.T) {
	p := New()
	got, _ := p.Render(nil)
	if got != "" {
		t.Errorf("empty render = %q", got)
	}
}

func TestProvider_Validate(t *testing.T) {
	p := New()
	if err := p.Validate(json.RawMessage(`{"blocks":[{"type":"paragraph","data":{}}]}`)); err != nil {
		t.Errorf("valid: %v", err)
	}
	if err := p.Validate(json.RawMessage(`{"blocks":[{"type":"weird","data":{}}]}`)); err == nil {
		t.Error("unknown type should fail Validate")
	}
	if err := p.Validate(json.RawMessage(`not-json`)); err == nil {
		t.Error("bad JSON should fail Validate")
	}
}

func TestProvider_RegistrySwap(t *testing.T) {
	// Prove the provider-swap pattern: register both, lookup by name.
	r := editor.NewRegistry()
	if err := r.Register(New()); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, ok := r.Get("editorjs")
	if !ok || got.Name() != "editorjs" {
		t.Errorf("registry get: ok=%v name=%q", ok, got.Name())
	}
}
