package bundle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fetcher fetches a tarball from a GH release URL using a Bearer token,
// streams it through an integrity hash, un-tars into a versioned dir,
// and atomically swaps the `current` symlink.
//
// Layout on disk:
//
//	<root>/<tenant_slug>/<version>/...
//	<root>/<tenant_slug>/current → <version>
//
// `current` swap uses rename(2) semantics — readers see either the old
// target or the new target, never a partial state (V9 monotonic
// activation).
type Fetcher struct {
	// BundleRoot is the directory under which tenants are stored.
	BundleRoot string

	// Token is the GH PAT or App token used as `Authorization: Bearer`.
	// Required (V24) — never make an anonymous request.
	Token string

	// HTTPClient lets tests inject a recording client. Default is
	// http.DefaultClient with a 30s timeout if nil.
	HTTPClient *http.Client

	// Now is a clock injection point for tests.
	Now func() time.Time

	// MaxTarballBytes caps the downloaded size (defence against
	// resource exhaustion). Default 200 MiB.
	MaxTarballBytes int64
}

// Activate fetches the tarball, hashes it, extracts it under
// <BundleRoot>/<tenantSlug>/<version>/, then atomically swaps the
// `current` symlink. Returns the SHA-256 hex of the tarball for
// integrity recording.
func (f *Fetcher) Activate(ctx context.Context, tenantSlug, version, tarballURL string) (string, error) {
	if f.BundleRoot == "" {
		return "", errors.New("fetcher: BundleRoot required")
	}
	if f.Token == "" {
		return "", errors.New("fetcher: Token required (V24)")
	}
	if tenantSlug == "" || version == "" || tarballURL == "" {
		return "", errors.New("fetcher: tenantSlug, version, tarballURL all required")
	}
	if !strings.HasPrefix(tarballURL, "https://") {
		return "", errors.New("fetcher: tarball URL must be https")
	}

	cli := f.HTTPClient
	if cli == nil {
		cli = &http.Client{Timeout: 30 * time.Second}
	}
	maxBytes := f.MaxTarballBytes
	if maxBytes == 0 {
		maxBytes = 200 << 20
	}

	// Fetch the tarball.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tarballURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+f.Token)
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := cli.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch tarball: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch tarball: HTTP %d", resp.StatusCode)
	}

	// Extract to a temp dir under tenant root, hashing as we go.
	tenantDir := filepath.Join(f.BundleRoot, tenantSlug)
	if err := os.MkdirAll(tenantDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir tenant root: %w", err)
	}
	versionDir := filepath.Join(tenantDir, version)
	tmpDir, err := os.MkdirTemp(tenantDir, ".extract-*")
	if err != nil {
		return "", fmt.Errorf("mkdir temp: %w", err)
	}
	// Best-effort cleanup of temp on error.
	cleanup := tmpDir
	defer func() {
		if cleanup != "" {
			_ = os.RemoveAll(cleanup)
		}
	}()

	hasher := sha256.New()
	limited := io.LimitReader(resp.Body, maxBytes+1)
	teed := io.TeeReader(limited, hasher)

	if err := extractTarGz(teed, tmpDir, maxBytes); err != nil {
		return "", fmt.Errorf("extract: %w", err)
	}

	// Move temp dir to version dir.
	if _, err := os.Stat(versionDir); err == nil {
		// Version already exists — V9 regression unless content matches.
		return "", ErrVersionRegression
	}
	if err := os.Rename(tmpDir, versionDir); err != nil {
		return "", fmt.Errorf("rename version dir: %w", err)
	}
	cleanup = "" // success — don't delete versionDir

	// Atomic swap of `current` symlink.
	if err := atomicSymlink(versionDir, filepath.Join(tenantDir, "current")); err != nil {
		return "", fmt.Errorf("swap current symlink: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// extractTarGz unpacks a tar.gz stream into dest. Refuses any entry
// whose cleaned path escapes dest (zip-slip guard).
func extractTarGz(r io.Reader, dest string, maxBytes int64) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}

		// Zip-slip guard.
		clean := filepath.Clean(hdr.Name)
		if strings.Contains(clean, "..") || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
			return fmt.Errorf("tar entry escapes dest: %q", hdr.Name)
		}
		target := filepath.Join(dest, clean)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode&0o777))
			if err != nil {
				return fmt.Errorf("open %s: %w", target, err)
			}
			if _, err := io.CopyN(f, tr, hdr.Size); err != nil && err != io.EOF {
				_ = f.Close()
				return fmt.Errorf("copy %s: %w", target, err)
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Symlinks are accepted only if both target+source resolve
			// inside dest. Conservatively: skip symlinks. They're not
			// expected in a static site bundle.
			continue
		default:
			// Other types (block dev, char dev, fifo, etc.) — skip.
			continue
		}
	}
	return nil
}

// atomicSymlink writes a symlink at `path` pointing to `target` such
// that readers see either the old target or the new target, never a
// partial state.
//
// Strategy: write to a side-by-side temp name, then rename(2). On Linux
// + macOS, rename(2) over an existing symlink is atomic at the inode
// level.
func atomicSymlink(target, path string) error {
	tmp := path + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
