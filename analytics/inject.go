// Package analytics injects per-tenant analytics snippets into served
// HTML.
//
// Per gocodealone-multisite SPEC.md V25: when the tenant's
// multisite.yaml has analytics.google.measurement_id set, the host
// injects gtag.js into rendered pages. Without an ID set, ⊥ injection.
//
// The actual snippet is rendered by workflow-plugin-analytics — this
// package only owns the per-tenant gating + HTML splice point.
package analytics

import (
	"bytes"
	"strings"
)

// TenantConfig is the per-tenant analytics config (from multisite.yaml).
type TenantConfig struct {
	GoogleMeasurementID string
	AnonymizeIP         bool
}

// Enabled returns true iff this config opts the tenant into analytics
// injection.
func (c TenantConfig) Enabled() bool {
	return c.GoogleMeasurementID != ""
}

// InjectGtag returns html with a `<script>` block inserted before the
// closing `</head>` tag. If no </head> is present (rare — degraded
// HTML) the snippet is prepended.
//
// If cfg is disabled, returns html unchanged.
//
// The snippet content is intentionally minimal here. Production wires
// this through workflow-plugin-analytics, which has a more complete
// gtag rendering surface; this implementation is the fallback used
// when that plugin is not loaded.
func InjectGtag(html []byte, cfg TenantConfig) []byte {
	if !cfg.Enabled() {
		return html
	}

	snippet := RenderGtagSnippet(cfg)

	// Splice before </head> if present.
	idx := bytes.Index(bytes.ToLower(html), []byte("</head>"))
	if idx < 0 {
		// Degraded HTML — prepend.
		out := make([]byte, 0, len(html)+len(snippet))
		out = append(out, snippet...)
		out = append(out, html...)
		return out
	}

	out := make([]byte, 0, len(html)+len(snippet))
	out = append(out, html[:idx]...)
	out = append(out, snippet...)
	out = append(out, html[idx:]...)
	return out
}

// RenderGtagSnippet returns the gtag.js script tag block. The
// measurement ID is interpolated; anonymize_ip is gated.
//
// The measurement_id must match the GA4 pattern (G-XXXXXXXXXX). Anything
// else is escaped at the JS string boundary to prevent injection.
func RenderGtagSnippet(cfg TenantConfig) []byte {
	if !cfg.Enabled() {
		return nil
	}
	mid := escapeJSString(cfg.GoogleMeasurementID)

	var b strings.Builder
	b.WriteString(`<script async src="https://www.googletagmanager.com/gtag/js?id=`)
	b.WriteString(escapeAttr(cfg.GoogleMeasurementID))
	b.WriteString(`"></script>` + "\n")
	b.WriteString(`<script>`)
	b.WriteString(`window.dataLayer=window.dataLayer||[];function gtag(){dataLayer.push(arguments);}gtag('js',new Date());`)
	if cfg.AnonymizeIP {
		b.WriteString(`gtag('config','`)
		b.WriteString(mid)
		b.WriteString(`',{'anonymize_ip':true});`)
	} else {
		b.WriteString(`gtag('config','`)
		b.WriteString(mid)
		b.WriteString(`');`)
	}
	b.WriteString(`</script>` + "\n")

	return []byte(b.String())
}

// escapeJSString returns a safe-for-single-quoted-JS-string version of
// input. The measurement_id is expected to be a strict pattern
// (G-XXXXXXXXXX); this is defence-in-depth.
func escapeJSString(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '\'', '"', '\n', '\r', '<', '>':
			// Disallow chars that could break out of the string
			// context. Skip them entirely — measurement_id should
			// never contain these.
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// escapeAttr is the HTML-attribute-safe analogue. Same restrictive
// allow-list since measurement_id is highly constrained.
func escapeAttr(s string) string {
	// Reuse the same filter — it's equally safe for attr context for
	// the constrained measurement_id pattern.
	return escapeJSString(s)
}
