package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func makePage(tenant int64, path, title string) *Page {
	return &Page{
		TenantID:   tenant,
		Path:       path,
		Title:      title,
		BodyBlocks: json.RawMessage(`{"type":"doc","content":[]}`),
		Status:     StatusDraft,
	}
}

func TestPageStore_CreateGetUpdateDelete(t *testing.T) {
	s := NewMemoryPageStore()
	ctx := context.Background()

	// Create
	p := makePage(1, "/about", "About")
	if err := s.Create(ctx, 1, p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ID == 0 {
		t.Error("Create did not assign ID")
	}
	if p.Version != 1 {
		t.Errorf("Version after Create: got %d want 1", p.Version)
	}

	// Get
	got, err := s.Get(ctx, 1, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "About" {
		t.Errorf("Title: got %q want About", got.Title)
	}

	// Update
	got.Title = "About Us"
	if err := s.Update(ctx, 1, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Version != 2 {
		t.Errorf("Version after Update: got %d want 2", got.Version)
	}

	// Delete
	if err := s.Delete(ctx, 1, got.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, 1, got.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: got %v want ErrNotFound", err)
	}
}

func TestPageStore_TenantIsolation(t *testing.T) {
	s := NewMemoryPageStore()
	ctx := context.Background()

	p1 := makePage(1, "/x", "T1")
	p2 := makePage(2, "/x", "T2")
	if err := s.Create(ctx, 1, p1); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, 2, p2); err != nil {
		t.Fatal(err)
	}

	// Tenant 1 cannot see tenant 2's page even with correct ID.
	if _, err := s.Get(ctx, 1, p2.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant Get: got %v want ErrNotFound (V12)", err)
	}
	if _, err := s.Get(ctx, 2, p1.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant Get reverse: got %v want ErrNotFound", err)
	}

	// Each tenant CAN see their own.
	if _, err := s.Get(ctx, 1, p1.ID); err != nil {
		t.Errorf("self Get: %v", err)
	}
	if _, err := s.Get(ctx, 2, p2.ID); err != nil {
		t.Errorf("self Get t2: %v", err)
	}
}

func TestPageStore_DeleteCrossTenantNoLeak(t *testing.T) {
	s := NewMemoryPageStore()
	ctx := context.Background()
	p := makePage(1, "/x", "P")
	_ = s.Create(ctx, 1, p)

	if err := s.Delete(ctx, 2, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant Delete: got %v want ErrNotFound", err)
	}
	// Sanity: page still exists for owner.
	if _, err := s.Get(ctx, 1, p.ID); err != nil {
		t.Errorf("owner Get post-attack: %v", err)
	}
}

func TestPageStore_PathConflict(t *testing.T) {
	s := NewMemoryPageStore()
	ctx := context.Background()
	_ = s.Create(ctx, 1, makePage(1, "/dup", "First"))
	err := s.Create(ctx, 1, makePage(1, "/dup", "Second"))
	if !errors.Is(err, ErrPathConflict) {
		t.Errorf("Create duplicate path: got %v want ErrPathConflict", err)
	}
}

func TestPageStore_SubsiteScoping(t *testing.T) {
	s := NewMemoryPageStore()
	ctx := context.Background()

	mainPg := makePage(1, "/news", "Main news")
	mainPg.Subsite = "main"
	tourPg := makePage(1, "/news", "Tour news")
	tourPg.Subsite = "tour"

	if err := s.Create(ctx, 1, mainPg); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, 1, tourPg); err != nil {
		// Same tenant, same path, different subsite — should be allowed.
		t.Fatalf("subsite-distinct create: %v", err)
	}

	got, err := s.GetByPath(ctx, 1, "tour", "/news")
	if err != nil || got.Title != "Tour news" {
		t.Errorf("GetByPath tour: %v / %v", err, got)
	}
	got, err = s.GetByPath(ctx, 1, "main", "/news")
	if err != nil || got.Title != "Main news" {
		t.Errorf("GetByPath main: %v / %v", err, got)
	}
}

func TestPageStore_GetByPath_MissNotFound(t *testing.T) {
	s := NewMemoryPageStore()
	if _, err := s.GetByPath(context.Background(), 1, "", "/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByPath miss: got %v want ErrNotFound", err)
	}
}

func TestPageStore_List(t *testing.T) {
	s := NewMemoryPageStore()
	ctx := context.Background()
	_ = s.Create(ctx, 1, makePage(1, "/a", "A"))
	_ = s.Create(ctx, 1, makePage(1, "/b", "B"))
	p3 := makePage(1, "/c", "C")
	p3.Subsite = "shop"
	_ = s.Create(ctx, 1, p3)
	_ = s.Create(ctx, 2, makePage(2, "/x", "T2"))

	// No subsite filter → all tenant-1 pages.
	all, _ := s.List(ctx, 1, "")
	if len(all) != 3 {
		t.Errorf("List tenant 1: got %d want 3", len(all))
	}

	// Filter to "shop" subsite.
	shop, _ := s.List(ctx, 1, "shop")
	if len(shop) != 1 || shop[0].Path != "/c" {
		t.Errorf("List shop: got %v", shop)
	}

	// Tenant 2 sees only its own.
	t2, _ := s.List(ctx, 2, "")
	if len(t2) != 1 {
		t.Errorf("List tenant 2: got %d want 1", len(t2))
	}
}

func TestPage_Validate(t *testing.T) {
	cases := []struct {
		name string
		p    *Page
		want string
	}{
		{"no tenant", &Page{Path: "/x", Title: "X"}, "tenant_id required"},
		{"no path", &Page{TenantID: 1, Title: "X"}, "path required"},
		{"no title", &Page{TenantID: 1, Path: "/x"}, "title required"},
		{"invalid status", &Page{TenantID: 1, Path: "/x", Title: "X", Status: "weird"}, "invalid status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Validate()
			if err == nil {
				t.Fatal("expected error")
			}
			if !contains(err.Error(), tc.want) {
				t.Errorf("error: got %q want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestPage_ValidateDefaultsStatusDraft(t *testing.T) {
	p := &Page{TenantID: 1, Path: "/x", Title: "X"}
	if err := p.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if p.Status != StatusDraft {
		t.Errorf("Status default: got %q want draft", p.Status)
	}
}

func TestPageStore_NilPage(t *testing.T) {
	s := NewMemoryPageStore()
	if err := s.Create(context.Background(), 1, nil); err == nil {
		t.Error("nil page Create should error")
	}
	if err := s.Update(context.Background(), 1, nil); err == nil {
		t.Error("nil page Update should error")
	}
}

func TestPageStore_UpdateNotFound(t *testing.T) {
	s := NewMemoryPageStore()
	p := makePage(1, "/x", "X")
	p.ID = 999
	if err := s.Update(context.Background(), 1, p); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update missing: got %v want ErrNotFound", err)
	}
}

func TestPageStore_UpdatePathConflict(t *testing.T) {
	s := NewMemoryPageStore()
	ctx := context.Background()
	p1 := makePage(1, "/a", "A")
	p2 := makePage(1, "/b", "B")
	_ = s.Create(ctx, 1, p1)
	_ = s.Create(ctx, 1, p2)
	p2.Path = "/a"
	if err := s.Update(ctx, 1, p2); !errors.Is(err, ErrPathConflict) {
		t.Errorf("Update conflicting path: got %v want ErrPathConflict", err)
	}
}

// contains is a small substring check kept dep-free.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
