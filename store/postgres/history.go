package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
	"github.com/jackc/pgx/v5"
)

func pageBefore(ctx context.Context, tx pgx.Tx, tenantID, pageID int64) ([]store.PageState, error) {
	p := &store.Page{}
	err := scanPage(tx.QueryRow(ctx, pageSelectSQL()+` WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, pageID), p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return store.States([]*store.Page{p}), nil
}

func appendPageHistory(ctx context.Context, tx pgx.Tx, tenantID, revision int64, operation string, before, after []store.PageState) error {
	var scope string
	if err := tx.QueryRow(ctx, `SELECT content_scope FROM cms_page_revisions WHERE tenant_id=$1`, tenantID).Scan(&scope); err != nil {
		return store.ErrHistoryUnavailable
	}
	e := store.NewPageHistoryEntry(ctx, scope, revision, operation, before, after, time.Now())
	old, err := json.Marshal(e.Before)
	if err != nil {
		return store.ErrHistoryUnavailable
	}
	saved, err := json.Marshal(e.After)
	if err != nil {
		return store.ErrHistoryUnavailable
	}
	// Refuse gaps caused by mixed old/new cooperative writers. Adoption starts
	// at the explicit marker; later appends require the previous saved event.
	tag, err := tx.Exec(ctx, `INSERT INTO cms_page_history (tenant_id,content_scope,revision,committed_at,actor,operation,before_pages,after_pages,digest)
		SELECT tenant_id,content_scope,$2,$3,$4,$5,$6,$7,$8 FROM cms_page_revisions
		WHERE tenant_id=$1 AND revision=$2 AND
		(history_start_revision=$2-1 OR EXISTS(SELECT 1 FROM cms_page_history h WHERE h.tenant_id=$1 AND h.content_scope=cms_page_revisions.content_scope AND h.revision=$2-1))`, tenantID, revision, e.CommittedAt, e.Actor, e.Operation, old, saved, e.Digest)
	if err != nil || tag.RowsAffected() != 1 {
		return store.ErrHistoryUnavailable
	}
	return nil
}

func (s *Store) ReadPageHistory(ctx context.Context, tenantID int64, q store.PageHistoryQuery) (store.PageHistory, error) {
	if tenantID <= 0 {
		return store.PageHistory{}, store.ErrNotFound
	}
	if err := q.Validate(); err != nil {
		return store.PageHistory{}, err
	}
	// A read-only consistent snapshot does not take the page writer lock.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return store.PageHistory{}, store.ErrHistoryUnavailable
	}
	defer tx.Rollback(ctx)
	h := store.PageHistory{Entries: []store.PageHistoryEntry{}, NextRevision: q.AfterRevision}
	err = tx.QueryRow(ctx, `SELECT content_scope,history_start_revision,revision FROM cms_page_revisions WHERE tenant_id=$1`, tenantID).Scan(&h.Scope, &h.HistoryStartRevision, &h.CurrentRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.PageHistory{}, store.ErrNotFound
	}
	if err != nil || h.HistoryStartRevision > h.CurrentRevision {
		return store.PageHistory{}, store.ErrHistoryUnavailable
	}
	var head int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(revision),$2) FROM cms_page_history WHERE tenant_id=$1 AND content_scope=$3`, tenantID, h.HistoryStartRevision, h.Scope).Scan(&head); err != nil || head != h.CurrentRevision {
		return store.PageHistory{}, store.ErrHistoryUnavailable
	}
	rows, err := tx.Query(ctx, `SELECT content_scope,revision,committed_at,actor,operation,before_pages,after_pages,digest FROM cms_page_history WHERE tenant_id=$1 AND revision>$2 ORDER BY revision LIMIT $3`, tenantID, q.AfterRevision, q.Limit+1)
	if err != nil {
		return store.PageHistory{}, store.ErrHistoryUnavailable
	}
	defer rows.Close()
	previous := q.AfterRevision
	if previous < h.HistoryStartRevision {
		previous = h.HistoryStartRevision
	}
	for rows.Next() {
		var e store.PageHistoryEntry
		var before, after []byte
		if err := rows.Scan(&e.Scope, &e.Revision, &e.CommittedAt, &e.Actor, &e.Operation, &before, &after, &e.Digest); err != nil || json.Unmarshal(before, &e.Before) != nil || json.Unmarshal(after, &e.After) != nil {
			return store.PageHistory{}, store.ErrHistoryUnavailable
		}
		if e.Scope != h.Scope || e.Revision != previous+1 || e.Digest != e.ContentDigest() {
			return store.PageHistory{}, store.ErrHistoryUnavailable
		}
		previous = e.Revision
		if len(h.Entries) == q.Limit {
			h.HasMore = true
			break
		}
		h.Entries = append(h.Entries, e)
		h.NextRevision = e.Revision
	}
	if rows.Err() != nil {
		return store.PageHistory{}, store.ErrHistoryUnavailable
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return store.PageHistory{}, store.ErrHistoryUnavailable
	}
	return h, nil
}

var _ store.PageHistoryStore = (*Store)(nil)
