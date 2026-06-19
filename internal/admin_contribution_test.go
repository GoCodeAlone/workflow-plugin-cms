package internal

import "testing"

func TestCMSAdminContribution(t *testing.T) {
	contribution := CMSAdminContribution()

	if contribution.ID != "cms-site-manager" {
		t.Fatalf("ID = %q, want cms-site-manager", contribution.ID)
	}
	if contribution.RenderMode != "iframe" {
		t.Fatalf("RenderMode = %q, want iframe", contribution.RenderMode)
	}

	for _, permission := range []string{
		"admin:multisite.sites:read",
		"admin:multisite.sites:update",
		"admin:multisite.pages:read",
		"admin:multisite.pages:update",
		"admin:multisite.publish:update",
		"admin:multisite.onboarding:plan",
	} {
		if !stringSetContains(contribution.Permissions, permission) {
			t.Fatalf("missing permission %q in %#v", permission, contribution.Permissions)
		}
	}

	for _, key := range []string{
		"sites_path",
		"domains_path",
		"pages_path",
		"templates_path",
		"overlays_path",
		"launch_edit_path",
	} {
		if contribution.Metadata[key] == "" {
			t.Fatalf("metadata %q missing from %#v", key, contribution.Metadata)
		}
	}
}

func TestCMSAdminContributionRequiresAuthorizedMetadata(t *testing.T) {
	if metadata := CMSAdminContributionMetadata(false); len(metadata) != 0 {
		t.Fatalf("unauthorized metadata = %#v, want empty", metadata)
	}
	if metadata := CMSAdminContributionMetadata(true); metadata["pages_path"] == "" {
		t.Fatalf("authorized metadata missing pages_path: %#v", metadata)
	}
}

func TestEngineModuleAdminContributionService(t *testing.T) {
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

	out, err := invoker.InvokeMethod("AdminContribution", map[string]any{"authorized": true})
	if err != nil {
		t.Fatalf("AdminContribution: %v", err)
	}
	contribution := out["contribution"].(map[string]any)
	if contribution["id"] != "cms-site-manager" {
		t.Fatalf("contribution = %#v", contribution)
	}
	metadata := contribution["metadata"].(map[string]string)
	if metadata["sites_path"] == "" {
		t.Fatalf("metadata missing sites_path: %#v", metadata)
	}
}

func stringSetContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
