package internal

import (
	"testing"
	"time"
)

func TestNavigationTargetsAndPublishedFilter(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	items := []NavigationItem{
		{Label: "Home", Kind: NavTargetStatic, Target: "/", Status: NavStatusPublished},
		{Label: "About", Kind: NavTargetCMSPage, Target: "/about", Status: NavStatusPublished},
		{Label: "Feature", Kind: NavTargetOverlay, Target: "/feature.html#hero", Status: NavStatusPublished},
		{Label: "GitHub", Kind: NavTargetExternal, Target: "https://github.com/GoCodeAlone", Status: NavStatusPublished},
		{Label: "Draft", Kind: NavTargetCMSPage, Target: "/draft", Status: NavStatusDraft},
		{Label: "Future", Kind: NavTargetCMSPage, Target: "/future", Status: NavStatusScheduled, PublishAt: &future},
	}

	published, err := PublishedNavigation(items, now)
	if err != nil {
		t.Fatalf("PublishedNavigation: %v", err)
	}
	if len(published) != 4 {
		t.Fatalf("published nav count = %d, want 4: %#v", len(published), published)
	}
	for _, item := range published {
		if item.Label == "Draft" || item.Label == "Future" {
			t.Fatalf("unpublished item included: %#v", item)
		}
	}
}

func TestNavigationRejectsInvalidTargets(t *testing.T) {
	_, err := PublishedNavigation([]NavigationItem{
		{Label: "Bad", Kind: NavTargetExternal, Target: "javascript:alert(1)", Status: NavStatusPublished},
	}, time.Now())
	if err == nil {
		t.Fatal("expected invalid external target to fail")
	}
}
