package internal

import (
	"context"
	"testing"
)

func TestManifest_Identity(t *testing.T) {
	p := NewPlugin()
	cms, ok := p.(*CMSPlugin)
	if !ok {
		t.Fatalf("expected *CMSPlugin, got %T", p)
	}
	m := cms.Manifest()
	if m.Name != "workflow-plugin-cms" {
		t.Errorf("name: got %q want workflow-plugin-cms", m.Name)
	}
	if m.Author != "GoCodeAlone" {
		t.Errorf("author: got %q want GoCodeAlone", m.Author)
	}
}

func TestModuleTypes_All(t *testing.T) {
	p := NewPlugin().(*CMSPlugin)
	want := map[string]bool{
		"cms.tenant_resolver":             true,
		"cms.static_serve_before_dynamic": true,
		"cms.engine":                      true,
		"analytics.injection":             true,
	}
	got := p.ModuleTypes()
	if len(got) != len(want) {
		t.Errorf("module count: got %d want %d (%v)", len(got), len(want), got)
	}
	for _, mt := range got {
		if !want[mt] {
			t.Errorf("unexpected module type: %s", mt)
		}
	}
}

func TestCreateModule_AllTypesInstantiate(t *testing.T) {
	p := NewPlugin().(*CMSPlugin)
	for _, mt := range p.ModuleTypes() {
		m, err := p.CreateModule(mt, "test", map[string]any{})
		if err != nil {
			t.Errorf("CreateModule(%s): %v", mt, err)
		}
		if m == nil {
			t.Errorf("CreateModule(%s) returned nil module", mt)
		}
	}
}

func TestCreateModule_UnknownType(t *testing.T) {
	p := NewPlugin().(*CMSPlugin)
	_, err := p.CreateModule("unknown.type", "x", nil)
	if err == nil {
		t.Fatal("expected error for unknown module type, got nil")
	}
}

func TestStepTypes_Listed(t *testing.T) {
	p := NewPlugin().(*CMSPlugin)
	want := map[string]bool{
		"step.cms_render_page":     true,
		"step.cms_bundle_activate": true,
	}
	for _, st := range p.StepTypes() {
		if !want[st] {
			t.Errorf("unexpected step type: %s", st)
		}
	}
}

func TestCreateStep_RenderPageExecutes(t *testing.T) {
	p := NewPlugin().(*CMSPlugin)
	step, err := p.CreateStep("step.cms_render_page", "render", map[string]any{
		"content_type": "text/html; charset=utf-8",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := step.Execute(context.Background(), nil, nil, map[string]any{
		"tenant_id": int64(7),
		"path":      "/about",
		"title":     "About",
		"body_html": "<main>About</main>",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Output["html"] != "<main>About</main>" || got.Output["path"] != "/about" || got.Output["tenant_id"] != int64(7) {
		t.Fatalf("render output = %+v", got.Output)
	}
}

func TestCreateStep_BundleActivateExecutesValidation(t *testing.T) {
	p := NewPlugin().(*CMSPlugin)
	step, err := p.CreateStep("step.cms_bundle_activate", "activate", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := step.Execute(context.Background(), nil, nil, nil, nil, nil); err == nil {
		t.Fatal("expected missing bundle activation config to fail")
	}
}

func TestCreateStep_UnknownType(t *testing.T) {
	p := NewPlugin().(*CMSPlugin)
	if _, err := p.CreateStep("step.unknown", "x", nil); err == nil {
		t.Fatal("expected error for unknown step type")
	}
}
