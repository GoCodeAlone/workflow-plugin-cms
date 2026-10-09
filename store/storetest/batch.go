// Package storetest provides the same mutation behavior contract for memory and
// real Postgres tests. It writes only the fresh tenant IDs supplied by the test.
package storetest

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

type BatchStore interface {
	store.PageStore
	store.PageBatchStore
}

func PageBatches(t *testing.T, s BatchStore, targetTenant, sourceTenant int64) store.PageBatchReceipt {
	t.Helper()
	ctx := context.Background()
	create := func(tenant int64, path, title string) *store.Page {
		p := &store.Page{Path: path, Title: title, BodyHTML: "<p>" + title + "</p>", Status: store.StatusDraft}
		if err := s.Create(ctx, tenant, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	home := create(targetTenant, "/", "Old home")
	keep := create(targetTenant, "/keep", "Keep")
	removed := create(targetTenant, "/remove", "Remove")
	review := create(sourceTenant, "/", "Approved home")
	if review.ID == home.ID {
		t.Fatal("source and target must differ")
	}

	state, err := s.ReadPageState(ctx, targetTenant)
	if err != nil {
		t.Fatal(err)
	}
	content := store.ContentOf(review)
	news := store.PageContent{Path: "/news", Title: "New page", BodyHTML: "<p>News</p>", Status: store.StatusPublished}
	batch := store.PageBatch{TargetScope: state.Scope, BaselineRevision: state.Revision, Baseline: store.Baseline(state.Pages), Mutations: []store.PageMutation{{Key: "review:home", Kind: "update", TargetID: home.ID, ExpectedVersion: home.Version, Content: &content}, {Key: "review:news", Kind: "create", Content: &news}, {Key: "explicit:remove", Kind: "delete", TargetID: removed.ID, ExpectedVersion: removed.Version}}}
	bad := batch
	bad.Mutations = append([]store.PageMutation(nil), batch.Mutations...)
	invalid := news
	invalid.Title = ""
	bad.Mutations[1].Content = &invalid
	if _, err := s.ApplyPageBatch(ctx, targetTenant, bad); !errors.Is(err, store.ErrBatchInvalid) {
		t.Fatalf("invalid item must reject batch: %v", err)
	}
	unchanged, _ := s.Get(ctx, targetTenant, home.ID)
	if unchanged.Version != home.Version || unchanged.Title != home.Title {
		t.Fatal("invalid batch partially mutated")
	}
	if _, err := s.ApplyPageBatch(ctx, sourceTenant, batch); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("wrong target accepted: %v", err)
	}
	receipt, err := s.ApplyPageBatch(ctx, targetTenant, batch)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, targetTenant, home.ID)
	if err != nil || got.Title != review.Title || got.Version != 2 {
		t.Fatalf("promoted content did not reload: %v", err)
	}
	if receipt.Mapping["review:home"] != home.ID || receipt.Mapping["review:news"] == review.ID {
		t.Fatal("mapping copied a source identity")
	}
	kept, _ := s.Get(ctx, targetTenant, keep.ID)
	if kept.Version != keep.Version || kept.Title != keep.Title {
		t.Fatal("omitted page changed")
	}
	if err := s.Update(ctx, targetTenant, home); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("stale editor overwrote promotion: %v", err)
	}
	if err := s.Delete(ctx, targetTenant, home.ID, home.Version); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("stale delete accepted: %v", err)
	}
	if err := s.Delete(ctx, sourceTenant, home.ID, got.Version); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("cross tenant delete must not leak")
	}
	if _, err := s.RollbackPageBatch(ctx, targetTenant, receipt); err != nil {
		t.Fatal(err)
	}
	rolled, _ := s.Get(ctx, targetTenant, home.ID)
	if rolled.Title != home.Title || rolled.Version <= got.Version {
		t.Fatal("rollback did not restore content with monotonic version")
	}
	restored, _ := s.Get(ctx, targetTenant, removed.ID)
	if restored == nil || restored.Version <= removed.Version {
		t.Fatal("deleted page not restored with new version")
	}
	if _, err := s.Get(ctx, targetTenant, receipt.Mapping["review:news"]); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("created page survived rollback")
	}

	state, err = s.ReadPageState(ctx, targetTenant)
	if err != nil {
		t.Fatal(err)
	}
	batch.Baseline = store.Baseline(state.Pages)
	batch.BaselineRevision = state.Revision
	batch.Mutations[0].ExpectedVersion = rolled.Version
	batch.Mutations[2].ExpectedVersion = restored.Version
	receipt, err = s.ApplyPageBatch(ctx, targetTenant, batch)
	if err != nil {
		t.Fatal(err)
	}
	kept, _ = s.Get(ctx, targetTenant, keep.ID)
	kept.Title = "Later editorial change"
	if err := s.Update(ctx, targetTenant, kept); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RollbackPageBatch(ctx, targetTenant, receipt); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("rollback overwrote later edit: %v", err)
	}
	if _, err := s.ApplyPageBatch(ctx, targetTenant, batch); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("baseline drift accepted: %v", err)
	}
	// Simultaneous save/promotion: only one may use the same loaded version.
	fresh, _ := s.Get(ctx, targetTenant, home.ID)

	state, err = s.ReadPageState(ctx, targetTenant)
	if err != nil {
		t.Fatal(err)
	}
	next := store.ContentOf(fresh)
	next.Title = "Promotion race winner"
	raceBatch := store.PageBatch{TargetScope: state.Scope, BaselineRevision: state.Revision, Baseline: store.Baseline(state.Pages), Mutations: []store.PageMutation{{Key: "race", Kind: "update", TargetID: fresh.ID, ExpectedVersion: fresh.Version, Content: &next}}}
	fresh.Title = "Editor race winner"
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; results <- s.Update(ctx, targetTenant, fresh) }()
	go func() { <-start; _, err := s.ApplyPageBatch(ctx, targetTenant, raceBatch); results <- err }()
	close(start)
	wins := 0
	conflicts := 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			wins++
		} else if errors.Is(err, store.ErrVersionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("racing save and promotion did not conflict")
	}
	// A delete loaded before promotion also competes on the same version.
	fresh, _ = s.Get(ctx, targetTenant, home.ID)

	state, err = s.ReadPageState(ctx, targetTenant)
	if err != nil {
		t.Fatal(err)
	}
	next = store.ContentOf(fresh)
	next.Title = "Delete race promotion"
	raceBatch = store.PageBatch{TargetScope: state.Scope, BaselineRevision: state.Revision, Baseline: store.Baseline(state.Pages), Mutations: []store.PageMutation{{Key: "delete-race", Kind: "update", TargetID: fresh.ID, ExpectedVersion: fresh.Version, Content: &next}}}
	start = make(chan struct{})
	go func() { <-start; results <- s.Delete(ctx, targetTenant, fresh.ID, fresh.Version) }()
	go func() { <-start; _, err := s.ApplyPageBatch(ctx, targetTenant, raceBatch); results <- err }()
	close(start)
	wins, conflicts = 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			wins++
		} else if errors.Is(err, store.ErrVersionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("racing delete and promotion did not conflict")
	}
	// Concurrent creates of one path are serialized too.
	start = make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			p := &store.Page{Path: "/race-create", Title: "Race", Status: store.StatusDraft}
			results <- s.Create(ctx, targetTenant, p)
		}()
	}
	close(start)
	wins, conflicts = 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			wins++
		} else if errors.Is(err, store.ErrPathConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("duplicate racing create accepted")
	}
	// Occupied legacy staging names must not block unchanged-path updates.
	kept, _ = s.Get(ctx, targetTenant, keep.ID)
	occupied := create(targetTenant, "/__cms_batch_internal__/"+strconv.FormatInt(kept.ID, 10), "Omitted staging path")
	state, err = s.ReadPageState(ctx, targetTenant)
	if err != nil {
		t.Fatal(err)
	}
	titleOnly := store.ContentOf(kept)
	titleOnly.Title = "Title-only promotion"
	if _, err := s.ApplyPageBatch(ctx, targetTenant, store.BatchFor(state, []store.PageMutation{{Key: "title-only", Kind: "update", TargetID: kept.ID, ExpectedVersion: kept.Version, Content: &titleOnly}})); err != nil {
		t.Fatalf("occupied staging path blocked title update: %v", err)
	}
	still, _ := s.Get(ctx, targetTenant, occupied.ID)
	if still.Title != occupied.Title {
		t.Fatal("omitted staging path changed")
	}
	a, b := create(targetTenant, "/swap-a", "Swap A"), create(targetTenant, "/swap-b", "Swap B")
	state, _ = s.ReadPageState(ctx, targetTenant)
	ca, cb := store.ContentOf(a), store.ContentOf(b)
	ca.Path, cb.Path = b.Path, a.Path
	if _, err := s.ApplyPageBatch(ctx, targetTenant, store.BatchFor(state, []store.PageMutation{{Key: "swap-a", Kind: "update", TargetID: a.ID, ExpectedVersion: a.Version, Content: &ca}, {Key: "swap-b", Kind: "update", TargetID: b.ID, ExpectedVersion: b.Version, Content: &cb}})); err != nil {
		t.Fatalf("atomic path swap failed: %v", err)
	}
	afterA, _ := s.Get(ctx, targetTenant, a.ID)
	afterB, _ := s.Get(ctx, targetTenant, b.ID)
	if afterA.Path != b.Path || afterB.Path != a.Path {
		t.Fatal("path swap not persisted")
	}
	// Deletion ABA: an old empty post-state can recur after newer mutations.
	state, _ = s.ReadPageState(ctx, sourceTenant)
	review, _ = s.Get(ctx, sourceTenant, review.ID)
	deleted, err := s.ApplyPageBatch(ctx, sourceTenant, store.BatchFor(state, []store.PageMutation{{Key: "aba-delete", Kind: "delete", TargetID: review.ID, ExpectedVersion: review.Version}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RollbackPageBatch(ctx, sourceTenant, deleted); err != nil {
		t.Fatal(err)
	}
	oldEditor, _ := s.Get(ctx, sourceTenant, review.ID)
	fresh, _ = s.Get(ctx, sourceTenant, review.ID)
	fresh.Title = "Intervening edit"
	if s.Update(ctx, sourceTenant, fresh) != nil || s.Delete(ctx, sourceTenant, fresh.ID, fresh.Version) != nil {
		t.Fatal("ABA edit/delete fixture failed")
	}
	if _, err := s.RollbackPageBatch(ctx, sourceTenant, deleted); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatal("old deletion receipt replay accepted changed-then-empty state")
	}
	if err := s.Update(ctx, sourceTenant, oldEditor); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("stale pre-delete editor resurrected")
	}
	newPage := create(sourceTenant, "/aba-new", "New deletion")
	state, _ = s.ReadPageState(ctx, sourceTenant)
	deleted, err = s.ApplyPageBatch(ctx, sourceTenant, store.BatchFor(state, []store.PageMutation{{Key: "aba-new-delete", Kind: "delete", TargetID: newPage.ID, ExpectedVersion: newPage.Version}}))
	if err != nil {
		t.Fatal(err)
	}
	temporary := create(sourceTenant, "/temporary", "Created and deleted")
	if s.Delete(ctx, sourceTenant, temporary.ID, temporary.Version) != nil {
		t.Fatal("create/delete fixture failed")
	}
	if _, err := s.RollbackPageBatch(ctx, sourceTenant, deleted); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatal("create/delete history disappeared from rollback guard")
	}
	if _, err := s.RollbackPageBatch(ctx, targetTenant, deleted); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatal("receipt target content scope not bound")
	}
	return deleted
}
