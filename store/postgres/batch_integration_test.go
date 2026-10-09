package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
	"github.com/GoCodeAlone/workflow-plugin-cms/store/storetest"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Uses a uniquely named schema and drops only that schema. Never uses the
// application's search_path/tables. Connection values are not printed.
func isolatedPostgres(t *testing.T) *Store {
	return isolatedPostgresHistory(t, true)
}

func isolatedPostgresHistory(t *testing.T, includeHistory bool) *Store {
	t.Helper()
	uri := os.Getenv("CMS_TEST_DATABASE_URL")
	if uri == "" {
		t.Skip("CMS_TEST_DATABASE_URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, uri)
	if err != nil {
		t.Fatal("test database configuration invalid")
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("promotion_20261008_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal("could not create isolated test schema")
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error("isolated test schema cleanup failed")
		}
	})
	config, err := pgxpool.ParseConfig(uri)
	if err != nil {
		t.Fatal("test configuration invalid")
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("test pool initialization failed")
	}
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, `CREATE TABLE tenants(id BIGSERIAL PRIMARY KEY,slug TEXT UNIQUE NOT NULL,label TEXT NOT NULL DEFAULT '',theme_id BIGINT,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now()); CREATE TABLE pages(id BIGSERIAL PRIMARY KEY,tenant_id BIGINT NOT NULL REFERENCES tenants(id),subsite TEXT NOT NULL DEFAULT '',path TEXT NOT NULL,title TEXT NOT NULL,body_html TEXT,body_blocks JSONB,status TEXT NOT NULL CHECK(status IN ('draft','published','scheduled','archived')),template_id TEXT,publish_at TIMESTAMPTZ,unpublish_at TIMESTAMPTZ,version INT NOT NULL DEFAULT 1,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),UNIQUE(tenant_id,subsite,path));`)
	if err != nil {
		t.Fatal("isolated test tables failed")
	}
	migration, err := os.ReadFile("migrations/0001_page_content_revision.up.sql")
	if err != nil {
		t.Fatal("required test migration unavailable")
	}
	if _, err = pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal("isolated revision migration failed")
	}
	if includeHistory {
		historyMigration, err := os.ReadFile("migrations/0002_page_saved_history.up.sql")
		if err != nil {
			t.Fatal("required history test migration unavailable")
		}
		if _, err = pool.Exec(ctx, string(historyMigration)); err != nil {
			t.Fatal("isolated history migration failed")
		}
	}
	return NewWithPool(pool)
}

func TestPostgresPageBatchContract(t *testing.T) {
	s := isolatedPostgres(t)
	ctx := context.Background()
	pool := s.pool
	target, source := &store.Tenant{Slug: "test-target"}, &store.Tenant{Slug: "test-review"}
	if s.CreateTenant(ctx, target) != nil || s.CreateTenant(ctx, source) != nil {
		t.Fatal("isolated tenants failed")
	}
	replay := storetest.PageBatches(t, s, target.ID, source.ID)
	// Rebuild with fresh connections: revision history is durable database state.
	restartedPool, err := pgxpool.NewWithConfig(ctx, pool.Config())
	if err != nil {
		t.Fatal("fresh isolated pool failed")
	}
	defer restartedPool.Close()
	restarted := NewWithPool(restartedPool)
	if _, err := restarted.RollbackPageBatch(ctx, source.ID, replay); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatal("durable replay guard lost after store reconstruction")
	}
	// A database-side failure after an earlier update must roll back every item.
	_, err = pool.Exec(ctx, `CREATE FUNCTION refuse_test_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.title='DB refusal test' THEN RAISE EXCEPTION 'test refusal' USING ERRCODE='23514'; END IF; RETURN NEW; END $$; CREATE TRIGGER refuse_test_insert BEFORE INSERT ON pages FOR EACH ROW EXECUTE FUNCTION refuse_test_insert();`)
	if err != nil {
		t.Fatal("isolated failure trigger failed")
	}
	all, err := s.List(ctx, target.ID, "")
	if err != nil || len(all) == 0 {
		t.Fatal("failure fixture unavailable")
	}
	before := store.Baseline(store.States(all))
	changed := store.ContentOf(all[0])
	changed.Title = "Must roll back"
	refused := store.PageContent{Path: "/database-rejected", Title: "DB refusal test", Status: store.StatusDraft}
	state, err := s.ReadPageState(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	batch := store.PageBatch{TargetScope: state.Scope, BaselineRevision: state.Revision, Baseline: before, Mutations: []store.PageMutation{{Key: "updated-first", Kind: "update", TargetID: all[0].ID, ExpectedVersion: all[0].Version, Content: &changed}, {Key: "refused-second", Kind: "create", Content: &refused}}}
	if _, err := s.ApplyPageBatch(ctx, target.ID, batch); err != store.ErrBatchInvalid {
		t.Fatal("database failure was not safely reported")
	}
	all, err = s.List(ctx, target.ID, "")
	if err != nil || !reflect.DeepEqual(before, store.Baseline(store.States(all))) {
		t.Fatal("database failure partially committed content")
	}
}

func TestPostgresMissingRevisionSchemaFailsClosed(t *testing.T) {
	s := isolatedPostgres(t)
	ctx := context.Background()
	tenant := &store.Tenant{Slug: "missing-ledger"}
	if s.CreateTenant(ctx, tenant) != nil {
		t.Fatal("tenant failed")
	}
	p := &store.Page{Path: "/", Title: "Before", Status: store.StatusDraft}
	if s.Create(ctx, tenant.ID, p) != nil {
		t.Fatal("page failed")
	}
	version := p.Version
	if _, err := s.pool.Exec(ctx, `DROP TABLE cms_page_revisions CASCADE`); err != nil {
		t.Fatal("isolated missing-schema fixture failed")
	}
	p.Title = "Must not commit"
	if s.Update(ctx, tenant.ID, p) != store.ErrBatchInvalid {
		t.Fatal("write bypassed missing revision ledger")
	}
	if s.Delete(ctx, tenant.ID, p.ID, version) != store.ErrBatchInvalid {
		t.Fatal("delete bypassed missing revision ledger")
	}
	create := &store.Page{Path: "/uncommitted", Title: "Must not commit", Status: store.StatusDraft}
	if s.Create(ctx, tenant.ID, create) != store.ErrBatchInvalid {
		t.Fatal("create bypassed missing revision ledger")
	}
	got, err := s.Get(ctx, tenant.ID, p.ID)
	if err != nil || got.Version != version || got.Title != "Before" {
		t.Fatal("missing schema partially committed content")
	}
	if _, err := s.GetByPath(ctx, tenant.ID, "", "/uncommitted"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("failed create persisted")
	}
}
