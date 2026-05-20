package editor

import (
	"encoding/json"
	"reflect"
	"testing"
)

type stubProvider struct {
	name string
}

func (s *stubProvider) Name() string                                   { return s.name }
func (s *stubProvider) FrontendBundleID() string                       { return "" }
func (s *stubProvider) EmptyBlocks() json.RawMessage                   { return []byte(`{}`) }
func (s *stubProvider) Render(blocks json.RawMessage) (string, bool)   { return "", true }
func (s *stubProvider) Validate(blocks json.RawMessage) error          { return nil }

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubProvider{name: "a"}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := r.Register(&stubProvider{name: "b"}); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if r.Count() != 2 {
		t.Errorf("count: got %d want 2", r.Count())
	}
	p, ok := r.Get("a")
	if !ok || p.Name() != "a" {
		t.Errorf("get a: ok=%v name=%v", ok, p)
	}
	if _, ok := r.Get("missing"); ok {
		t.Error("Get('missing') should return ok=false")
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubProvider{name: "x"})
	err := r.Register(&stubProvider{name: "x"})
	if err == nil {
		t.Fatal("expected duplicate-name error")
	}
}

func TestRegistry_RegisterNilOrEmpty(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Error("nil provider should error")
	}
	if err := r.Register(&stubProvider{name: ""}); err == nil {
		t.Error("empty name should error")
	}
}

func TestRegistry_NamesSorted(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubProvider{name: "zoo"})
	_ = r.Register(&stubProvider{name: "alpha"})
	_ = r.Register(&stubProvider{name: "mid"})
	want := []string{"alpha", "mid", "zoo"}
	got := r.Names()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("names: got %v want %v", got, want)
	}
}
