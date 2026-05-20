package tiptap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProvider_Identity(t *testing.T) {
	p := New()
	if p.Name() != "tiptap" {
		t.Errorf("Name: got %q want tiptap", p.Name())
	}
	if p.FrontendBundleID() == "" {
		t.Error("FrontendBundleID should be non-empty")
	}
}

func TestEmptyBlocks_ValidDoc(t *testing.T) {
	p := New()
	b := p.EmptyBlocks()
	if err := p.Validate(b); err != nil {
		t.Errorf("EmptyBlocks should validate: %v", err)
	}
}

func TestValidate_BadInputs(t *testing.T) {
	p := New()
	cases := []struct {
		name string
		in   json.RawMessage
	}{
		{"empty bytes", nil},
		{"empty string", json.RawMessage(``)},
		{"not json", json.RawMessage(`not json`)},
		{"wrong root", json.RawMessage(`{"type":"paragraph","content":[]}`)},
	}
	for _, tc := range cases {
		if err := p.Validate(tc.in); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestRender_Paragraph(t *testing.T) {
	p := New()
	in := json.RawMessage(`{
		"type":"doc",
		"content":[
			{"type":"paragraph","content":[{"type":"text","text":"Hello world"}]}
		]
	}`)
	got, ok := p.Render(in)
	if !ok {
		t.Fatal("Render returned ok=false")
	}
	if got != "<p>Hello world</p>" {
		t.Errorf("got %q want %q", got, "<p>Hello world</p>")
	}
}

func TestRender_Heading(t *testing.T) {
	p := New()
	in := json.RawMessage(`{
		"type":"doc",
		"content":[
			{"type":"heading","attrs":{"level":3},"content":[{"type":"text","text":"Title"}]}
		]
	}`)
	got, _ := p.Render(in)
	if got != "<h3>Title</h3>" {
		t.Errorf("got %q want %q", got, "<h3>Title</h3>")
	}
}

func TestRender_Marks(t *testing.T) {
	p := New()
	in := json.RawMessage(`{
		"type":"doc",
		"content":[
			{"type":"paragraph","content":[
				{"type":"text","marks":[{"type":"bold"}],"text":"strong"},
				{"type":"text","text":" plain "},
				{"type":"text","marks":[{"type":"italic"}],"text":"em"},
				{"type":"text","text":" "},
				{"type":"text","marks":[{"type":"code"}],"text":"code"}
			]}
		]
	}`)
	got, _ := p.Render(in)
	want := "<p><strong>strong</strong> plain <em>em</em> <code>code</code></p>"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestRender_LinkMark(t *testing.T) {
	p := New()
	in := json.RawMessage(`{
		"type":"doc",
		"content":[
			{"type":"paragraph","content":[
				{"type":"text","marks":[{"type":"link","attrs":{"href":"https://example.com"}}],"text":"click"}
			]}
		]
	}`)
	got, _ := p.Render(in)
	if !strings.Contains(got, `<a href="https://example.com">click</a>`) {
		t.Errorf("expected link, got %q", got)
	}
}

func TestRender_LinkTargetBlank(t *testing.T) {
	p := New()
	in := json.RawMessage(`{
		"type":"doc",
		"content":[
			{"type":"paragraph","content":[
				{"type":"text","marks":[{"type":"link","attrs":{"href":"https://example.com","target":"_blank"}}],"text":"x"}
			]}
		]
	}`)
	got, _ := p.Render(in)
	if !strings.Contains(got, `target="_blank"`) {
		t.Errorf("expected target attr, got %q", got)
	}
	if !strings.Contains(got, `rel="noopener noreferrer"`) {
		t.Errorf("expected rel attr, got %q", got)
	}
}

func TestRender_HTMLEscape(t *testing.T) {
	p := New()
	in := json.RawMessage(`{
		"type":"doc",
		"content":[
			{"type":"paragraph","content":[
				{"type":"text","text":"<script>alert(1)</script>"}
			]}
		]
	}`)
	got, _ := p.Render(in)
	if strings.Contains(got, "<script>") {
		t.Errorf("HTML escape failed; got %q", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("expected escaped script tag, got %q", got)
	}
}

func TestRender_Lists(t *testing.T) {
	p := New()
	in := json.RawMessage(`{
		"type":"doc",
		"content":[
			{"type":"bullet_list","content":[
				{"type":"list_item","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]}]},
				{"type":"list_item","content":[{"type":"paragraph","content":[{"type":"text","text":"b"}]}]}
			]}
		]
	}`)
	got, _ := p.Render(in)
	want := "<ul><li><p>a</p></li><li><p>b</p></li></ul>"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestRender_OrderedListAndBlockquote(t *testing.T) {
	p := New()
	in := json.RawMessage(`{
		"type":"doc",
		"content":[
			{"type":"ordered_list","content":[
				{"type":"list_item","content":[{"type":"paragraph","content":[{"type":"text","text":"x"}]}]}
			]},
			{"type":"blockquote","content":[{"type":"paragraph","content":[{"type":"text","text":"q"}]}]}
		]
	}`)
	got, _ := p.Render(in)
	if !strings.Contains(got, "<ol><li>") {
		t.Errorf("ol missing: %q", got)
	}
	if !strings.Contains(got, "<blockquote><p>q</p></blockquote>") {
		t.Errorf("blockquote missing: %q", got)
	}
}

func TestRender_UnknownNodeSkipped(t *testing.T) {
	p := New()
	in := json.RawMessage(`{
		"type":"doc",
		"content":[
			{"type":"unknown_block","content":[{"type":"paragraph","content":[{"type":"text","text":"x"}]}]}
		]
	}`)
	got, ok := p.Render(in)
	if !ok {
		t.Fatal("render ok=false")
	}
	// Unknown node skipped; its inner paragraph still rendered.
	if !strings.Contains(got, "<p>x</p>") {
		t.Errorf("expected inner content preserved, got %q", got)
	}
}

func TestRender_HeadingLevelClamp(t *testing.T) {
	p := New()
	for _, lvl := range []int{0, 7, -1, 99} {
		in := json.RawMessage(`{"type":"doc","content":[{"type":"heading","attrs":{"level":` + itoa(lvl) + `},"content":[{"type":"text","text":"t"}]}]}`)
		got, _ := p.Render(in)
		if !strings.Contains(got, "<h2>") {
			t.Errorf("level %d should clamp to h2, got %q", lvl, got)
		}
	}
}

func TestRender_InvalidReturnsFalse(t *testing.T) {
	p := New()
	_, ok := p.Render(json.RawMessage(`{"type":"paragraph"}`))
	if ok {
		t.Error("invalid root should return ok=false")
	}
}

// itoa keeps the test file dependency-free.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	s := string(b[i:])
	if neg {
		s = "-" + s
	}
	return s
}
