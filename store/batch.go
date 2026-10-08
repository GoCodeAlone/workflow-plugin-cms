package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

func idString(id int64) string { return strconv.FormatInt(id, 10) }

var ErrBatchInvalid = errors.New("page batch invalid")

// PageContent is an explicit content allowlist; it carries no tenant authority.
type PageContent struct {
	Subsite     string          `json:"subsite"`
	Path        string          `json:"path"`
	Title       string          `json:"title"`
	BodyHTML    string          `json:"body_html"`
	BodyBlocks  json.RawMessage `json:"body_blocks,omitempty"`
	Status      PageStatus      `json:"status"`
	TemplateID  string          `json:"template_id,omitempty"`
	PublishAt   *time.Time      `json:"publish_at,omitempty"`
	UnpublishAt *time.Time      `json:"unpublish_at,omitempty"`
}

func ContentOf(p *Page) PageContent {
	c := PageContent{p.Subsite, p.Path, p.Title, p.BodyHTML, append(json.RawMessage(nil), p.BodyBlocks...), p.Status, p.TemplateID, cloneTime(p.PublishAt), cloneTime(p.UnpublishAt)}
	return c
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC()
	return &v
}

// Page reconstructs content in the caller's authorized tenant, not a payload tenant.
func (c PageContent) Page(tenantID, id int64, version int) *Page {
	return &Page{ID: id, TenantID: tenantID, Subsite: c.Subsite, Path: c.Path, Title: c.Title, BodyHTML: c.BodyHTML, BodyBlocks: append(json.RawMessage(nil), c.BodyBlocks...), Status: c.Status, TemplateID: c.TemplateID, PublishAt: cloneTime(c.PublishAt), UnpublishAt: cloneTime(c.UnpublishAt), Version: version}
}

func (c PageContent) Validate() error {
	if !strings.HasPrefix(c.Path, "/") || path.Clean(c.Path) != c.Path || strings.ContainsAny(c.Path, "?#\\\x00") || strings.TrimSpace(c.Title) == "" || strings.TrimSpace(c.Subsite) != c.Subsite {
		return ErrBatchInvalid
	}
	if len(c.BodyBlocks) > 0 && !json.Valid(c.BodyBlocks) {
		return ErrBatchInvalid
	}
	if c.Status != StatusDraft && c.Status != StatusPublished && c.Status != StatusScheduled && c.Status != StatusArchived {
		return ErrBatchInvalid
	}
	if c.Status == StatusScheduled && c.PublishAt == nil {
		return ErrBatchInvalid
	}
	if c.PublishAt != nil && c.PublishAt.IsZero() || c.UnpublishAt != nil && c.UnpublishAt.IsZero() {
		return ErrBatchInvalid
	}
	if c.PublishAt != nil && c.UnpublishAt != nil && !c.UnpublishAt.After(*c.PublishAt) {
		return ErrBatchInvalid
	}
	return nil
}

func (c PageContent) Digest() string {
	// JSONB may reorder object keys; normalize blocks before computing a baseline.
	if len(c.BodyBlocks) > 0 {
		var value any
		d := json.NewDecoder(strings.NewReader(string(c.BodyBlocks)))
		d.UseNumber()
		if d.Decode(&value) == nil {
			c.BodyBlocks, _ = json.Marshal(value)
		}
	}
	c.PublishAt, c.UnpublishAt = cloneTime(c.PublishAt), cloneTime(c.UnpublishAt)
	b, _ := json.Marshal(c)
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:])
}

type PageBaseline struct {
	ID      int64  `json:"id"`
	Version int    `json:"version"`
	Digest  string `json:"digest"`
}

type PageState struct {
	ID      int64       `json:"id"`
	Version int         `json:"version"`
	Content PageContent `json:"content"`
}

