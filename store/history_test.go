package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
	"github.com/GoCodeAlone/workflow-plugin-cms/store/storetest"
)

func TestMemorySavedPageHistory(t *testing.T) {
	storetest.PageHistory(t, store.NewMemoryPageStore(), 1, 2)
}

func TestHistoryActorAndSemanticDigest(t *testing.T) {
	for _, actor := range []string{"", "\n", "user\x00injected", strings.Repeat("x", 257)} {
		e := store.NewPageHistoryEntry(store.WithPageWriteActor(context.Background(), actor), "scope", 1, "create", nil, nil, time.Now())
		if e.Actor != "unattributed" {
			t.Fatal("invalid actor accepted")
		}
	}
	e := store.NewPageHistoryEntry(context.Background(), "scope", 1, "create", nil, nil, time.Now())
	if e.Actor != "unattributed" {
		t.Fatal("missing identity fabricated")
	}
	changed := e
	changed.Actor = "other"
	if e.ContentDigest() == changed.ContentDigest() {
		t.Fatal("actor not bound by digest")
	}
	changed = e
	changed.Revision++
	if e.ContentDigest() == changed.ContentDigest() {
		t.Fatal("revision not bound by digest")
	}
}
