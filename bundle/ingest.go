// Package bundle owns the per-tenant static-content bundle lifecycle:
// HMAC-verified ingest webhook → authenticated GH release tarball fetch
// → un-tar into versioned directory → atomic swap of `current` symlink.
//
// Per gocodealone-multisite SPEC.md:
//   V8: ingest webhook → HMAC sig ! valid; mismatched req → 401.
//   V9: bundle version monotonic per tenant; replay attempt → 409.
//   V24: content-repo tarball fetch ! authenticated; ⊥ public-anon GET.
package bundle

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// IngestPayload is the body of POST /api/v1/ingest/release.
// Sent by the content-repo's release.yml GH Action after a tag is
// published; the host fetches the actual tarball.
type IngestPayload struct {
	Tag        string `json:"tag"`        // e.g. "v0.3.0"
	Repo       string `json:"repo"`       // "GoCodeAlone/gocodealone-website"
	TarballURL string `json:"tarball_url"` // GH release asset URL
}

// Validate returns nil iff the payload satisfies basic shape rules.
func (p *IngestPayload) Validate() error {
	if p.Tag == "" {
		return errors.New("ingest: tag required")
	}
	if p.Repo == "" {
		return errors.New("ingest: repo required")
	}
	if p.TarballURL == "" {
		return errors.New("ingest: tarball_url required")
	}
	if !strings.HasPrefix(p.TarballURL, "https://") {
		return errors.New("ingest: tarball_url must be https")
	}
	return nil
}

// ErrInvalidSignature is returned when the HMAC header does not match.
var ErrInvalidSignature = errors.New("ingest: invalid HMAC signature")

// ErrSignatureHeaderMissing is returned when no signature header was supplied.
var ErrSignatureHeaderMissing = errors.New("ingest: signature header missing")

// SignatureHeader is the canonical HMAC-bearing header.
const SignatureHeader = "X-Multisite-Sig"

// VerifyHMAC returns nil iff `header` matches the HMAC-SHA256 of `body`
// using `secret`.
//
// The header value must be of the form "sha256=<hex>". Constant-time
// comparison protects against timing side-channels.
func VerifyHMAC(header string, body []byte, secret string) error {
	if header == "" {
		return ErrSignatureHeaderMissing
	}
	prefix := "sha256="
	if !strings.HasPrefix(header, prefix) {
		return ErrInvalidSignature
	}
	got, err := hex.DecodeString(header[len(prefix):])
	if err != nil {
		return ErrInvalidSignature
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	want := mac.Sum(nil)

	if !hmac.Equal(got, want) {
		return ErrInvalidSignature
	}
	return nil
}

// ComputeSignature is the inverse of VerifyHMAC — used by the content-
// repo release.yml to sign outgoing requests. Returned header value is
// "sha256=<hex>".
func ComputeSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// IngestHandler is an http.Handler that verifies the HMAC + parses the
// payload, then hands off to fn for the actual fetch + activate work.
//
// The handler itself does NO fetching — that's the bundle fetcher's job
// (see Fetcher in fetcher.go) — so this handler stays trivially
// testable.
type IngestHandler struct {
	Secret string
	OnPayload func(IngestPayload) error // host-supplied; usually queues a fetch
}

func (h *IngestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	sig := r.Header.Get(SignatureHeader)
	if err := VerifyHMAC(sig, body, h.Secret); err != nil {
		// V8: 401 on signature mismatch. Body is neutral — don't leak
		// which of (missing | malformed | mismatch) failed.
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var p IngestPayload
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := p.Validate(); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	if h.OnPayload != nil {
		if err := h.OnPayload(p); err != nil {
			// V9: replay / version regression → 409. Other errors → 500.
			if errors.Is(err, ErrVersionRegression) {
				http.Error(w, "version regression", http.StatusConflict)
				return
			}
			http.Error(w, fmt.Sprintf("ingest: %v", err), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"status":"accepted"}`))
}

// ErrVersionRegression is returned by OnPayload when the incoming
// bundle version is not monotonic (V9).
var ErrVersionRegression = errors.New("ingest: version regression")
