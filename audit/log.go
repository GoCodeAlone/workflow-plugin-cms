// Package audit emits HMAC-signed audit-chain entries for admin
// mutations. Per gocodealone-multisite SPEC.md T26.
//
// The chain links each entry to the prior entry's signature, so any
// row mutation invalidates the chain from that point forward. The
// signing key (MULTISITE_AUDIT_SIGN_KEY) is rotated via the
// `wfctl multisite audit-rotate` runbook (docs/runbook/backup.md).
//
// In-memory Sink ships here; production wires a postgres-backed Sink
// via workflow-plugin-audit-chain (separate module).
package audit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// Entry is one audit record.
type Entry struct {
	ID         int64
	OccurredAt time.Time
	Actor      string // user_id (or "system" for bootstrap entries)
	TenantID   int64  // 0 for cross-tenant ops
	Action     string // "tenant.create", "page.update", etc.
	Subject    string // resource id ("page:42")
	Meta       map[string]any
	PrevSig    string // hex; "" for the first entry
	Sig        string // HMAC-SHA256(prev_sig || canonical(entry))
}

// canonicalBody returns the entry body in a deterministic JSON form
// used for the HMAC input.
func (e Entry) canonicalBody() ([]byte, error) {
	return json.Marshal(struct {
		ID         int64          `json:"id"`
		OccurredAt time.Time      `json:"occurred_at"`
		Actor      string         `json:"actor"`
		TenantID   int64          `json:"tenant_id"`
		Action     string         `json:"action"`
		Subject    string         `json:"subject"`
		Meta       map[string]any `json:"meta"`
	}{
		ID: e.ID, OccurredAt: e.OccurredAt, Actor: e.Actor,
		TenantID: e.TenantID, Action: e.Action, Subject: e.Subject, Meta: e.Meta,
	})
}

// Sink persists audit entries.
type Sink interface {
	Append(entry Entry) (Entry, error)
	List(tenantID int64) ([]Entry, error)
}

// Logger produces signed entries + appends them to the configured Sink.
type Logger struct {
	mu      sync.Mutex
	signKey []byte
	sink    Sink
	nextID  int64
	lastSig string
}

// New returns a Logger. signKey must be non-empty.
func New(signKey string, sink Sink) *Logger {
	if sink == nil {
		sink = NewMemorySink()
	}
	return &Logger{
		signKey: []byte(signKey),
		sink:    sink,
	}
}

// Record signs + appends one entry. Always returns the canonical
// stored copy.
func (l *Logger) Record(actor string, tenantID int64, action, subject string, meta map[string]any) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.nextID++
	e := Entry{
		ID:         l.nextID,
		OccurredAt: time.Now().UTC(),
		Actor:      actor,
		TenantID:   tenantID,
		Action:     action,
		Subject:    subject,
		Meta:       meta,
		PrevSig:    l.lastSig,
	}

	body, err := e.canonicalBody()
	if err != nil {
		return Entry{}, err
	}
	mac := hmac.New(sha256.New, l.signKey)
	_, _ = mac.Write([]byte(l.lastSig))
	_, _ = mac.Write(body)
	e.Sig = hex.EncodeToString(mac.Sum(nil))

	stored, err := l.sink.Append(e)
	if err != nil {
		// Roll back the ID so the next attempt does not skip.
		l.nextID--
		return Entry{}, err
	}
	l.lastSig = e.Sig
	return stored, nil
}

// Verify walks the chain returned by Sink.List + returns the first
// index that breaks (i.e. the first row whose stored Sig does not
// match the recomputed value). Returns -1 if the whole chain verifies.
func (l *Logger) Verify(tenantID int64) (int, error) {
	entries, err := l.sink.List(tenantID)
	if err != nil {
		return -1, err
	}
	prev := ""
	for i, e := range entries {
		body, err := e.canonicalBody()
		if err != nil {
			return i, err
		}
		mac := hmac.New(sha256.New, l.signKey)
		_, _ = mac.Write([]byte(prev))
		_, _ = mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		if want != e.Sig {
			return i, nil
		}
		prev = e.Sig
	}
	return -1, nil
}

// MemorySink stores audit entries in-process. Used for tests + local dev.
type MemorySink struct {
	mu      sync.RWMutex
	entries []Entry
}

func NewMemorySink() *MemorySink {
	return &MemorySink{}
}

func (s *MemorySink) Append(e Entry) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return e, nil
}

func (s *MemorySink) List(tenantID int64) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		if tenantID != 0 && e.TenantID != tenantID && e.TenantID != 0 {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
