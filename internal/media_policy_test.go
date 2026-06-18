package internal

import "testing"

func TestMediaPolicyAllowsRelativeAndOwnedObjectStoreURLs(t *testing.T) {
	policy := MediaPolicy{
		AllowedObjectPrefixes: []string{"https://cdn.gocodealone.com/sites/blackorchid/"},
	}
	for _, value := range []string{
		"/assets/black-orchid-logo.png",
		"images/filler.jpeg",
		"https://cdn.gocodealone.com/sites/blackorchid/filler.jpeg",
	} {
		if err := ValidatePublishedMediaReference(value, policy); err != nil {
			t.Fatalf("ValidatePublishedMediaReference(%q): %v", value, err)
		}
	}
}

func TestMediaPolicyRejectsWixAndUnownedSourceHosts(t *testing.T) {
	policy := MediaPolicy{
		AllowedObjectPrefixes: []string{"https://cdn.gocodealone.com/sites/blackorchid/"},
	}
	for _, value := range []string{
		"https://static.wixstatic.com/media/logo.png",
		"https://static.parastorage.com/services/site.png",
		"https://example.com/image.png",
		"//static.wixstatic.com/media/logo.png",
	} {
		if err := ValidatePublishedMediaReference(value, policy); err == nil {
			t.Fatalf("ValidatePublishedMediaReference(%q) succeeded, want rejection", value)
		}
	}
}
