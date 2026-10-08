// Package storetest provides the same mutation behavior contract for memory and
// real Postgres tests. It writes only the fresh tenant IDs supplied by the test.
package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

type BatchStore interface {
	store.PageStore
	store.PageBatchStore
}

func PageBatches(t *testing.T, s BatchStore, targetTenant, sourceTenant int64) {
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
	all, err := s.List(ctx, targetTenant, "")
	if err != nil {
		t.Fatal(err)
	}
	baseline := store.Baseline(store.States(all))
	content := store.ContentOf(review)
	news := store.PageContent{Path: "/news", Title: "New page", BodyHTML: "<p>News</p>", Status: store.StatusPublished}
	batch := store.PageBatch{Baseline: baseline, Mutations: []store.PageMutation{{Key: "review:home", Kind: "update", TargetID: home.ID, ExpectedVersion: home.Version, Content: &content}, {Key: "review:news", Kind: "create", Content: &news}, {Key: "explicit:remove", Kind: "delete", TargetID: removed.ID, ExpectedVersion: removed.Version}}}
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
	all, _ = s.List(ctx, targetTenant, "")
	batch.Baseline = store.Baseline(store.States(all))
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
	all, _ = s.List(ctx, targetTenant, "")
	next := store.ContentOf(fresh)
	next.Title = "Promotion race winner"
	raceBatch := store.PageBatch{Baseline: store.Baseline(store.States(all)), Mutations: []store.PageMutation{{Key: "race", Kind: "update", TargetID: fresh.ID, ExpectedVersion: fresh.Version, Content: &next}}}
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
	all, _ = s.List(ctx, targetTenant, "")
	next = store.ContentOf(fresh)
	next.Title = "Delete race promotion"
	raceBatch = store.PageBatch{Baseline: store.Baseline(store.States(all)), Mutations: []store.PageMutation{{Key: "delete-race", Kind: "update", TargetID: fresh.ID, ExpectedVersion: fresh.Version, Content: &next}}}
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
}
