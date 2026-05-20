package internal

import (
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
