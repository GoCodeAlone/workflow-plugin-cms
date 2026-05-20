// Package theme resolves the active theme for a tenant page render.
//
// Per gocodealone-multisite SPEC.md:
//
//	C11: Theming — default theme bundled with host for sites lacking own
//	     template; per-site theme is expected.
//	V23: bundle's `multisite.yaml.theme` → first try tenant theme record;
//	     on miss + `use_host_default=true` → host default theme; on miss +
//	     `use_host_default=false` → 503 (config error, ⊥ silent fallback).
package theme

import (
	"context"
	"errors"
	"fmt"
)

// Theme is the persistent representation of a renderable theme.
//
// Production wires a postgres-backed Store; the in-memory implementation
// here suits tests + local dev. TenantID == 0 means a host-default
// theme (shared across tenants).
type Theme struct {
	ID                  int64
	TenantID            int64 // 0 = host default
	Name                string
	TemplateArchivePath string
	IsDefault           bool
}

// Store provides theme lookup by id + by-tenant-default + host-default.
type Store interface {
	// Get fetches a theme by id, scoped to the tenant. tenant_id mismatch
	// → ErrNotFound (V12 multi-tenancy guard).
	Get(ctx context.Context, tenantID, themeID int64) (*Theme, error)
	// GetHostDefault returns the bundled host-default theme.
	GetHostDefault(ctx context.Context) (*Theme, error)
}

// ErrNotFound — same semantic as store.ErrNotFound to keep callers
// indifferent to source.
var ErrNotFound = errors.New("theme not found")

// ErrThemeMissingNoFallback is returned when the requested theme is not
// found AND `use_host_default` is false (V23: hard 503 — never silent
// fallback).
var ErrThemeMissingNoFallback = errors.New("theme missing and use_host_default=false (V23)")

// Spec is the resolver input — comes from multisite.yaml.theme block.
type Spec struct {
	ID             int64 // theme id; 0 means "default" name lookup
	UseHostDefault bool
}

// Resolve returns the theme to render with, following the V23 ladder:
//
//  1. spec.ID → store.Get → return
//  2. miss + spec.UseHostDefault=true → store.GetHostDefault → return
//  3. miss + spec.UseHostDefault=false → ErrThemeMissingNoFallback
//
// Calls Get with tenantID; the tenant-scoping guard lives in the Store
// impl.
func Resolve(ctx context.Context, s Store, tenantID int64, spec Spec) (*Theme, error) {
	if s == nil {
		return nil, errors.New("theme: nil store")
	}

	// 1. Tenant-owned theme lookup (when ID set).
	if spec.ID > 0 {
		theme, err := s.Get(ctx, tenantID, spec.ID)
		if err == nil {
			return theme, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("theme get: %w", err)
		}
		// Fall through to step 2.
	}

	// 2. Host default fallback when allowed.
	if spec.UseHostDefault {
		theme, err := s.GetHostDefault(ctx)
		if err != nil {
			return nil, fmt.Errorf("host default: %w", err)
		}
		return theme, nil
	}

	// 3. Hard miss — V23 says NEVER silent fallback.
	return nil, ErrThemeMissingNoFallback
}

// MemoryStore is an in-memory Store for tests + local dev.
type MemoryStore struct {
	themes      map[int64]*Theme // by id (tenant_id == 0 entries are defaults)
	hostDefault *Theme
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{themes: map[int64]*Theme{}} }

// Put inserts/updates a theme.
func (m *MemoryStore) Put(t *Theme) {
	m.themes[t.ID] = t
	if t.TenantID == 0 && t.IsDefault {
		m.hostDefault = t
	}
}

func (m *MemoryStore) Get(_ context.Context, tenantID, themeID int64) (*Theme, error) {
	t, ok := m.themes[themeID]
	if !ok {
		return nil, ErrNotFound
	}
	if t.TenantID != 0 && t.TenantID != tenantID {
		// V12 multi-tenancy guard — pretend it doesn't exist.
		return nil, ErrNotFound
	}
	cp := *t
	return &cp, nil
}

func (m *MemoryStore) GetHostDefault(_ context.Context) (*Theme, error) {
	if m.hostDefault == nil {
		return nil, ErrNotFound
	}
	cp := *m.hostDefault
	return &cp, nil
}
