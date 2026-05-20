// Package media implements tenant-scoped media upload + retrieval.
//
// Per gocodealone-multisite SPEC.md T25.
//
// Two backends are supported via the Backend interface:
//   - LocalFS — files written under <root>/<tenant_id>/<sha256>.<ext>.
//   - Spaces — DigitalOcean Spaces (S3-compatible) via presigned URL.
//
// The LocalFS backend ships in this file; Spaces lives in
// media/spaces.go (added when the host needs it).
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MaxUploadBytes is the default per-request cap (10 MiB).
const MaxUploadBytes = 10 * 1024 * 1024

// Backend persists + retrieves tenant-scoped media.
type Backend interface {
	// Put stores body for tenantID, returning a stable public URL.
	Put(tenantID int64, body io.Reader, contentType string) (string, error)
	// Get returns the file contents + content-type for a stored URL.
	// Implementations may return ErrNotFound.
	Get(url string) (io.ReadCloser, string, error)
}

// ErrNotFound is returned by Backend.Get on a miss.
var ErrNotFound = errors.New("media: not found")

// ErrTooLarge is returned by the handler when the upload exceeds the
// configured cap.
var ErrTooLarge = errors.New("media: payload too large")

// LocalFS is a development backend that writes under root.
//
// Path layout: <root>/<tenant_id>/<sha256>.<ext>. The sha256 is
// content-addressed — uploading the same file twice yields the same
// URL (idempotent).
type LocalFS struct {
	Root      string
	PublicURL string // e.g. "https://gocodealone.tech/media"; appended with /<tenant>/<sha>.<ext>
}

func (l *LocalFS) Put(tenantID int64, body io.Reader, contentType string) (string, error) {
	if tenantID == 0 {
		return "", errors.New("media: tenant_id required")
	}
	if l.Root == "" {
		return "", errors.New("media: LocalFS.Root not configured")
	}

	limited := io.LimitReader(body, MaxUploadBytes+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if len(buf) > MaxUploadBytes {
		return "", ErrTooLarge
	}

	sum := sha256.Sum256(buf)
	hash := hex.EncodeToString(sum[:])

	ext := extFromContentType(contentType)
	name := hash + ext

	tenantDir := filepath.Join(l.Root, strconv.FormatInt(tenantID, 10))
	if err := os.MkdirAll(tenantDir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(tenantDir, name)
	if _, err := os.Stat(target); err == nil {
		// Idempotent — already present.
		return l.publicURL(tenantID, name), nil
	}
	if err := os.WriteFile(target, buf, 0o644); err != nil {
		return "", err
	}
	return l.publicURL(tenantID, name), nil
}

func (l *LocalFS) Get(url string) (io.ReadCloser, string, error) {
	// Parse <publicURL>/<tenant>/<sha>.<ext> back into a filesystem path.
	if l.PublicURL != "" && strings.HasPrefix(url, l.PublicURL) {
		url = strings.TrimPrefix(url, l.PublicURL)
	}
	url = strings.TrimPrefix(url, "/")
	parts := strings.SplitN(url, "/", 2)
	if len(parts) != 2 {
		return nil, "", ErrNotFound
	}
	tenant, name := parts[0], parts[1]
	// Reject any path traversal attempt.
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		return nil, "", ErrNotFound
	}
	target := filepath.Join(l.Root, tenant, name)
	f, err := os.Open(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	ct := contentTypeFromExt(filepath.Ext(name))
	return f, ct, nil
}

func (l *LocalFS) publicURL(tenantID int64, name string) string {
	base := strings.TrimRight(l.PublicURL, "/")
	if base == "" {
		return fmt.Sprintf("/media/%d/%s", tenantID, name)
	}
	return fmt.Sprintf("%s/%d/%s", base, tenantID, name)
}

func extFromContentType(ct string) string {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ""
	}
	switch strings.ToLower(mt) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	case "application/pdf":
		return ".pdf"
	case "text/plain":
		return ".txt"
	default:
		return ""
	}
}

func contentTypeFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

// UploadHandler is an http.Handler that consumes multipart/form-data
// uploads under POST /api/v1/admin/tenants/:id/upload. Tenant ID is
// extracted from the URL by the caller.
//
// The handler:
//   1. Caps the read at MaxUploadBytes + 1 byte to detect overflow.
//   2. Hands the body to the configured Backend.
//   3. Returns 201 + {url, content_type, bytes} on success.
type UploadHandler struct {
	Backend Backend
}

// ServeForTenant performs an upload scoped to tenantID. Returns the
// stored URL.
func (h *UploadHandler) ServeForTenant(w http.ResponseWriter, r *http.Request, tenantID int64) {
	if h.Backend == nil {
		http.Error(w, "media backend not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Use the body directly — clients can also send multipart but the
	// admin UI ships single-blob uploads; this keeps the surface small.
	ct := r.Header.Get("Content-Type")
	url, err := h.Backend.Put(tenantID, r.Body, ct)
	if err != nil {
		switch {
		case errors.Is(err, ErrTooLarge):
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = fmt.Fprintf(w, `{"url":%q,"content_type":%q}`, url, ct)
}
