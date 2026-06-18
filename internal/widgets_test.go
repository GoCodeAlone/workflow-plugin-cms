package internal

import "testing"

func TestWidgetInstancesRenderOnlyAllowlistedTypes(t *testing.T) {
	registry := WidgetRegistry{
		Types: map[string]WidgetType{
			"tour-list": {Type: "tour-list", Markup: `<section data-widget="tour-list"></section>`},
		},
	}
	html, err := RenderWidgetInstance(WidgetInstance{ID: "w1", Type: "tour-list"}, registry)
	if err != nil {
		t.Fatalf("RenderWidgetInstance allowlisted: %v", err)
	}
	if html != `<section data-widget="tour-list"></section>` {
		t.Fatalf("html = %q, want allowlisted markup", html)
	}
	if _, err := RenderWidgetInstance(WidgetInstance{ID: "w2", Type: "raw-script"}, registry); err == nil {
		t.Fatal("unregistered widget type rendered")
	}
}

func TestWidgetRegistryRejectsScriptInjection(t *testing.T) {
	err := ValidateWidgetRegistry(WidgetRegistry{
		Types: map[string]WidgetType{
			"unsafe": {Type: "unsafe", Markup: `<script>alert(1)</script>`},
		},
	})
	if err == nil {
		t.Fatal("expected raw script widget markup to be rejected")
	}
}
