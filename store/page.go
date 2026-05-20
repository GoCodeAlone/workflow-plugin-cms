// Package store defines persistence interfaces + an in-memory test
// implementation for the CMS engine.
//
// Per gocodealone-multisite SPEC.md:
//   V12: per-tenant data writes ! include tenant_id WHERE clause.
//   V15: CMS-rendered page → tenant_id from session; ⊥ from URL/header.
//   V22: page filtered by (tenant_id, subsite_label).
//
// Production wiring: postgres-backed implementation lives in
// gocodealone-multisite (per the host's schema in migrations/0001).
// This package owns the interface and an in-memory store for tests +
// local dev.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// PageStatus is the publish state of a CMS page.
type PageStatus string

const (
	StatusDraft     PageStatus = "draft"
	StatusPublished PageStatus = "published"
)

// Page is a CMS-managed dynamic page for a tenant.
//
// Field semantics:
//   - TenantID:  ! non-zero (V12 multi-tenancy guard)
//   - Subsite:   "" → applies to all subsites; "<label>" → subsite-scoped
//   - Path:      URL path (e.g. "/blog/welcome"); ! unique per (tenant, subsite)
//   - BodyHTML:  rendered HTML for serve-time output (Render result)
//   - BodyBlocks: provider-specific block JSON (source of truth)
//   - Status:    draft | published
//   - Version:   monotonic per-page edit counter
type Page struct {
	ID         int64
	TenantID   int64
	Subsite    string
	Path       string
	Title      string
	BodyHTML   string
	BodyBlocks json.RawMessage
	Status     PageStatus
	Version    int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Validate returns nil iff the page satisfies persistence invariants.
func (p *Page) Validate() error {
	if p.TenantID == 0 {
		return errors.New("page: tenant_id required (V12)")
	}
	if p.Path == "" {
		return errors.New("page: path required")
	}
	if p.Title == "" {
		return errors.New("page: title required")
	}
	if p.Status == "" {
		p.Status = StatusDraft
	}
	if p.Status != StatusDraft && p.Status != StatusPublished {
		return fmt.Errorf("page: invalid status %q", p.Status)
	}
	return nil
}

// ErrNotFound is returned when a page does not exist OR exists in a
// different tenant scope. Callers ! NOT distinguish the two cases —
// leaking tenant existence cross-tenant violates V12/V16.
var ErrNotFound = errors.New("page not found")

// ErrPathConflict is returned when a Create or Update would produce a
// duplicate (tenant_id, subsite, path) tuple.
var ErrPathConflict = errors.New("page path conflict")

// PageStore persists Page records scoped by tenant.
//
// Every method takes tenantID as the FIRST arg AFTER ctx — making
// tenant-scoping impossible to forget at the call site (V12 / V15).
type PageStore interface {
	Create(ctx context.Context, tenantID int64, p *Page) error
	Get(ctx context.Context, tenantID int64, id int64) (*Page, error)
	GetByPath(ctx context.Context, tenantID int64, subsite, path string) (*Page, error)
	Update(ctx context.Context, tenantID int64, p *Page) error
	Delete(ctx context.Context, tenantID int64, id int64) error
	List(ctx context.Context, tenantID int64, subsite string) ([]*Page, error)
}

// MemoryPageStore is an in-memory PageStore for tests + local dev.
// Production uses a postgres-backed implementation that wires the same
// interface.
type MemoryPageStore struct {
	mu    sync.RWMutex
	nextID int64
	pages map[int64]*Page // by ID
}

// NewMemoryPageStore returns an empty in-memory store.
func NewMemoryPageStore() *MemoryPageStore {
	return &MemoryPageStore{pages: map[int64]*Page{}}
}

// Create inserts the page. Returns ErrPathConflict if (tenant, subsite,
// path) is already taken.
func (s *MemoryPageStore) Create(_ context.Context, tenantID int64, p *Page) error {
	if p == nil {
		return errors.New("page: nil")
	}
	p.TenantID = tenantID // enforce caller-supplied (V12)
	if err := p.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.duplicateLocked(tenantID, p.Subsite, p.Path, 0) {
		return ErrPathConflict
	}

	s.nextID++
	p.ID = s.nextID
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.Version == 0 {
		p.Version = 1
	}
	cp := *p
	s.pages[p.ID] = &cp
	return nil
}

// Get returns a copy of the page. tenant_id mismatch → ErrNotFound (no
// leak — V12/V16).
func (s *MemoryPageStore) Get(_ context.Context, tenantID, id int64) (*Page, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.pages[id]
	if !ok || p.TenantID != tenantID {
		return nil, ErrNotFound
	}
	cp := *p
	return &cp, nil
}

// GetByPath looks up by (tenant, subsite, path).
func (s *MemoryPageStore) GetByPath(_ context.Context, tenantID int64, subsite, path string) (*Page, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.pages {
		if p.TenantID == tenantID && p.Subsite == subsite && p.Path == path {
			cp := *p
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

// Update writes the page. Returns ErrNotFound if no record matches
// (tenant_id, id). Bumps Version + UpdatedAt.
func (s *MemoryPageStore) Update(_ context.Context, tenantID int64, p *Page) error {
	if p == nil {
		return errors.New("page: nil")
	}
	p.TenantID = tenantID
	if err := p.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.pages[p.ID]
	if !ok || existing.TenantID != tenantID {
		return ErrNotFound
	}
	if s.duplicateLocked(tenantID, p.Subsite, p.Path, p.ID) {
		return ErrPathConflict
	}

	p.Version = existing.Version + 1
	p.CreatedAt = existing.CreatedAt
	p.UpdatedAt = time.Now().UTC()
	cp := *p
	s.pages[p.ID] = &cp
	return nil
}

// Delete removes the page. ErrNotFound on miss / wrong tenant.
func (s *MemoryPageStore) Delete(_ context.Context, tenantID, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.pages[id]
	if !ok || existing.TenantID != tenantID {
		return ErrNotFound
	}
	delete(s.pages, id)
	return nil
}

// List returns pages for the tenant, optionally filtered by subsite.
// Empty subsite arg = ALL pages for the tenant (including NULL-subsite
// + every subsite).
func (s *MemoryPageStore) List(_ context.Context, tenantID int64, subsite string) ([]*Page, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Page
	for _, p := range s.pages {
		if p.TenantID != tenantID {
			continue
		}
		if subsite != "" && p.Subsite != subsite {
			continue
		}
		cp := *p
		out = append(out, &cp)
	}
	// Stable sort by Path for deterministic output.
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// duplicateLocked returns true if an existing record (other than excludeID)
// has the same (tenant, subsite, path).
func (s *MemoryPageStore) duplicateLocked(tenantID int64, subsite, path string, excludeID int64) bool {
	for _, p := range s.pages {
		if p.ID == excludeID {
			continue
		}
		if p.TenantID == tenantID && p.Subsite == subsite && p.Path == path {
			return true
		}
	}
	return false
}
