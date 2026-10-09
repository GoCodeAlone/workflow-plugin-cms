// Package promotion prepares content-only publication artifacts. It deliberately
// has no network client or apply endpoint: host authority, bundle fencing and
// restart-safe activation are separate mandatory publication gates.
package promotion

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

var ErrInvalid = errors.New("content snapshot invalid")
var ErrBundle = errors.New("content bundle invalid or unsupported reference")
var ErrMapping = errors.New("content mapping invalid or ambiguous")

const SchemaVersion = 1
const maxSnapshotBytes = 8 << 20

var sitePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type SnapshotPage struct {
	Key           string            `json:"key"`
	SourceID      int64             `json:"source_id"`
	SourceVersion int               `json:"source_version"`
	Content       store.PageContent `json:"content"`
}
type Snapshot struct {
	Schema     int            `json:"schema"`
	SourceSite string         `json:"source_site"`
	CreatedAt  time.Time      `json:"created_at"`
	Pages      []SnapshotPage `json:"pages"`
	Bundle     BundleManifest `json:"bundle"`
	Digest     string         `json:"digest"`
}

func sumJSON(value any) string {
	b, _ := json.Marshal(value)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s Snapshot) ContentDigest() string { s.Digest = ""; return sumJSON(s) }

func (s Snapshot) Validate() error {
	if s.Schema != SchemaVersion || !sitePattern.MatchString(s.SourceSite) || s.CreatedAt.IsZero() || s.CreatedAt.Location() != time.UTC || len(s.Pages) == 0 || len(s.Pages) > 1000 || s.Digest != s.ContentDigest() {
		return ErrInvalid
	}
	keys := map[string]bool{}
	paths := map[string]bool{}
	for _, p := range s.Pages {
		if p.SourceID <= 0 || p.SourceVersion <= 0 || p.Key != s.SourceSite+":"+strconv.FormatInt(p.SourceID, 10) || keys[p.Key] {
			return ErrInvalid
		}
		keys[p.Key] = true
		if err := p.Content.Validate(); err != nil {
			return ErrInvalid
		}
		key := p.Content.Subsite + "\x00" + p.Content.Path
		if paths[key] {
			return ErrInvalid
		}
		paths[key] = true
	}
	if err := s.Bundle.Validate(); err != nil {
		return err
	}
	return nil
}

// Export freezes selected rows. It reads the supplied authorized tenant scope;
// it never consults a domain, credential, tenant setting or editor membership.
func Export(ctx context.Context, pages store.PageStore, sourceTenantID int64, sourceSite string, ids []int64, bundleRoot string, now time.Time) (Snapshot, error) {
	if !sitePattern.MatchString(sourceSite) || sourceTenantID <= 0 || len(ids) == 0 {
		return Snapshot{}, ErrInvalid
	}
	all, err := pages.List(ctx, sourceTenantID, "")
	if err != nil {
		return Snapshot{}, ErrInvalid
	}
	available := map[int64]*store.Page{}
	for _, p := range all {
		available[p.ID] = p
	}
	s := Snapshot{Schema: SchemaVersion, SourceSite: sourceSite, CreatedAt: now.UTC(), Pages: []SnapshotPage{}}
	seen := map[int64]bool{}
	for _, id := range ids {
		p := available[id]
		if p == nil || seen[id] {
			return Snapshot{}, ErrInvalid
		}
		seen[id] = true
		s.Pages = append(s.Pages, SnapshotPage{sourceSite + ":" + strconv.FormatInt(id, 10), id, p.Version, store.ContentOf(p)})
	}
	sort.Slice(s.Pages, func(i, j int) bool { return s.Pages[i].SourceID < s.Pages[j].SourceID })
	s.Bundle, err = InventoryBundle(bundleRoot)
	if err != nil {
		return Snapshot{}, err
	}
	contents := []store.PageContent{}
	for _, p := range s.Pages {
		contents = append(contents, p.Content)
	}
	if err = VerifyBundle(bundleRoot, s.Bundle, contents); err != nil {
		return Snapshot{}, err
	}
	s.Digest = s.ContentDigest()
	if err = s.Validate(); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

// Decode rejects unknown fields, duplicate JSON keys, trailing documents and
// oversize input. Errors are fixed; rejected keys/values are never returned.
func Decode(r io.Reader) (Snapshot, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxSnapshotBytes+1))
	if err != nil || len(b) > maxSnapshotBytes {
		return Snapshot{}, ErrInvalid
	}
	if !uniqueJSON(b) {
		return Snapshot{}, ErrInvalid
	}
	var s Snapshot
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&s) != nil || d.Decode(new(any)) != io.EOF {
		return Snapshot{}, ErrInvalid
	}
	if s.Validate() != nil {
		return Snapshot{}, ErrInvalid
	}
	return s, nil
}

func uniqueJSON(b []byte) bool {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value func() bool
	value = func() bool {
		t, err := d.Token()
		if err != nil {
			return false
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				s, ok := key.(string)
				if err != nil || !ok || keys[s] {
					return false
				}
				keys[s] = true
				if !value() {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !value() {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	return value() && func() bool { _, err := d.Token(); return err == io.EOF }()
}
