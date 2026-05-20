package audit

import (
	"testing"
)

func TestLogger_RecordAndVerify(t *testing.T) {
	l := New("super-secret-key", nil)
	if _, err := l.Record("alice", 1, "tenant.create", "tenant:1", nil); err != nil {
		t.Fatalf("record 1: %v", err)
	}
	if _, err := l.Record("alice", 1, "page.create", "page:42", map[string]any{"title": "Welcome"}); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	if _, err := l.Record("system", 0, "deploy.boot", "host:1", nil); err != nil {
		t.Fatalf("record 3: %v", err)
	}

	broken, err := l.Verify(0)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if broken != -1 {
		t.Errorf("verify failed at index %d; whole chain should verify", broken)
	}
}

func TestLogger_DetectsTamper(t *testing.T) {
	sink := NewMemorySink()
	l := New("k", sink)
	_, _ = l.Record("a", 1, "x", "y", nil)
	_, _ = l.Record("a", 1, "x", "z", nil)
	_, _ = l.Record("a", 1, "x", "w", nil)

	// Tamper with row 1's body via the sink's slice — we mutate the
	// stored canonical body but leave the signature unchanged.
	sink.mu.Lock()
	sink.entries[1].Action = "tampered"
	sink.mu.Unlock()

	broken, err := l.Verify(0)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if broken != 1 {
		t.Errorf("expected break at index 1; got %d", broken)
	}
}

func TestLogger_ChainsAcrossRows(t *testing.T) {
	// Each row's PrevSig must equal the previous row's Sig.
	sink := NewMemorySink()
	l := New("k", sink)
	_, _ = l.Record("a", 1, "x", "y", nil)
	_, _ = l.Record("a", 1, "x", "z", nil)
	entries, _ := sink.List(0)
	if len(entries) != 2 {
		t.Fatalf("len = %d want 2", len(entries))
	}
	if entries[0].PrevSig != "" {
		t.Errorf("first PrevSig should be empty; got %q", entries[0].PrevSig)
	}
	if entries[1].PrevSig != entries[0].Sig {
		t.Errorf("PrevSig chain broken: entries[1].PrevSig = %q want %q", entries[1].PrevSig, entries[0].Sig)
	}
}

func TestMemorySink_TenantScope(t *testing.T) {
	s := NewMemorySink()
	_, _ = s.Append(Entry{ID: 1, TenantID: 1, Action: "a"})
	_, _ = s.Append(Entry{ID: 2, TenantID: 2, Action: "b"})
	_, _ = s.Append(Entry{ID: 3, TenantID: 0, Action: "system"})

	t1, _ := s.List(1)
	if len(t1) != 2 || (t1[0].ID != 1 && t1[1].ID != 1) {
		t.Errorf("tenant 1 list: %v", t1)
	}
	// System rows (TenantID=0) should appear in every tenant's list.
	for _, e := range t1 {
		if e.ID == 3 {
			return
		}
	}
	t.Error("system row missing from tenant 1 list")
}
