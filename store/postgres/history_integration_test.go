package postgres

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
	"github.com/GoCodeAlone/workflow-plugin-cms/store/storetest"
	"github.com/jackc/pgx/v5/pgxpool"
)

func historyTenant(t *testing.T, s *Store, slug string) int64 {
	t.Helper()
	tenant := &store.Tenant{Slug: slug}
	if err := s.CreateTenant(context.Background(), tenant); err != nil {
		t.Fatal("isolated tenant failed")
	}
	return tenant.ID
}

func TestPostgresSavedPageHistory(t *testing.T) {
	s := isolatedPostgres(t)
	tenantID := historyTenant(t, s, "saved-history")
	otherID := historyTenant(t, s, "other-history")
	storetest.PageHistory(t, s, tenantID, otherID)
	before, err := s.ReadPageHistory(context.Background(), tenantID, store.PageHistoryQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	// Reconnect to the same task-owned schema: deleted snapshots and actor
	// history must survive loss of all in-memory store state.
	pool, err := pgxpool.NewWithConfig(context.Background(), s.pool.Config())
	if err != nil {
		t.Fatal("fresh isolated pool failed")
	}
	defer pool.Close()
	after, err := NewWithPool(pool).ReadPageHistory(context.Background(), tenantID, store.PageHistoryQuery{Limit: 100})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("saved history lost after reconnect")
	}
	for _, query := range []string{
		"UPDATE cms_page_history SET actor='rewritten'",
		"DELETE FROM cms_page_history",
		"TRUNCATE cms_page_history",
	} {
		if _, err := s.pool.Exec(context.Background(), query); err == nil {
			t.Fatal("append-only history rewritable")
		}
	}
}

func TestPostgresHistoryAdoptionDoesNotInventPast(t *testing.T) {
	s := isolatedPostgresHistory(t, false)
	ctx := context.Background()
	tenantID := historyTenant(t, s, "existing-before-history")
	// Simulate the previous released store before history is installed.
	var pageID int64
	if err := s.pool.QueryRow(ctx, "INSERT INTO pages(tenant_id,path,title,status) VALUES($1,'/existing','Existing','draft') RETURNING id", tenantID).Scan(&pageID); err != nil {
		t.Fatal("prehistory fixture failed")
	}
	if _, err := s.pool.Exec(ctx, "UPDATE cms_page_revisions SET revision=7 WHERE tenant_id=$1", tenantID); err != nil {
		t.Fatal("prehistory ledger failed")
	}
	migration, err := os.ReadFile("migrations/0002_page_saved_history.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal("isolated history adoption failed")
	}
	h, err := s.ReadPageHistory(ctx, tenantID, store.PageHistoryQuery{Limit: 20})
	if err != nil || h.HistoryStartRevision != 7 || h.CurrentRevision != 7 || len(h.Entries) != 0 {
		t.Fatal("historical actors or snapshots fabricated")
	}
	p, err := s.Get(ctx, tenantID, pageID)
	if err != nil {
		t.Fatal(err)
	}
	p.Title = "First recorded save"
	if err := s.Update(store.WithPageWriteActor(ctx, "user:first-recorded"), tenantID, p); err != nil {
		t.Fatal(err)
	}
	h, err = s.ReadPageHistory(ctx, tenantID, store.PageHistoryQuery{Limit: 20})
	if err != nil || len(h.Entries) != 1 || h.Entries[0].Revision != 8 || h.Entries[0].Before[0].Content.Title != "Existing" || h.Entries[0].Actor != "user:first-recorded" {
		t.Fatal("adoption marker or first saved event incorrect")
	}
}