func States(pages []*Page) []PageState {
	out := make([]PageState, 0, len(pages))
	for _, p := range pages {
		out = append(out, PageState{p.ID, p.Version, ContentOf(p)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func Baseline(states []PageState) []PageBaseline {
	out := make([]PageBaseline, 0, len(states))
	for _, p := range states {
		out = append(out, PageBaseline{p.ID, p.Version, p.Content.Digest()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// PageMutation never copies a source ID into a target ID implicitly.
type PageMutation struct {
	Key             string       `json:"key"`
	Kind            string       `json:"kind"` // create, update, delete; restore is rollback-only
	TargetID        int64        `json:"target_id"`
	ExpectedVersion int          `json:"expected_version"`
	Content         *PageContent `json:"content,omitempty"`
}
type PageBatch struct {
	TargetScope      string         `json:"target_scope"`
	BaselineRevision int64          `json:"baseline_revision"`
	Baseline         []PageBaseline `json:"baseline"`
	Mutations        []PageMutation `json:"mutations"`
}
type PageBatchReceipt struct {
	TargetScope    string           `json:"target_scope"`
	BeforeRevision int64            `json:"before_revision"`
	AfterRevision  int64            `json:"after_revision"`
	Before         []PageState      `json:"before"`
	After          []PageState      `json:"after"`
	Mapping        map[string]int64 `json:"mapping"`
	Digest         string           `json:"digest"`
}

// Seal detects archive corruption. It is not a signature or authorization.
func (r *PageBatchReceipt) Seal() {
	r.Digest = ""
	b, _ := json.Marshal(r)
	h := sha256.Sum256(b)
	r.Digest = hex.EncodeToString(h[:])
}

// PageBatchStore is internal persistence, not an authorized publication API.
// The host must provide approval, fencing, verified bundles and durable backups.
type PageBatchStore interface {
	ReadPageState(context.Context, int64) (PageSet, error)
	ApplyPageBatch(context.Context, int64, PageBatch) (PageBatchReceipt, error)
	RollbackPageBatch(context.Context, int64, PageBatchReceipt) (PageBatchReceipt, error)
}

// PageSet is read atomically in the invocation's authorized tenant. Scope is a
// durable content namespace, never a permission or copied tenant setting.
type PageSet struct {
	Scope    string      `json:"scope"`
	Revision int64       `json:"revision"`
	Pages    []PageState `json:"pages"`
}

func BatchFor(state PageSet, mutations []PageMutation) PageBatch {
	return PageBatch{TargetScope: state.Scope, BaselineRevision: state.Revision, Baseline: Baseline(state.Pages), Mutations: mutations}
}

// CheckPageBatch compares the entire tenant baseline and checks the final paths
// before any mutation. Omitted pages remain untouched. restore is never accepted
// by ordinary apply; it is generated only by the guarded rollback implementation.
func CheckPageBatch(state PageSet, batch PageBatch, allowRestore bool) error {
	current := state.Pages
	if state.Scope == "" || state.Scope != batch.TargetScope || state.Revision != batch.BaselineRevision || !reflect.DeepEqual(Baseline(current), batch.Baseline) {
		return ErrVersionConflict
	}
	if len(batch.Mutations) == 0 {
		return ErrBatchInvalid
	}
	pages := map[int64]PageState{}
	for _, p := range current {
		pages[p.ID] = p
	}
	keys := map[string]bool{}
	targets := map[int64]bool{}
	creates := []PageContent{}
	for _, m := range batch.Mutations {
		if m.Key == "" || keys[m.Key] {
			return ErrBatchInvalid
		}
		keys[m.Key] = true
		if m.TargetID != 0 && targets[m.TargetID] {
			return ErrBatchInvalid
		}
		targets[m.TargetID] = true
		switch m.Kind {
		case "create":
			if m.TargetID != 0 || m.ExpectedVersion != 0 || m.Content == nil {
				return ErrBatchInvalid
			}
			creates = append(creates, *m.Content)
		case "update", "delete":
			p, ok := pages[m.TargetID]
			if !ok || m.ExpectedVersion <= 0 || p.Version != m.ExpectedVersion {
				return ErrVersionConflict
			}
			if m.Kind == "delete" {
				if m.Content != nil {
					return ErrBatchInvalid
				}
				delete(pages, m.TargetID)
			} else {
				if m.Content == nil {
					return ErrBatchInvalid
				}
				p.Content = *m.Content
				pages[m.TargetID] = p
			}
		case "restore":
			if !allowRestore || m.TargetID <= 0 || m.ExpectedVersion <= 0 || m.Content == nil {
				return ErrBatchInvalid
			}
			if _, ok := pages[m.TargetID]; ok {
				return ErrVersionConflict
			}
			pages[m.TargetID] = PageState{m.TargetID, m.ExpectedVersion, *m.Content}
		default:
			return ErrBatchInvalid
		}
	}
	paths := map[string]bool{}
	check := func(c PageContent) error {
		if err := c.Validate(); err != nil {
			return err
		}
		key := c.Subsite + "\x00" + c.Path
		if paths[key] {
			return ErrPathConflict
		}
		paths[key] = true
		return nil
	}
	for _, p := range pages {
		if err := check(p.Content); err != nil {
			return err
		}
	}
	for _, c := range creates {
		if err := check(c); err != nil {
			return err
		}
	}
	return nil
}

func RollbackBatch(r PageBatchReceipt) (PageBatch, error) {
	copy := r
	copy.Seal()
	if r.Digest == "" || r.Digest != copy.Digest || r.TargetScope == "" || r.BeforeRevision < 0 || r.AfterRevision <= r.BeforeRevision {
		return PageBatch{}, ErrBatchInvalid
	}
	before := map[int64]PageState{}
	after := map[int64]PageState{}
	for _, p := range r.Before {
		if p.ID <= 0 || p.Version <= 0 || p.Content.Validate() != nil {
			return PageBatch{}, ErrBatchInvalid
		}
		if _, ok := before[p.ID]; ok {
			return PageBatch{}, ErrBatchInvalid
		}
		before[p.ID] = p
	}
	for _, p := range r.After {
		if p.ID <= 0 || p.Version <= 0 || p.Content.Validate() != nil {
			return PageBatch{}, ErrBatchInvalid
		}
		if _, ok := after[p.ID]; ok {
			return PageBatch{}, ErrBatchInvalid
		}
		after[p.ID] = p
	}
	b := PageBatch{TargetScope: r.TargetScope, BaselineRevision: r.AfterRevision, Baseline: Baseline(r.After)}
	for _, p := range r.After {
		old, ok := before[p.ID]
		if !ok {
			b.Mutations = append(b.Mutations, PageMutation{Key: "rollback:" + idString(p.ID), Kind: "delete", TargetID: p.ID, ExpectedVersion: p.Version})
		} else if old.Content.Digest() != p.Content.Digest() {
			c := old.Content
			b.Mutations = append(b.Mutations, PageMutation{Key: "rollback:" + idString(p.ID), Kind: "update", TargetID: p.ID, ExpectedVersion: p.Version, Content: &c})
		}
	}
	for _, p := range r.Before {
		if _, ok := after[p.ID]; !ok {
			c := p.Content
			b.Mutations = append(b.Mutations, PageMutation{Key: "rollback:" + idString(p.ID), Kind: "restore", TargetID: p.ID, ExpectedVersion: p.Version + 1, Content: &c})
		}
	}
	return b, nil
}
