// Package monitoring exposes per-tenant request counters and a
// Prometheus-compatible /metrics endpoint.
//
// Per gocodealone-multisite SPEC.md T30.
//
// Intentionally dependency-free — no prometheus client library. The
// /metrics body uses the OpenMetrics text exposition format, which
// Prometheus + Grafana Agent + DataDog scrape cleanly.
package monitoring

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Counters tracks per-tenant request counts + a global aggregate.
type Counters struct {
	mu        sync.RWMutex
	perTenant map[string]*tenantCounters
	global    tenantCounters
	startedAt time.Time
}

type tenantCounters struct {
	requests atomic.Int64
	errors   atomic.Int64
}

// New returns a fresh Counters.
func New() *Counters {
	return &Counters{
		perTenant: map[string]*tenantCounters{},
		startedAt: time.Now(),
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

// ServeHTTP is the /metrics handler.
func (c *Counters) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var b strings.Builder
	uptime := time.Since(c.startedAt).Seconds()
	fmt.Fprintf(&b, "# HELP multisite_uptime_seconds Time since the process started.\n")
	fmt.Fprintf(&b, "# TYPE multisite_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "multisite_uptime_seconds %.0f\n", uptime)

	fmt.Fprintf(&b, "# HELP multisite_requests_total Total HTTP requests served.\n")
	fmt.Fprintf(&b, "# TYPE multisite_requests_total counter\n")
	fmt.Fprintf(&b, "multisite_requests_total %d\n", c.global.requests.Load())

	fmt.Fprintf(&b, "# HELP multisite_request_errors_total Total responses with status >= 500.\n")
	fmt.Fprintf(&b, "# TYPE multisite_request_errors_total counter\n")
	fmt.Fprintf(&b, "multisite_request_errors_total %d\n", c.global.errors.Load())

	fmt.Fprintf(&b, "# HELP multisite_tenant_requests_total Total requests served per tenant.\n")
	fmt.Fprintf(&b, "# TYPE multisite_tenant_requests_total counter\n")

	c.mu.RLock()
	tenants := make([]string, 0, len(c.perTenant))
	for t := range c.perTenant {
		tenants = append(tenants, t)
	}
	c.mu.RUnlock()
	sort.Strings(tenants)

	for _, t := range tenants {
		c.mu.RLock()
		tc := c.perTenant[t]
		c.mu.RUnlock()
		fmt.Fprintf(&b, "multisite_tenant_requests_total{tenant=%q} %d\n", escapeLabel(t), tc.requests.Load())
		fmt.Fprintf(&b, "multisite_tenant_request_errors_total{tenant=%q} %d\n", escapeLabel(t), tc.errors.Load())
	}

	_, _ = w.Write([]byte(b.String()))
}

// escapeLabel sanitises a Prometheus label value. The grammar allows
// printable chars except `"`, `\`, and newlines — strip those.
func escapeLabel(s string) string {
	r := strings.NewReplacer(`\`, ``, `"`, ``, "\n", ``, "\r", ``)
	return r.Replace(s)
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
