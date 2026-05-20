package analytics

import (
	"bytes"
	"strings"
	"testing"
)

func TestInjectGtag_DisabledPassesThrough(t *testing.T) {
	html := []byte("<html><head></head><body>x</body></html>")
	out := InjectGtag(html, TenantConfig{})
	if !bytes.Equal(out, html) {
		t.Errorf("disabled: output should equal input; got %q", out)
	}
}

func TestInjectGtag_BeforeCloseHead(t *testing.T) {
	html := []byte(`<html><head><meta name="x" content="y"></head><body></body></html>`)
	cfg := TenantConfig{GoogleMeasurementID: "G-ABCDE12345", AnonymizeIP: true}
	out := InjectGtag(html, cfg)
	s := string(out)

	headIdx := strings.Index(s, "</head>")
	gtagIdx := strings.Index(s, "googletagmanager.com")
	if headIdx < 0 || gtagIdx < 0 {
		t.Fatalf("missing markers; got %q", s)
	}
	if gtagIdx >= headIdx {
		t.Errorf("gtag snippet should be BEFORE </head>; gtag=%d head=%d", gtagIdx, headIdx)
	}
	if !strings.Contains(s, "anonymize_ip") {
		t.Errorf("anonymize_ip should be present; got %q", s)
	}
	if !strings.Contains(s, "G-ABCDE12345") {
		t.Errorf("measurement_id should be present; got %q", s)
	}
}

func TestInjectGtag_NoAnonymizeIP(t *testing.T) {
	html := []byte("<html><head></head><body></body></html>")
	cfg := TenantConfig{GoogleMeasurementID: "G-FOO12345", AnonymizeIP: false}
	out := InjectGtag(html, cfg)
	if strings.Contains(string(out), "anonymize_ip") {
		t.Errorf("AnonymizeIP=false should NOT emit anonymize_ip flag; got %q", out)
	}
}

func TestInjectGtag_NoCloseHeadDegraded(t *testing.T) {
	// HTML without </head> — snippet prepended.
	html := []byte("<body>no head tag</body>")
	cfg := TenantConfig{GoogleMeasurementID: "G-NOHEAD123"}
	out := InjectGtag(html, cfg)
	s := string(out)
	if !strings.HasPrefix(s, "<script") {
		t.Errorf("expected snippet prepended on degraded HTML; got %q", s[:50])
	}
}

func TestRenderGtagSnippet_DisabledReturnsNil(t *testing.T) {
	if got := RenderGtagSnippet(TenantConfig{}); got != nil {
		t.Errorf("disabled should return nil; got %v", got)
	}
}

func TestEscapeJSString_StripsBreakoutChars(t *testing.T) {
	// escapeJSString strips quotes, backslash, < > and line separators.
	// It does NOT strip / (forward slash) — that's not a breakout char
	// in single-quoted JS string context.
	cases := []struct {
		in, want string
	}{
		{`G-ABC123`, `G-ABC123`},
		{`G-ABC'123`, `G-ABC123`},                                  // ' stripped
		{`G-<script>alert(1)</script>`, `G-scriptalert(1)/script`}, // < > stripped, / preserved
		{"G-ABC\n123", "G-ABC123"},                                 // newline stripped
	}
	for _, tc := range cases {
		got := escapeJSString(tc.in)
		if got != tc.want {
			t.Errorf("escapeJSString(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestInjectGtag_XSSAttempt(t *testing.T) {
	html := []byte("<html><head></head></html>")
	// Try a measurement_id with injection attempt.
	cfg := TenantConfig{GoogleMeasurementID: `G-X');alert(1);//`, AnonymizeIP: false}
	out := InjectGtag(html, cfg)
	s := string(out)

	// The injected snippet must NOT contain the literal injection
	// characters that would break out of the JS string context.
	if strings.Contains(s, "');alert") {
		t.Errorf("XSS payload leaked through; got %q", s)
	}
}

func TestTenantConfig_Enabled(t *testing.T) {
	if (TenantConfig{}).Enabled() {
		t.Error("zero-value TenantConfig should be disabled")
	}
	if !(TenantConfig{GoogleMeasurementID: "G-X"}).Enabled() {
		t.Error("with ID should be enabled")
	}
}
