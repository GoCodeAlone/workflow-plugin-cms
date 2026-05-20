// Package store persists CMS records.
package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Tenant is a multisite tenant.
type Tenant struct {
	ID        int64
	Slug      string
	Label     string
	ThemeID   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Domain is a vanity / preview / subdomain owned by a tenant.
//
// Per SPEC V22: subsite scope is host-level (set on the Domain), not
// path-level — `cool-band.com` and `cool-band.com/tour` resolve to the
// same tenant but different subsites.
type Domain struct {
	ID           int64
	TenantID     int64
	Host         string // exact, lowercase, e.g. "cool-band.com"
	SubsiteLabel string // empty = root subsite
	Kind         string // "vanity" | "preview" | "subdomain"
}

// TenantAdminStore is the CRUD surface for the multisite admin. It is
// separate from TenantStore (the resolver's read-only interface) so
// reads can be optimised independently from writes.
//
// All operations are tenant-scoped via TenantID (no cross-tenant leak).
type TenantAdminStore interface {
	// CreateTenant assigns an ID + timestamps. Slug must be unique;
	// returns ErrTenantSlugTaken otherwise.
	CreateTenant(ctx context.Context, t *Tenant) error
	GetTenant(ctx context.Context, id int64) (*Tenant, error)
	UpdateTenant(ctx context.Context, t *Tenant) error
	DeleteTenant(ctx context.Context, id int64) error
	ListTenants(ctx context.Context) ([]*Tenant, error)

	// CreateDomain attaches a Domain to an existing tenant. Host must be
	// globally unique across all tenants; returns ErrDomainTaken otherwise.
	CreateDomain(ctx context.Context, d *Domain) error
	DeleteDomain(ctx context.Context, tenantID, id int64) error
	ListDomains(ctx context.Context, tenantID int64) ([]*Domain, error)
}

// Sentinel errors.
var (
	ErrTenantNotFound = errors.New("store: tenant not found")
	ErrTenantSlugTaken = errors.New("store: tenant slug taken")
	ErrDomainNotFound  = errors.New("store: domain not found")
	ErrDomainTaken     = errors.New("store: domain host taken")
)

// MemoryTenantAdminStore is the in-memory implementation used for tests
// and as the default until a persistent store is wired.
type MemoryTenantAdminStore struct {
	mu      sync.RWMutex
	nextID  int64
	tenants map[int64]*Tenant
	domains map[int64]*Domain
}

func NewMemoryTenantAdminStore() *MemoryTenantAdminStore {
	return &MemoryTenantAdminStore{
		tenants: map[int64]*Tenant{},
		domains: map[int64]*Domain{},
	}
}

func (s *MemoryTenantAdminStore) CreateTenant(_ context.Context, t *Tenant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.tenants {
		if strings.EqualFold(existing.Slug, t.Slug) {
			return ErrTenantSlugTaken
		}
	}
	s.nextID++
	t.ID = s.nextID
	now := time.Now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now
	clone := *t
	s.tenants[t.ID] = &clone
	return nil
}

func (s *MemoryTenantAdminStore) GetTenant(_ context.Context, id int64) (*Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tenants[id]
	if !ok {
		return nil, ErrTenantNotFound
	}
	clone := *t
	return &clone, nil
}

func (s *MemoryTenantAdminStore) UpdateTenant(_ context.Context, t *Tenant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.tenants[t.ID]
	if !ok {
		return ErrTenantNotFound
	}
	// Slug uniqueness across all OTHER tenants.
	for id, other := range s.tenants {
		if id == t.ID {
			continue
		}
		if strings.EqualFold(other.Slug, t.Slug) {
			return ErrTenantSlugTaken
		}
	}
	existing.Slug = t.Slug
	existing.Label = t.Label
	existing.ThemeID = t.ThemeID
	existing.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *MemoryTenantAdminStore) DeleteTenant(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[id]; !ok {
		return ErrTenantNotFound
	}
	delete(s.tenants, id)
	// Cascade: drop any domain attached to this tenant.
	for did, d := range s.domains {
		if d.TenantID == id {
			delete(s.domains, did)
		}
	}
	return nil
}

func (s *MemoryTenantAdminStore) ListTenants(_ context.Context) ([]*Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Tenant, 0, len(s.tenants))
	for _, t := range s.tenants {
		clone := *t
		out = append(out, &clone)
	}
	return out, nil
}

func (s *MemoryTenantAdminStore) CreateDomain(_ context.Context, d *Domain) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[d.TenantID]; !ok {
		return ErrTenantNotFound
	}
	host := strings.ToLower(strings.TrimSpace(d.Host))
	if host == "" {
		return errors.New("store: host required")
	}
	for _, existing := range s.domains {
		if strings.EqualFold(existing.Host, host) {
			return ErrDomainTaken
		}
	}
	s.nextID++
	d.ID = s.nextID
	d.Host = host
	clone := *d
	s.domains[d.ID] = &clone
	return nil
}

func (s *MemoryTenantAdminStore) DeleteDomain(_ context.Context, tenantID, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.domains[id]
	if !ok || d.TenantID != tenantID {
		// Tenant isolation: cross-tenant deletes look identical to misses.
		return ErrDomainNotFound
	}
	delete(s.domains, id)
	return nil
}

func (s *MemoryTenantAdminStore) ListDomains(_ context.Context, tenantID int64) ([]*Domain, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.tenants[tenantID]; !ok {
		return nil, ErrTenantNotFound
	}
	out := []*Domain{}
	for _, d := range s.domains {
		if d.TenantID == tenantID {
			clone := *d
			out = append(out, &clone)
		}
	}
	return out, nil
}
