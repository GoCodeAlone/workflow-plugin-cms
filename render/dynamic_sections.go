// Package render handles dynamic-section substitution in served HTML.
//
// Per gocodealone-multisite SPEC.md V14: dynamic sections in static HTML
// are rendered server-side via marker substitution; ⊥ client-side JS
// fetch unless explicit.
//
// Markers look like:
//
//	<!-- multisite:latest-blog -->
//
// At render time the renderer finds each marker and replaces it with the
// HTML produced by the configured template for that section id. The
// marker shape is intentionally invisible to browsers — if a tenant
// strips the host's templates, the page degrades to a literal HTML
// comment (no broken-page artifact).
package render

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sync"
)

// SectionTemplate is the contract a CMS template (e.g. blog-list,
// contact-form) implements. The renderer passes the section id + a
// tenant-scoped context; the template returns the HTML to substitute.
type SectionTemplate interface {
	Name() string
	Render(ctx context.Context, sectionID string, params map[string]string) (string, error)
}

// Registry maps template name → SectionTemplate.
type Registry struct {
	mu        sync.RWMutex
	templates map[string]SectionTemplate
}

func NewRegistry() *Registry {
	return &Registry{templates: map[string]SectionTemplate{}}
}

func (r *Registry) Register(t SectionTemplate) error {
	if t == nil {
		return fmt.Errorf("nil template")
	}
	name := t.Name()
	if name == "" {
		return fmt.Errorf("template has empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.templates[name]; dup {
		return fmt.Errorf("template %q already registered", name)
	}
	r.templates[name] = t
	return nil
}

func (r *Registry) Get(name string) (SectionTemplate, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.templates[name]
	return t, ok
}

// markerRe matches `<!-- multisite:<id> -->` with optional whitespace.
// `<id>` is a sequence of ASCII letters/digits/-/_.
var markerRe = regexp.MustCompile(`<!--\s*multisite:([a-zA-Z0-9_\-]+)\s*-->`)

// SectionSpec describes what template to invoke for a marker id, taken
// from the tenant's multisite.yaml `dynamic_sections[]` entries.
type SectionSpec struct {
	ID       string            // matches the marker id
	Template string            // template name (must resolve in Registry)
	Params   map[string]string // per-section params passed through to template.Render
}

// SubstituteOptions controls how Substitute behaves on edge cases.
type SubstituteOptions struct {
	// OnMissingTemplate decides what to do when a marker references an
	// id that the tenant's manifest declares BUT the host has no
	// registered template for. Default: leave the comment in place
	// (silent degrade).
	OnMissingTemplate MissingTemplatePolicy

	// OnMissingSpec decides what to do when a marker references an id
	// that the tenant's manifest does NOT declare. Default: leave the
	// comment in place (the marker is a no-op).
	OnMissingSpec MissingSpecPolicy
}

// MissingTemplatePolicy controls behaviour for declared-but-unregistered template.
type MissingTemplatePolicy int

const (
	// MissingTemplateSilent leaves the original comment intact.
	MissingTemplateSilent MissingTemplatePolicy = iota
	// MissingTemplateError aborts substitution and returns an error.
	MissingTemplateError
)

// MissingSpecPolicy controls behaviour for un-declared marker ids.
type MissingSpecPolicy int

const (
	// MissingSpecSilent leaves the original comment intact.
	MissingSpecSilent MissingSpecPolicy = iota
	// MissingSpecError aborts substitution and returns an error.
	MissingSpecError
)

// Substitute walks `html` and replaces every multisite marker with the
// rendered output of its declared template.
//
//   - registry: template registry the host wires at boot.
//   - specs:    the tenant's multisite.yaml dynamic_sections[] list.
//
// Returns the rewritten HTML. On a template Render error, returns the
// partial-progress result + the error (caller decides whether to
// serve-or-fail).
func Substitute(ctx context.Context, html []byte, registry *Registry, specs []SectionSpec, opts SubstituteOptions) ([]byte, error) {
	// Build id → spec lookup.
	specByID := make(map[string]*SectionSpec, len(specs))
	for i := range specs {
		specByID[specs[i].ID] = &specs[i]
	}

	var lastErr error

	out := markerRe.ReplaceAllFunc(html, func(match []byte) []byte {
		idMatch := markerRe.FindSubmatch(match)
		if len(idMatch) < 2 {
			return match
		}
		id := string(idMatch[1])

		spec, ok := specByID[id]
		if !ok {
			if opts.OnMissingSpec == MissingSpecError {
				lastErr = fmt.Errorf("dynamic section %q not declared in manifest", id)
			}
			return match
		}

		tmpl, ok := registry.Get(spec.Template)
		if !ok {
			if opts.OnMissingTemplate == MissingTemplateError {
				lastErr = fmt.Errorf("template %q not registered for section %q", spec.Template, id)
			}
			return match
		}

		body, err := tmpl.Render(ctx, id, spec.Params)
		if err != nil {
			lastErr = fmt.Errorf("render section %q: %w", id, err)
			return match
		}
		return []byte(body)
	})

	return out, lastErr
}

// FindMarkerIDs returns the unique marker ids referenced in the given
// HTML, in order of first occurrence. Useful for validation passes
// (warn when manifest declares ids that don't appear in the bundle).
func FindMarkerIDs(html []byte) []string {
	matches := markerRe.FindAllSubmatch(html, -1)
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		id := string(m[1])
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// stringTemplate is a trivial SectionTemplate that returns a fixed
// string. Useful for tests + simple use cases.
type stringTemplate struct {
	name string
	body string
}

// StringTemplate returns a SectionTemplate that always renders `body`.
// For tests + quick wiring.
func StringTemplate(name, body string) SectionTemplate {
	return &stringTemplate{name: name, body: body}
}

func (t *stringTemplate) Name() string { return t.name }
func (t *stringTemplate) Render(_ context.Context, _ string, _ map[string]string) (string, error) {
	return t.body, nil
}

// ensure bytes.Buffer reference avoids unused-import (used in tests).
var _ = bytes.NewBuffer
