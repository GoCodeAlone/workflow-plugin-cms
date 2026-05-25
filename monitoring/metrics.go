// Package monitoring exposes dependency-free per-tenant request counters for
// CMS host observability adapters.
package monitoring

import (
	"net/http"
	"sync"
	"sync/atomic"
)

// Counters tracks per-tenant request counts + a global aggregate.
type Counters struct {
	mu        sync.RWMutex
	perTenant map[string]*tenantCounters
	global    tenantCounters
}

type tenantCounters struct {
	requests atomic.Int64
	errors   atomic.Int64
}

// New returns a fresh Counters.
func New() *Counters {
	return &Counters{
		perTenant: map[string]*tenantCounters{},
	}
}

// Inc records one request for tenant (empty = unresolved). statusCode
// >= 500 also increments the error counter.
func (c *Counters) Inc(tenant string, statusCode int) {
	c.global.requests.Add(1)
	if statusCode >= 500 {
		c.global.errors.Add(1)
	}
	if tenant == "" {
		return
	}
	c.mu.RLock()
	tc, ok := c.perTenant[tenant]
	c.mu.RUnlock()
	if !ok {
		c.mu.Lock()
		tc, ok = c.perTenant[tenant]
		if !ok {
			tc = &tenantCounters{}
			c.perTenant[tenant] = tc
		}
		c.mu.Unlock()
	}
	tc.requests.Add(1)
	if statusCode >= 500 {
		tc.errors.Add(1)
	}
}

// Snapshot returns a copy of the current totals (for tests).
func (c *Counters) Snapshot() map[string]int64 {
	out := map[string]int64{"_global": c.global.requests.Load()}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for k, v := range c.perTenant {
		out[k] = v.requests.Load()
	}
	return out
}

// StatusRecorder wraps http.ResponseWriter to capture the status code
// for the Counters.Inc call.
type StatusRecorder struct {
	http.ResponseWriter
	Status int
}

func (r *StatusRecorder) WriteHeader(code int) {
	r.Status = code
	r.ResponseWriter.WriteHeader(code)
}
