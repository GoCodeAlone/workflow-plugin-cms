package internal

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

func TestRenderPageDocument_BodyBlocksCanonicalWhenPresent(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	page := &store.Page{
		Path:       "/about",
		Title:      "About",
		BodyHTML:   "<p>stale html</p>",
		BodyBlocks: json.RawMessage(`{"type":"doc","content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"About Us"}]},{"type":"paragraph","content":[{"type":"text","text":"fresh blocks"}]}]}`),
		Status:     store.StatusPublished,
	}

	got, rendered, err := RenderPageDocument(page, PageTemplate{}, now)
	if err != nil {
		t.Fatalf("RenderPageDocument: %v", err)
	}
	if !rendered {
		t.Fatal("published page was not rendered")
	}
	if !strings.Contains(got, "<h2>About Us</h2>") || !strings.Contains(got, "<p>fresh blocks</p>") {
		t.Fatalf("rendered body = %q, want block document HTML", got)
	}
	if strings.Contains(got, "stale html") {
		t.Fatalf("body_html rendered despite body_blocks being present: %q", got)
	}
}

func TestRenderPageDocument_DraftDoesNotRenderPublicly(t *testing.T) {
	got, rendered, err := RenderPageDocument(&store.Page{
		Path:     "/draft",
		Title:    "Draft",
		BodyHTML: "<p>draft</p>",
		Status:   store.StatusDraft,
	}, PageTemplate{}, time.Now())
	if err != nil {
		t.Fatalf("RenderPageDocument: %v", err)
	}
	if rendered || got != "" {
		t.Fatalf("draft rendered = %v body=%q, want not rendered", rendered, got)
	}
}

func TestRenderPageDocument_ScheduledRendersOnlyAtPublishTime(t *testing.T) {
	publishAt := time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)
	page := &store.Page{
		Path:      "/tour",
		Title:     "Tour",
		BodyHTML:  "<p>tour</p>",
		Status:    store.StatusScheduled,
		PublishAt: &publishAt,
	}

	if got, rendered, err := RenderPageDocument(page, PageTemplate{}, publishAt.Add(-time.Second)); err != nil || rendered || got != "" {
		t.Fatalf("future scheduled page rendered=%v body=%q err=%v, want not rendered", rendered, got, err)
	}
	if got, rendered, err := RenderPageDocument(page, PageTemplate{}, publishAt); err != nil || !rendered || !strings.Contains(got, "tour") {
		t.Fatalf("active scheduled page rendered=%v body=%q err=%v, want rendered", rendered, got, err)
	}
}

func TestRenderPageDocument_TemplateWrapsPageBody(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	page := &store.Page{
		Path:     "/contact",
		Title:    "Contact",
		BodyHTML: "<main>contact body</main>",
		Status:   store.StatusPublished,
	}
	template := PageTemplate{
		ID:   "site-shell",
		HTML: "<html><body><header>site shell</header><!--cms:body--><footer>site footer</footer></body></html>",
	}

	got, rendered, err := RenderPageDocument(page, template, now)
	if err != nil {
		t.Fatalf("RenderPageDocument: %v", err)
	}
	if !rendered {
		t.Fatal("published page was not rendered")
	}
	for _, want := range []string{"site shell", "<main>contact body</main>", "site footer"} {
		if !strings.Contains(got, want) {
			t.Fatalf("template output missing %q: %q", want, got)
		}
	}
}
