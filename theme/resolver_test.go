package theme

import (
	"context"
	"errors"
	"testing"
)

func newStore(t *testing.T, withDefault bool, tenantThemes ...*Theme) *MemoryStore {
	t.Helper()
	s := NewMemoryStore()
	if withDefault {
		s.Put(&Theme{ID: 1, TenantID: 0, Name: "host-default", TemplateArchivePath: "/themes/default", IsDefault: true})
	}
	for _, th := range tenantThemes {
		s.Put(th)
	}
	return s
}

func TestResolve_TenantThemeHit(t *testing.T) {
	s := newStore(t, true, &Theme{ID: 100, TenantID: 5, Name: "my-band-theme"})
	got, err := Resolve(context.Background(), s, 5, Spec{ID: 100, UseHostDefault: false})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Name != "my-band-theme" {
		t.Errorf("got %q want my-band-theme", got.Name)
	}
}

func TestResolve_MissWithFallback_ReturnsHostDefault(t *testing.T) {
	s := newStore(t, true /* host default present */)
	got, err := Resolve(context.Background(), s, 5, Spec{ID: 999, UseHostDefault: true})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Name != "host-default" {
		t.Errorf("got %q want host-default", got.Name)
	}
}

func TestResolve_MissWithoutFallback_HardError(t *testing.T) {
	s := newStore(t, true)
	_, err := Resolve(context.Background(), s, 5, Spec{ID: 999, UseHostDefault: false})
	if !errors.Is(err, ErrThemeMissingNoFallback) {
		t.Errorf("got %v want ErrThemeMissingNoFallback (V23)", err)
	}
}

func TestResolve_CrossTenantTheme_IsHidden(t *testing.T) {
	// Tenant 5 owns theme 100. Tenant 6 must NOT be able to resolve it.
	s := newStore(t, true, &Theme{ID: 100, TenantID: 5, Name: "five"})
	_, err := Resolve(context.Background(), s, 6, Spec{ID: 100, UseHostDefault: false})
	if !errors.Is(err, ErrThemeMissingNoFallback) {
		t.Errorf("cross-tenant: got %v want ErrThemeMissingNoFallback (V12 + V23)", err)
	}
}

func TestResolve_NoIDWithFallback_ReturnsDefault(t *testing.T) {
	// Spec.ID = 0 + UseHostDefault = true → straight to default.
	s := newStore(t, true)
	got, err := Resolve(context.Background(), s, 5, Spec{ID: 0, UseHostDefault: true})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.IsDefault {
		t.Error("got non-default theme")
	}
}

func TestResolve_NoIDNoFallback_HardError(t *testing.T) {
	s := newStore(t, true)
	_, err := Resolve(context.Background(), s, 5, Spec{ID: 0, UseHostDefault: false})
	if !errors.Is(err, ErrThemeMissingNoFallback) {
		t.Errorf("got %v want ErrThemeMissingNoFallback", err)
	}
}

func TestResolve_FallbackButNoDefaultRegistered_Errors(t *testing.T) {
	// use_host_default = true but the host has no default theme record →
	// returns the wrapping error (not silent).
	s := newStore(t, false /* no default */)
	_, err := Resolve(context.Background(), s, 5, Spec{ID: 999, UseHostDefault: true})
	if err == nil {
		t.Fatal("expected error when no default present")
	}
}

func TestResolve_NilStore(t *testing.T) {
	if _, err := Resolve(context.Background(), nil, 5, Spec{}); err == nil {
		t.Error("nil store should error")
	}
}

func TestMemoryStore_PutAndGetDefault(t *testing.T) {
	s := NewMemoryStore()
	s.Put(&Theme{ID: 1, TenantID: 0, IsDefault: true, Name: "x"})
	got, err := s.GetHostDefault(context.Background())
	if err != nil {
		t.Fatalf("GetHostDefault: %v", err)
	}
	if got.Name != "x" {
		t.Errorf("got %q want x", got.Name)
	}
}