func TestPostgresHistoryFailureRollsBackEveryWrite(t *testing.T) {
	s := isolatedPostgres(t)
	ctx := context.Background()
	tenantID := historyTenant(t, s, "history-failure")
	p := &store.Page{Path: "/", Title: "Before", Status: store.StatusDraft}
	if err := s.Create(ctx, tenantID, p); err != nil {
		t.Fatal(err)
	}
	state, err := s.ReadPageState(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	content := store.PageContent{Path: "/batch", Title: "Batch", Status: store.StatusDraft}
	receipt, err := s.ApplyPageBatch(ctx, tenantID, store.BatchFor(state, []store.PageMutation{{Key: "created", Kind: "create", Content: &content}}))
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadPageState(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "DROP TABLE cms_page_history"); err != nil {
		t.Fatal("isolated missing-history fixture failed")
	}
	p.Title = "Must not save"
	if err := s.Update(ctx, tenantID, p); !errors.Is(err, store.ErrHistoryUnavailable) {
		t.Fatal("update bypassed missing history")
	}
	if err := s.Delete(ctx, tenantID, p.ID, 1); !errors.Is(err, store.ErrHistoryUnavailable) {
		t.Fatal("delete bypassed missing history")
	}
	if err := s.Create(ctx, tenantID, &store.Page{Path: "/new", Title: "Must not save", Status: store.StatusDraft}); !errors.Is(err, store.ErrHistoryUnavailable) {
		t.Fatal("create bypassed missing history")
	}
	content.Path = "/must-not-save"
	if _, err := s.ApplyPageBatch(ctx, tenantID, store.BatchFor(before, []store.PageMutation{{Key: "refused", Kind: "create", Content: &content}})); !errors.Is(err, store.ErrHistoryUnavailable) {
		t.Fatal("batch bypassed missing history")
	}
	if _, err := s.RollbackPageBatch(ctx, tenantID, receipt); !errors.Is(err, store.ErrHistoryUnavailable) {
		t.Fatal("rollback bypassed missing history")
	}
	after, err := s.ReadPageState(ctx, tenantID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("history failure partly committed page or revision")
	}
	if _, err := s.ReadPageHistory(ctx, tenantID, store.PageHistoryQuery{Limit: 20}); !errors.Is(err, store.ErrHistoryUnavailable) {
		t.Fatal("missing history reported as empty complete history")
	}
}

func TestPostgresMixedWriterGapRefuses(t *testing.T) {
	s := isolatedPostgres(t)
	ctx := context.Background()
	tenantID := historyTenant(t, s, "mixed-writer")
	p := &store.Page{Path: "/", Title: "Before", Status: store.StatusDraft}
	if err := s.Create(ctx, tenantID, p); err != nil {
		t.Fatal(err)
	}
	// A superseded writer advanced only the prior revision ledger.
	if _, err := s.pool.Exec(ctx, "UPDATE cms_page_revisions SET revision=revision+1 WHERE tenant_id=$1", tenantID); err != nil {
		t.Fatal("isolated mixed-writer fixture failed")
	}
	p.Title = "Must refuse"
	if err := s.Update(ctx, tenantID, p); !errors.Is(err, store.ErrHistoryUnavailable) {
		t.Fatal("unrecorded revision gap accepted")
	}
	got, err := s.Get(ctx, tenantID, p.ID)
	if err != nil || got.Title != "Before" || got.Version != 1 {
		t.Fatal("gap refusal committed content")
	}
	if _, err := s.ReadPageHistory(ctx, tenantID, store.PageHistoryQuery{Limit: 20}); !errors.Is(err, store.ErrHistoryUnavailable) {
		t.Fatal("gap concealed by history reader")
	}
}

func TestPostgresConcurrentSaveRecordsOneWinner(t *testing.T) {
	s := isolatedPostgres(t)
	ctx := context.Background()
	tenantID := historyTenant(t, s, "concurrent-history")
	p := &store.Page{Path: "/", Title: "Loaded", Status: store.StatusDraft}
	if err := s.Create(ctx, tenantID, p); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, title := range []string{"Owner save", "Agent save"} {
		loaded, err := s.Get(ctx, tenantID, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		loaded.Title = title
		group.Add(1)
		go func(page *store.Page) {
			defer group.Done()
			<-ready
			results <- s.Update(store.WithPageWriteActor(ctx, page.Title), tenantID, page)
		}(loaded)
	}
	close(ready)
	group.Wait()
	close(results)
	saved, refused := 0, 0
	for err := range results {
		if err == nil {
			saved++
		} else if errors.Is(err, store.ErrVersionConflict) {
			refused++
		} else {
			t.Fatal(err)
		}
	}
	h, err := s.ReadPageHistory(ctx, tenantID, store.PageHistoryQuery{Limit: 20})
	got, getErr := s.Get(ctx, tenantID, p.ID)
	if saved != 1 || refused != 1 || err != nil || getErr != nil || len(h.Entries) != 2 || h.CurrentRevision != 2 || h.Entries[1].After[0].Content.Title != got.Title || h.Entries[1].Actor != got.Title {
		t.Fatal("concurrent save overwrote winner or recorded a refused write")
	}
}

func TestPostgresHistoryInsertFailureIsAtomic(t *testing.T) {
	s := isolatedPostgres(t)
	ctx := context.Background()
	tenantID := historyTenant(t, s, "insert-history-failure")
	p := &store.Page{Path: "/", Title: "Before", Status: store.StatusDraft}
	if err := s.Create(ctx, tenantID, p); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadPageState(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	historyBefore, err := s.ReadPageHistory(ctx, tenantID, store.PageHistoryQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	// Inject a late failure at the actual history INSERT boundary after all
	// batch page writes and the tenant revision increment have executed.
	_, err = s.pool.Exec(ctx, "CREATE FUNCTION refuse_history_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture refusal'; END $$; CREATE TRIGGER refuse_history_insert BEFORE INSERT ON cms_page_history FOR EACH ROW EXECUTE FUNCTION refuse_history_insert();")
	if err != nil {
		t.Fatal("isolated history failure trigger failed")
	}
	update := store.ContentOf(p)
	update.Title = "Must roll back"
	create := store.PageContent{Path: "/new", Title: "Must also roll back", Status: store.StatusDraft}
	batch := store.BatchFor(before, []store.PageMutation{{Key: "update", Kind: "update", TargetID: p.ID, ExpectedVersion: p.Version, Content: &update}, {Key: "create", Kind: "create", Content: &create}})
	if _, err := s.ApplyPageBatch(ctx, tenantID, batch); !errors.Is(err, store.ErrHistoryUnavailable) {
		t.Fatal("late history failure not refused")
	}
	after, err := s.ReadPageState(ctx, tenantID)
	historyAfter, historyErr := s.ReadPageHistory(ctx, tenantID, store.PageHistoryQuery{Limit: 20})
	if err != nil || historyErr != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(historyBefore, historyAfter) {
		t.Fatal("late history failure partially committed")
	}
}
