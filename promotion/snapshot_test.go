package promotion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

func fixture(t *testing.T) (Snapshot, string, *store.MemoryPageStore, *store.Page) {
	t.Helper()
	root := t.TempDir()
	write := func(p, s string) {
		name := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("assets/site.css", "body{color:#111;background:#fff}")
	write("assets/photo.jpg", "owned-photo-bytes")
	write("cms/templates/main.html", `<!doctype html><link rel="stylesheet" href="/assets/site.css"><main><!--cms:body--></main>`)
	pages := store.NewMemoryPageStore()
	p := &store.Page{Path: "/", Title: "Approved home", BodyHTML: `<img src="/assets/photo.jpg" alt="Portrait"><a href="/">Home</a>`, Status: store.StatusPublished, TemplateID: "main"}
	if err := pages.Create(context.Background(), 10, p); err != nil {
		t.Fatal(err)
	}
	s, err := Export(context.Background(), pages, 10, "test-review", []int64{p.ID}, root, time.Date(2026, 10, 8, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return s, root, pages, p
}

func TestSnapshotFreezeExplicitMappingAndDryRun(t *testing.T) {
	s, root, pages, review := fixture(t)
	b, _ := json.Marshal(s)
	decoded, err := Decode(bytes.NewReader(b))
	if err != nil || decoded.Digest != s.Digest {
		t.Fatal("strict roundtrip failed")
	}
	for _, needle := range []string{`"tenant_id"`, `"password"`, `"domains"`, `"editors"`, `"repository"`, `"token"`} {
		if bytes.Contains(b, []byte(needle)) {
			t.Fatal("authority field leaked into snapshot")
		}
	}
	review.Title = "Later unapproved draft"
	if err := pages.Update(context.Background(), 10, review); err != nil {
		t.Fatal(err)
	}
	if s.Pages[0].Content.Title == review.Title {
		t.Fatal("export was not frozen")
	}
	target := &store.Page{Path: "/", Title: "Old live home", Status: store.StatusPublished}
	if err := pages.Create(context.Background(), 20, target); err != nil {
		t.Fatal(err)
	}
	keep := &store.Page{Path: "/keep", Title: "Keep", Status: store.StatusDraft}
	if err := pages.Create(context.Background(), 20, keep); err != nil {
		t.Fatal(err)
	}
	current, _ := pages.List(context.Background(), 20, "")
	mappings, err := SuggestedMapping(s, current)
	if err != nil || mappings[0].TargetID != target.ID || target.ID == s.Pages[0].SourceID {
		t.Fatal("path suggestion/mapping failed")
	}
	plan, err := DryRun(s, current, mappings, nil, s.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Batch.Mutations) != 1 || len(plan.Batch.Baseline) != 2 || plan.Diffs[0].Action != "update" {
		t.Fatal("dry run omitted baseline or updated omission")
	}
	before, _ := pages.Get(context.Background(), 20, target.ID)
	if before.Title != target.Title {
		t.Fatal("dry run wrote content")
	}
	receipt, err := pages.ApplyPageBatch(context.Background(), 20, plan.Batch)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := pages.Get(context.Background(), 20, target.ID)
	if got.Title != s.Pages[0].Content.Title {
		t.Fatal("apply used unapproved source edit")
	}
	if err := VerifyBundle(root, s.Bundle, []store.PageContent{store.ContentOf(got)}); err != nil {
		t.Fatal(err)
	}
	if _, err := pages.RollbackPageBatch(context.Background(), 20, receipt); err != nil {
		t.Fatal(err)
	}
	move := s
	move.Pages = append([]SnapshotPage(nil), s.Pages...)
	move.Pages[0].Content.Path = "/moved"
	move.Digest = move.ContentDigest()
	if _, err := DryRun(move, current, mappings, nil, s.Bundle); !errors.Is(err, ErrMapping) {
		t.Fatal("implicit move accepted")
	}
	if _, err := DryRun(s, current, nil, nil, s.Bundle); !errors.Is(err, ErrMapping) {
		t.Fatal("implicit target mapping accepted")
	}
}

func TestDecodeRejectsAuthorityUnknownDuplicateAndTrailingWithoutValues(t *testing.T) {
	s, _, _, _ := fixture(t)
	b, _ := json.Marshal(s)
	base := string(b)
	for _, payload := range []string{strings.Replace(base, `"schema":1`, `"schema":1,"credentials":"rejected_marker"`, 1), strings.Replace(base, `"schema":1`, `"schema":1,"schema":1`, 1), base + base, strings.Replace(base, `"title":"Approved home"`, `"title":"Approved home","domains":["rejected_marker"]`, 1), strings.Replace(base, `"size":`, `"extra":"rejected_marker","size":`, 1)} {
		_, err := Decode(strings.NewReader(payload))
		if err != ErrInvalid || strings.Contains(err.Error(), "rejected_marker") {
			t.Fatal("strict decoder leaked or accepted invalid payload")
		}
	}
}

func TestBundleHashesReferencesStaticCollisionsAndSymlinks(t *testing.T) {
	s, root, _, _ := fixture(t)
	contents := []store.PageContent{s.Pages[0].Content}
	if err := os.WriteFile(filepath.Join(root, "assets/photo.jpg"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if VerifyBundle(root, s.Bundle, contents) != ErrBundle {
		t.Fatal("changed bundle bytes accepted")
	}
	manifest, _ := InventoryBundle(root)
	for _, ref := range []string{`<img src="/media/10/hash.jpg">`, `<img src="https://example.com/media/10/hash.jpg">`, `<img src="/assets/missing.jpg">`, `<img src="/api/v1/admin/tenants/10/pages/assets/photo.jpg">`, `<a href="https://test-review.preview.example.com/">Review</a>`} {
		p := contents[0]
		p.BodyHTML = ref
		if VerifyBundle(root, manifest, []store.PageContent{p}) != ErrBundle {
			t.Fatal("unsupported reference accepted")
		}
	}
	p := contents[0]
	p.BodyBlocks = json.RawMessage(`{"type":"doc","url":"\u002fmedia/10/hash.jpg"}`)
	if VerifyBundle(root, manifest, []store.PageContent{p}) != ErrBundle {
		t.Fatal("escaped upload reference accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("Static shadow"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, _ = InventoryBundle(root)
	if VerifyBundle(root, manifest, contents) != ErrBundle {
		t.Fatal("static homepage shadow accepted")
	}
	if err := os.Remove(filepath.Join(root, "index.html")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "assets/photo.jpg"), filepath.Join(root, "assets/alias.jpg")); err != nil {
		t.Fatal(err)
	}
	if _, err := InventoryBundle(root); err != ErrBundle {
		t.Fatal("bundle symlink accepted")
	}
}
