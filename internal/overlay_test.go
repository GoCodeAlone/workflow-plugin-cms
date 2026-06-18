package internal

import (
	"encoding/json"
	"testing"
)

func TestOverlayCloneStoresSourceSelectorsAndDraftBlocks(t *testing.T) {
	overlay, err := NewStaticPageOverlay(StaticPageOverlayInput{
		TenantID:   7,
		SourcePath: "/about.html",
		SourceHash: "sha256:abc123",
		Selectors: []OverlaySelector{
			{Selector: "#hero", Mode: OverlayModeReplace},
			{Selector: ".cta", Mode: OverlayModeAppend},
		},
		DraftBlocks: json.RawMessage(`{"type":"doc","content":[{"type":"paragraph"}]}`),
	})
	if err != nil {
		t.Fatalf("NewStaticPageOverlay: %v", err)
	}

	if overlay.TenantID != 7 || overlay.SourcePath != "/about.html" || overlay.SourceHash != "sha256:abc123" {
		t.Fatalf("overlay source fields = %#v", overlay)
	}
	if len(overlay.Selectors) != 2 || overlay.Selectors[0].Selector != "#hero" {
		t.Fatalf("selectors = %#v, want cloned selectors", overlay.Selectors)
	}
	if len(overlay.DraftBlocks) == 0 {
		t.Fatalf("draft blocks not stored: %#v", overlay)
	}
	if overlay.Status != OverlayStatusDraft || !overlay.Enabled {
		t.Fatalf("initial status/enabled = %q/%v, want draft/enabled", overlay.Status, overlay.Enabled)
	}
}

func TestOverlayPublishRequiresMatchingSourceHashUnlessForced(t *testing.T) {
	overlay, err := NewStaticPageOverlay(StaticPageOverlayInput{
		TenantID:   7,
		SourcePath: "/about.html",
		SourceHash: "sha256:abc123",
		Selectors:  []OverlaySelector{{Selector: "#hero", Mode: OverlayModeReplace}},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := PublishOverlay(overlay, "sha256:abc123", false)
	if err != nil {
		t.Fatalf("PublishOverlay match: %v", err)
	}
	if !result.Published || overlay.Status != OverlayStatusPublished {
		t.Fatalf("publish result/status = %#v/%q, want published", result, overlay.Status)
	}

	overlay.Status = OverlayStatusDraft
	result, err = PublishOverlay(overlay, "sha256:new", false)
	if err != nil {
		t.Fatalf("PublishOverlay mismatch: %v", err)
	}
	if result.Published || overlay.Status != OverlayStatusConflictReview {
		t.Fatalf("mismatch result/status = %#v/%q, want conflict_review", result, overlay.Status)
	}
	if result.ConflictReason == "" {
		t.Fatalf("missing conflict reason: %#v", result)
	}

	result, err = PublishOverlay(overlay, "sha256:new", true)
	if err != nil {
		t.Fatalf("PublishOverlay forced: %v", err)
	}
	if !result.Published || overlay.Status != OverlayStatusPublished || overlay.SourceHash != "sha256:new" {
		t.Fatalf("forced result/status/hash = %#v/%q/%q, want published with new hash", result, overlay.Status, overlay.SourceHash)
	}
}

func TestOverlayDisablePreservesStaticSourceReference(t *testing.T) {
	overlay, err := NewStaticPageOverlay(StaticPageOverlayInput{
		TenantID:   7,
		SourcePath: "/about.html",
		SourceHash: "sha256:abc123",
		Selectors:  []OverlaySelector{{Selector: "#hero", Mode: OverlayModeReplace}},
	})
	if err != nil {
		t.Fatal(err)
	}

	DisableOverlay(overlay)
	if overlay.Enabled {
		t.Fatal("overlay still enabled")
	}
	if overlay.Status != OverlayStatusDisabled {
		t.Fatalf("status = %q, want disabled", overlay.Status)
	}
	if overlay.SourcePath != "/about.html" || overlay.SourceHash != "sha256:abc123" {
		t.Fatalf("source reference was modified: %#v", overlay)
	}
}

func TestOverlayEngineHooksUseTypedOverlayModel(t *testing.T) {
	mod, err := newEngineModule("cms", nil)
	if err != nil {
		t.Fatalf("newEngineModule: %v", err)
	}
	invoker, ok := mod.(interface {
		InvokeMethod(string, map[string]any) (map[string]any, error)
	})
	if !ok {
		t.Fatal("engine module does not expose service invoker")
	}

	out, err := invoker.InvokeMethod("OverlayClone", map[string]any{
		"tenant_id":   float64(7),
		"source_path": "/about.html",
		"source_hash": "sha256:abc123",
		"selectors": []map[string]string{
			{"selector": "#hero", "mode": "replace"},
		},
	})
	if err != nil {
		t.Fatalf("OverlayClone: %v", err)
	}
	overlay := out["overlay"].(*StaticPageOverlay)
	out, err = invoker.InvokeMethod("OverlayPublish", map[string]any{
		"overlay":             overlay,
		"current_source_hash": "sha256:new",
	})
	if err != nil {
		t.Fatalf("OverlayPublish: %v", err)
	}
	result := out["result"].(OverlayPublishResult)
	if result.Published {
		t.Fatalf("publish result = %#v, want conflict", result)
	}
}
