// Package editor defines the WYSIWYG editor provider interface for
// workflow-plugin-cms.
//
// The CMS engine stores page content in TWO complementary forms:
//
//   - body_blocks: structured block JSON (provider-specific shape)
//   - body_html:   rendered HTML for serve-time output
//
// Providers translate between the two and render block trees to HTML.
//
// Per gocodealone-multisite SPEC.md V10: provider impls share a common
// interface; swap = config flag, no code change.
//
// Default provider = TipTap (MIT, ProseMirror-based). Alt providers
// (Editor.js, Lexical, TinyMCE community) implement the same interface.
package editor

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Provider is the contract every WYSIWYG backend implements.
//
// Methods are intentionally minimal — the CMS engine owns persistence,
// auth, and route resolution. The provider owns block-format <→> HTML
// rendering and front-end-component identity.
type Provider interface {
	// Name returns the provider's short identifier (e.g. "tiptap",
	// "editorjs", "lexical"). Matches the `provider` config key.
	Name() string

	// FrontendBundleID returns the identifier of the editor's JS bundle
	// shipped with the admin UI. Empty string = host serves no bundle
	// for this provider (rare; usually only true for stub/test).
	FrontendBundleID() string

	// EmptyBlocks returns the canonical "empty document" block JSON for
	// new pages. Callers persist this as `body_blocks` for newly-created
	// drafts.
	EmptyBlocks() json.RawMessage

	// Render translates block JSON to HTML for serve-time output.
	// Returns ("", false) on bad input — caller falls back to last
	// known good body_html (V14: dynamic rendering is server-side
	// deterministic, never client-fetch).
	Render(blocks json.RawMessage) (html string, ok bool)

	// Validate returns nil if the block JSON is structurally valid for
	// this provider; otherwise an error suitable for surfacing to the
	// admin UI.
	Validate(blocks json.RawMessage) error
}

// Registry holds the set of available providers and resolves the
// currently-configured default by name.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry returns an empty Registry. Use Register to add providers
// at process start; lookup is concurrency-safe thereafter.
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

// Register adds a provider keyed by its Name(). Returns an error if a
// provider with the same name is already registered (duplicate config
// is a hard error to surface mismatches early).
func (r *Registry) Register(p Provider) error {
	if p == nil {
		return fmt.Errorf("nil provider")
	}
	name := p.Name()
	if name == "" {
		return fmt.Errorf("provider has empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.providers[name]; dup {
		return fmt.Errorf("provider %q already registered", name)
	}
	r.providers[name] = p
	return nil
}

// Get returns the provider with the given name. ok=false if not registered.
func (r *Registry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// Names returns the sorted list of registered provider names. Stable
// output suits diagnostic dumps + admin UI provider lists.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.providers))
	for n := range r.providers {
		out = append(out, n)
	}
	sortStrings(out)
	return out
}

// Count returns the number of registered providers.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.providers)
}

// sortStrings is a small dependency-free string sort to avoid pulling in
// the sort package for one call. Insertion sort — fine for small N.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
