package store

import (
	"context"
	"errors"
	"testing"
)

func TestTenantAdmin_CreateGetUpdateDelete(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryTenantAdminStore()

	tn := &Tenant{Slug: "acme", Label: "Acme Inc", ThemeID: "default"}
	if err := s.CreateTenant(ctx, tn); err != nil {
		t.Fatalf("create: %v", err)
	}
	if tn.ID == 0 {
		t.Fatal("create did not set ID")
	}

	got, err := s.GetTenant(ctx, tn.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Slug != "acme" {
		t.Errorf("get slug = %q want acme", got.Slug)
	}

	got.Label = "Acme Corp"
	if err := s.UpdateTenant(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, _ := s.GetTenant(ctx, tn.ID)
	if again.Label != "Acme Corp" {
		t.Errorf("update label = %q want Acme Corp", again.Label)
	}

	if err := s.DeleteTenant(ctx, tn.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetTenant(ctx, tn.ID); !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("post-delete get: %v want ErrTenantNotFound", err)
	}
}

func TestTenantAdmin_SlugUniqueness(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryTenantAdminStore()
	_ = s.CreateTenant(ctx, &Tenant{Slug: "dup", Label: "First"})
	err := s.CreateTenant(ctx, &Tenant{Slug: "dup", Label: "Second"})
	if !errors.Is(err, ErrTenantSlugTaken) {
		t.Errorf("create with dup slug: %v want ErrTenantSlugTaken", err)
	}
}

func TestDomain_CreateUniqueAndCascadeDelete(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryTenantAdminStore()
	tn := &Tenant{Slug: "a", Label: "A"}
	_ = s.CreateTenant(ctx, tn)

	d := &Domain{TenantID: tn.ID, Host: "a.example", Kind: "vanity"}
	if err := s.CreateDomain(ctx, d); err != nil {
		t.Fatalf("create domain: %v", err)
	}

	// Duplicate host rejected.
	err := s.CreateDomain(ctx, &Domain{TenantID: tn.ID, Host: "A.Example", Kind: "vanity"})
	if !errors.Is(err, ErrDomainTaken) {
		t.Errorf("dup host: %v want ErrDomainTaken (case-insensitive)", err)
	}

	// Cross-tenant delete fails as not-found (no info leak).
	tn2 := &Tenant{Slug: "b", Label: "B"}
	_ = s.CreateTenant(ctx, tn2)
	err = s.DeleteDomain(ctx, tn2.ID, d.ID)
	if !errors.Is(err, ErrDomainNotFound) {
		t.Errorf("cross-tenant delete: %v want ErrDomainNotFound", err)
	}

	// Cascade on tenant delete.
	_ = s.DeleteTenant(ctx, tn.ID)
	if list, _ := s.ListDomains(ctx, tn.ID); list != nil {
		// Tenant deleted, so list returns ErrTenantNotFound; the nil
		// check above is just defence.
	}
	if _, err := s.ListDomains(ctx, tn.ID); !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("post-cascade list: %v want ErrTenantNotFound", err)
	}
}

func TestDomain_DomainRequiresTenant(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryTenantAdminStore()
	err := s.CreateDomain(ctx, &Domain{TenantID: 9999, Host: "ghost.example"})
	if !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("create orphan domain: %v want ErrTenantNotFound", err)
	}
}
