package render

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestFindMarkerIDs(t *testing.T) {
	html := []byte(`
<html>
<body>
<!-- multisite:latest-blog -->
<p>hi</p>
<!-- multisite:contact-form  -->
<!-- multisite:latest-blog -->
</body>
</html>`)
	got := FindMarkerIDs(html)
	want := []string{"latest-blog", "contact-form"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestSubstitute_ReplacesMarker(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(StringTemplate("blog-list", "<ul><li>Post 1</li></ul>"))

	html := []byte(`<div><!-- multisite:latest-blog --></div>`)
	specs := []SectionSpec{{ID: "latest-blog", Template: "blog-list"}}

	out, err := Substitute(context.Background(), html, r, specs, SubstituteOptions{})
	if err != nil {
		t.Fatalf("Substitute: %v", err)
	}
	want := `<div><ul><li>Post 1</li></ul></div>`
	if string(out) != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestSubstitute_PreservesUnknownMarker_Default(t *testing.T) {
	r := NewRegistry()
	html := []byte(`<p><!-- multisite:nope --></p>`)
	out, err := Substitute(context.Background(), html, r, nil, SubstituteOptions{})
	if err != nil {
		t.Errorf("default policy should not error: %v", err)
	}
	if !strings.Contains(string(out), "multisite:nope") {
		t.Errorf("comment should remain on missing spec; got %q", out)
	}
}

func TestSubstitute_MissingSpec_ErrorPolicy(t *testing.T) {
	r := NewRegistry()
	html := []byte(`<p><!-- multisite:nope --></p>`)
	_, err := Substitute(context.Background(), html, r, nil, SubstituteOptions{OnMissingSpec: MissingSpecError})
	if err == nil {
		t.Fatal("expected error under MissingSpecError policy")
	}
}

func TestSubstitute_MissingTemplate_ErrorPolicy(t *testing.T) {
	r := NewRegistry()
	// Spec declared, but template not registered.
	html := []byte(`<p><!-- multisite:latest-blog --></p>`)
	specs := []SectionSpec{{ID: "latest-blog", Template: "blog-list"}}
	_, err := Substitute(context.Background(), html, r, specs, SubstituteOptions{OnMissingTemplate: MissingTemplateError})
	if err == nil {
		t.Fatal("expected error under MissingTemplateError policy")
	}
	if !strings.Contains(err.Error(), "blog-list") {
		t.Errorf("error should reference missing template: %v", err)
	}
}

func TestSubstitute_TemplateRenderError(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&errTemplate{})
	html := []byte(`<p><!-- multisite:x --></p>`)
	specs := []SectionSpec{{ID: "x", Template: "err-template"}}
	out, err := Substitute(context.Background(), html, r, specs, SubstituteOptions{})
	if err == nil {
		t.Fatal("expected error from template")
	}
	// Failed-section marker should remain in output for diagnostic.
	if !strings.Contains(string(out), "multisite:x") {
		t.Errorf("on error, marker should remain in partial output; got %q", out)
	}
}

type errTemplate struct{}

func (e *errTemplate) Name() string { return "err-template" }
func (e *errTemplate) Render(_ context.Context, _ string, _ map[string]string) (string, error) {
	return "", errors.New("intentional render failure")
}

func TestSubstitute_MultipleMarkers(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(StringTemplate("a-tmpl", "A"))
	_ = r.Register(StringTemplate("b-tmpl", "B"))

	html := []byte(`<x><!-- multisite:a --></x><y><!-- multisite:b --></y>`)
	specs := []SectionSpec{
		{ID: "a", Template: "a-tmpl"},
		{ID: "b", Template: "b-tmpl"},
	}
	out, err := Substitute(context.Background(), html, r, specs, SubstituteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `<x>A</x><y>B</y>` {
		t.Errorf("got %q", out)
	}
}

func TestSubstitute_NoMarkers_PassThrough(t *testing.T) {
	r := NewRegistry()
	html := []byte(`<html><body><p>nothing dynamic here</p></body></html>`)
	out, err := Substitute(context.Background(), html, r, nil, SubstituteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(html) {
		t.Errorf("pass-through changed; got %q", out)
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(StringTemplate("a", "x"))
	if err := r.Register(StringTemplate("a", "y")); err == nil {
		t.Error("duplicate register should error")
	}
}

func TestRegistry_NilAndEmpty(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Error("nil should error")
	}
	if err := r.Register(StringTemplate("", "x")); err == nil {
		t.Error("empty name should error")
	}
}

func TestSubstitute_ParamsPassedToTemplate(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&paramCapture{})
	html := []byte(`<x><!-- multisite:p --></x>`)
	specs := []SectionSpec{{
		ID:       "p",
		Template: "param-capture",
		Params:   map[string]string{"limit": "5", "tag": "release"},
	}}
	_, err := Substitute(context.Background(), html, r, specs, SubstituteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if paramCaptureSeen["limit"] != "5" || paramCaptureSeen["tag"] != "release" {
		t.Errorf("params not passed; got %v", paramCaptureSeen)
	}
}

type paramCapture struct{}

var paramCaptureSeen = map[string]string{}

func (p *paramCapture) Name() string { return "param-capture" }
func (p *paramCapture) Render(_ context.Context, _ string, params map[string]string) (string, error) {
	paramCaptureSeen = params
	return "", nil
}
