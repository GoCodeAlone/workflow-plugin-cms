package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
)

var ErrHistoryUnavailable = errors.New("page history unavailable; no write committed")
var ErrHistoryQuery = errors.New("invalid page history query")

type pageWriteActorKey struct{}

// WithPageWriteActor carries the host's authenticated stable identity into a
// store transaction. It grants no authority. Never populate it from a request
// body, arbitrary header, or unverified token claim.
func WithPageWriteActor(ctx context.Context, actor string) context.Context {
	actor = strings.TrimSpace(actor)
	if actor == "" || len(actor) > 256 || strings.ContainsFunc(actor, unicode.IsControl) {
		actor = "unattributed"
	}
	return context.WithValue(ctx, pageWriteActorKey{}, actor)
}

func pageWriteActor(ctx context.Context) string {
	if actor, ok := ctx.Value(pageWriteActorKey{}).(string); ok {
		return actor
	}
	return "unattributed"
}

// PageHistoryEntry records only pages touched by one committed write. Deleted
// pages remain in Before. Versions, blocks, schedules and templates are saved
// content; Scope and Revision identify the durable tenant content namespace.
// Digest detects corruption; it is not a signature or proof of authorization.
type PageHistoryEntry struct {
	Scope       string      `json:"scope"`
	Revision    int64       `json:"revision"`
	CommittedAt time.Time   `json:"committed_at"`
	Actor       string      `json:"actor"`
	Operation   string      `json:"operation"`
	Before      []PageState `json:"before"`
	After       []PageState `json:"after"`
	Digest      string      `json:"digest"`
}

func (e PageHistoryEntry) ContentDigest() string {
	// Bind semantic content digests so JSONB key ordering does not break reads.
	v := struct {
		Scope       string         `json:"scope"`
		Revision    int64          `json:"revision"`
		CommittedAt time.Time      `json:"committed_at"`
		Actor       string         `json:"actor"`
		Operation   string         `json:"operation"`
		Before      []PageBaseline `json:"before"`
		After       []PageBaseline `json:"after"`
	}{e.Scope, e.Revision, e.CommittedAt.UTC(), e.Actor, e.Operation, Baseline(e.Before), Baseline(e.After)}
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func NewPageHistoryEntry(ctx context.Context, scope string, revision int64, operation string, before, after []PageState, now time.Time) PageHistoryEntry {
	e := PageHistoryEntry{scope, revision, now.UTC().Truncate(time.Microsecond), pageWriteActor(ctx), operation, cloneStates(before), cloneStates(after), ""}
	e.Digest = e.ContentDigest()
	return e
}

func cloneStates(states []PageState) []PageState {
	out := make([]PageState, 0, len(states))
	for _, p := range states {
		out = append(out, PageState{p.ID, p.Version, ContentOf(p.Content.Page(1, p.ID, p.Version))})
	}
	return out
}

// ChangedPageStates includes version-only saves and excludes untouched pages.
func ChangedPageStates(before, after []PageState) ([]PageState, []PageState) {
	b, a := map[int64]PageState{}, map[int64]PageState{}
	for _, p := range before {
		b[p.ID] = p
	}
	for _, p := range after {
		a[p.ID] = p
	}
	changed := func(p PageState, other map[int64]PageState) bool {
		o, ok := other[p.ID]
		return !ok || p.Version != o.Version || p.Content.Digest() != o.Content.Digest()
	}
	var old, saved []PageState
	for _, p := range before {
		if changed(p, a) {
			old = append(old, p)
		}
	}
	for _, p := range after {
		if changed(p, b) {
			saved = append(saved, p)
		}
	}
	return cloneStates(old), cloneStates(saved)
}

type PageHistoryQuery struct {
	AfterRevision int64
	Limit         int
}

func (q PageHistoryQuery) Validate() error {
	if q.AfterRevision < 0 || q.Limit < 1 || q.Limit > 100 {
		return ErrHistoryQuery
	}
	return nil
}

// HistoryStartRevision is the head at adoption. No entries before it are
// claimed or fabricated. CurrentRevision is read atomically with this page of
// history. NextRevision is an exclusive cursor; HasMore signals another page.
type PageHistory struct {
	Scope                string             `json:"scope"`
	HistoryStartRevision int64              `json:"history_start_revision"`
	CurrentRevision      int64              `json:"current_revision"`
	Entries              []PageHistoryEntry `json:"entries"`
	NextRevision         int64              `json:"next_revision"`
	HasMore              bool               `json:"has_more"`
}

type PageHistoryStore interface {
	ReadPageHistory(context.Context, int64, PageHistoryQuery) (PageHistory, error)
}

func (s *MemoryPageStore) recordHistoryLocked(ctx context.Context, tenantID int64, operation string, before, after []PageState) {
	s.history[tenantID] = append(s.history[tenantID], NewPageHistoryEntry(ctx, s.scopeLocked(tenantID), s.revisions[tenantID], operation, before, after, time.Now()))
}

func (s *MemoryPageStore) ReadPageHistory(_ context.Context, tenantID int64, q PageHistoryQuery) (PageHistory, error) {
	if tenantID <= 0 {
		return PageHistory{}, ErrNotFound
	}
	if err := q.Validate(); err != nil {
		return PageHistory{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.scopes[tenantID] == "" {
		return PageHistory{}, ErrNotFound
	}
	h := PageHistory{Scope: s.scopes[tenantID], CurrentRevision: s.revisions[tenantID], Entries: []PageHistoryEntry{}, NextRevision: q.AfterRevision}
	for _, entry := range s.history[tenantID] {
		if entry.Revision <= q.AfterRevision {
			continue
		}
		if len(h.Entries) == q.Limit {
			h.HasMore = true
			break
		}
		e := entry
		e.Before, e.After = cloneStates(e.Before), cloneStates(e.After)
		h.Entries = append(h.Entries, e)
		h.NextRevision = e.Revision
	}
	return h, nil
}

var _ PageHistoryStore = (*MemoryPageStore)(nil)
