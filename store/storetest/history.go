package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

type HistoryStore interface {
	store.PageStore
	store.PageBatchStore
	store.PageHistoryStore
}

// PageHistory exercises the same saved-content contract in memory and actual
// PostgreSQL. Only committed writes create history; stale saves never retry.
func PageHistory(t *testing.T, s HistoryStore, tenantID, otherTenantID int64) {
	t.Helper()
	ctx := store.WithPageWriteActor(context.Background(), "user:owner-1")
	scheduled := time.Date(2030, 11, 3, 6, 30, 0, 0, time.UTC)
	p := &store.Page{Path: "/history", Title: "Original", BodyHTML: "<p>Original</p>", BodyBlocks: json.RawMessage("{\"z\":[],\"a\":\"blocks\"}"), TemplateID: "studio", Status: store.StatusScheduled, PublishAt: &scheduled}
	if err := s.Create(ctx, tenantID, p); err != nil {
		t.Fatal(err)
	}
	original := store.ContentOf(p)
	q := store.PageHistoryQuery{Limit: 100}
	h, err := s.ReadPageHistory(ctx, tenantID, q)
	if err != nil || len(h.Entries) != 1 {
		t.Fatalf("create history: %v %#v", err, h)
	}
	e := h.Entries[0]
	if e.Actor != "user:owner-1" || e.Operation != "create" || e.Revision != 1 || h.CurrentRevision != 1 || h.HistoryStartRevision != 0 || e.Scope != h.Scope || len(e.Before) != 0 || len(e.After) != 1 || e.After[0].Content.Digest() != original.Digest() || e.Digest != e.ContentDigest() || e.CommittedAt.IsZero() {
		t.Fatal("create did not bind saved content, trusted actor and revision")
	}
	// Returned values and caller pointers must not rewrite stored snapshots.
	e.After[0].Content.BodyBlocks[0] = ' '
	e.After[0].Content.PublishAt = nil
	p.BodyBlocks = nil
	p.PublishAt = nil
	p.Title = "Saved change"
	if err := s.Update(store.WithPageWriteActor(ctx, "agent:review-1"), tenantID, p); err != nil {
		t.Fatal(err)
	}
	saved := store.ContentOf(p)
	stale := saved.Page(tenantID, p.ID, 1)
	stale.Title = "Stale must not save"
	if err := s.Update(ctx, tenantID, stale); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatal("stale save accepted")
	}
	if err := s.Delete(ctx, tenantID, p.ID, 1); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatal("stale delete accepted")
	}
	h, err = s.ReadPageHistory(ctx, tenantID, q)
	if err != nil || len(h.Entries) != 2 || h.CurrentRevision != 2 || h.Entries[0].After[0].Content.Digest() != original.Digest() || h.Entries[1].Before[0].Content.Digest() != original.Digest() || h.Entries[1].After[0].Content.Digest() != saved.Digest() || h.Entries[1].Actor != "agent:review-1" || h.Entries[1].After[0].Version != 2 {
		t.Fatal("update history changed earlier snapshot or recorded failed writes")
	}
	if err := s.Delete(context.Background(), tenantID, p.ID, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, tenantID, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("deleted page still present")
	}
	h, err = s.ReadPageHistory(ctx, tenantID, q)
	if err != nil || len(h.Entries) != 3 || h.Entries[2].Operation != "delete" || h.Entries[2].Actor != "unattributed" || len(h.Entries[2].After) != 0 || h.Entries[2].Before[0].Content.Digest() != saved.Digest() {
		t.Fatal("deleted saved content not retained")
	}
	if other, err := s.ReadPageHistory(ctx, otherTenantID, q); err != nil && !errors.Is(err, store.ErrNotFound) || len(other.Entries) != 0 {
		t.Fatal("history leaked cross-tenant")
	}
	first, err := s.ReadPageHistory(ctx, tenantID, store.PageHistoryQuery{Limit: 2})
	if err != nil || len(first.Entries) != 2 || !first.HasMore || first.NextRevision != 2 {
		t.Fatal("history first page incorrect")
	}
	last, err := s.ReadPageHistory(ctx, tenantID, store.PageHistoryQuery{AfterRevision: first.NextRevision, Limit: 2})
	if err != nil || len(last.Entries) != 1 || last.HasMore || last.NextRevision != 3 {
		t.Fatal("history cursor replayed or omitted entries")
	}
	for _, query := range []store.PageHistoryQuery{{Limit: 0}, {Limit: 101}, {AfterRevision: -1, Limit: 1}} {
		if _, err := s.ReadPageHistory(ctx, tenantID, query); !errors.Is(err, store.ErrHistoryQuery) {
			t.Fatal("invalid history query accepted")
		}
	}
	if _, err := s.ReadPageHistory(ctx, 0, q); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("unscoped history read accepted")
	}
	// A batch event retains only its changed pages and rollback gets a new
	// tenant revision; receipt sealing and replay prevention stay authoritative.
	keep := &store.Page{Path: "/keep", Title: "Untouched", Status: store.StatusDraft}
	if err := s.Create(ctx, tenantID, keep); err != nil {
		t.Fatal(err)
	}
	state, err := s.ReadPageState(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	content := store.PageContent{Path: "/batch", Title: "Batch", Status: store.StatusDraft}
	receipt, err := s.ApplyPageBatch(ctx, tenantID, store.BatchFor(state, []store.PageMutation{{Key: "create", Kind: "create", Content: &content}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RollbackPageBatch(ctx, tenantID, receipt); err != nil {
		t.Fatal(err)
	}
	h, err = s.ReadPageHistory(ctx, tenantID, q)
	if err != nil || len(h.Entries) != 6 || h.Entries[4].Operation != "batch.apply" || len(h.Entries[4].Before) != 0 || len(h.Entries[4].After) != 1 || h.Entries[5].Operation != "batch.rollback" || len(h.Entries[5].Before) != 1 || len(h.Entries[5].After) != 0 || h.Entries[4].After[0].ID == keep.ID {
		t.Fatal("batch/rollback history omitted changes or included untouched pages")
	}
	before := h
	if _, err := s.RollbackPageBatch(ctx, tenantID, receipt); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatal("rollback replay accepted")
	}
	after, err := s.ReadPageHistory(ctx, tenantID, q)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed rollback wrote history")
	}
	for _, entry := range after.Entries {
		if entry.Digest != entry.ContentDigest() {
			t.Fatal("history digest mismatch")
		}
	}
}
