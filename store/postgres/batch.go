package postgres

import (
	"context"
	"errors"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type pageQueries interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// This lock covers all CMS page mutation paths. It is cooperative, not a claim
// that arbitrary external SQL writers participate or that bundle swaps are atomic.
func (s *Store) pageTransaction(ctx context.Context, tenantID int64) (pgx.Tx, error) {
	if tenantID <= 0 {
		return nil, store.ErrNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('cms.pages:' || $1::bigint::text, 0))`, tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func pageMiss(ctx context.Context, q pageQueries, tenantID, id int64) error {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pages WHERE tenant_id=$1 AND id=$2)`, tenantID, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return store.ErrVersionConflict
	}
	return store.ErrNotFound
}

func statesInTransaction(ctx context.Context, tx pgx.Tx, tenantID int64) ([]store.PageState, error) {
	rows, err := tx.Query(ctx, pageSelectSQL()+` WHERE tenant_id=$1 ORDER BY id FOR UPDATE`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pages []*store.Page
	for rows.Next() {
		p := &store.Page{}
		if err := scanPage(rows, p); err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return store.States(pages), nil
}

func (s *Store) ApplyPageBatch(ctx context.Context, tenantID int64, batch store.PageBatch) (store.PageBatchReceipt, error) {
	return s.applyBatch(ctx, tenantID, batch, false)
}

func (s *Store) RollbackPageBatch(ctx context.Context, tenantID int64, receipt store.PageBatchReceipt) (store.PageBatchReceipt, error) {
	batch, err := store.RollbackBatch(receipt)
	if err != nil {
		return store.PageBatchReceipt{}, err
	}
	return s.applyBatch(ctx, tenantID, batch, true)
}

func (s *Store) applyBatch(ctx context.Context, tenantID int64, batch store.PageBatch, restore bool) (store.PageBatchReceipt, error) {
	tx, err := s.pageTransaction(ctx, tenantID)
	if err != nil {
		return store.PageBatchReceipt{}, err
	}
	defer tx.Rollback(ctx)
	before, err := statesInTransaction(ctx, tx, tenantID)
	if err != nil {
		return store.PageBatchReceipt{}, store.ErrBatchInvalid
	}
	if err = store.CheckPageBatch(before, batch, restore); err != nil {
		return store.PageBatchReceipt{}, err
	}
	result := store.PageBatchReceipt{Before: before, Mapping: map[string]int64{}}
	// Vacate moved paths inside this transaction so swaps work despite immediate
	// unique constraints. Validated final content replaces every temporary path.
	for _, m := range batch.Mutations {
		if m.Kind == "update" {
			if _, err = tx.Exec(ctx, `UPDATE pages SET path='/__cms_batch_internal__/' || id::text WHERE tenant_id=$1 AND id=$2 AND version=$3`, tenantID, m.TargetID, m.ExpectedVersion); err != nil {
				return store.PageBatchReceipt{}, store.ErrBatchInvalid
			}
		}
	}
	for _, m := range batch.Mutations {
		if m.Kind == "delete" {
			if err = deletePage(ctx, tx, tenantID, m.TargetID, m.ExpectedVersion); err != nil {
				return store.PageBatchReceipt{}, batchError(err)
			}
			result.Mapping[m.Key] = m.TargetID
		}
	}
	for _, m := range batch.Mutations {
		if m.Kind == "delete" {
			continue
		}
		p := m.Content.Page(tenantID, m.TargetID, m.ExpectedVersion)
		switch m.Kind {
		case "create":
			err = createPage(ctx, tx, tenantID, p)
		case "update":
			err = updatePage(ctx, tx, tenantID, p)
		case "restore":
			err = scanPage(tx.QueryRow(ctx, `INSERT INTO pages (id,tenant_id,subsite,path,title,body_html,body_blocks,status,template_id,publish_at,unpublish_at,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12) RETURNING id,tenant_id,COALESCE(subsite,''),path,title,COALESCE(body_html,''),body_blocks,status,COALESCE(template_id,''),publish_at,unpublish_at,version,created_at,updated_at`, p.ID, tenantID, p.Subsite, p.Path, p.Title, p.BodyHTML, nullableJSON(p.BodyBlocks), p.Status, p.TemplateID, nullableTime(p.PublishAt), nullableTime(p.UnpublishAt), m.ExpectedVersion), p)
		}
		if err != nil {
			return store.PageBatchReceipt{}, batchError(err)
		}
		result.Mapping[m.Key] = p.ID
	}
	result.After, err = statesInTransaction(ctx, tx, tenantID)
	if err != nil {
		return store.PageBatchReceipt{}, store.ErrBatchInvalid
	}
	if err = tx.Commit(ctx); err != nil {
		return store.PageBatchReceipt{}, store.ErrBatchInvalid
	}
	result.Seal()
	return result, nil
}

func batchError(err error) error {
	if errors.Is(err, store.ErrVersionConflict) || errors.Is(err, store.ErrPathConflict) || errors.Is(err, store.ErrNotFound) {
		return err
	}
	return store.ErrBatchInvalid
}

var _ store.PageBatchStore = (*Store)(nil)
