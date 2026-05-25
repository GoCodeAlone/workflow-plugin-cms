package host

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GoCodeAlone/workflow/telemetry"
)

type requestCounters struct {
	mu        sync.RWMutex
	perTenant map[string]*tenantCounters
	global    tenantCounters
	startedAt time.Time
}

type tenantCounters struct {
	requests atomic.Int64
	errors   atomic.Int64
}

type counterSnapshot struct {
	Requests int64
	Errors   int64
}

func newRequestCounters() *requestCounters {
	return &requestCounters{
		perTenant: map[string]*tenantCounters{},
		startedAt: time.Now(),
	}
}

func (c *requestCounters) inc(tenant string, statusCode int) {
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

func (c *requestCounters) snapshot() map[string]counterSnapshot {
	out := map[string]counterSnapshot{
		"_global": {
			Requests: c.global.requests.Load(),
			Errors:   c.global.errors.Load(),
		},
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for tenant, counters := range c.perTenant {
		out[tenant] = counterSnapshot{
			Requests: counters.requests.Load(),
			Errors:   counters.errors.Load(),
		}
	}
	return out
}

func (c *requestCounters) emit(rec telemetry.MetricRecorder) {
	snapshot := c.snapshot()
	global := snapshot["_global"]
	rec.Gauge("multisite_uptime_seconds", time.Since(c.startedAt).Seconds(), nil)
	rec.Counter("multisite_requests_total", float64(global.Requests), nil)
	rec.Counter("multisite_request_errors_total", float64(global.Errors), nil)
	for tenant, counters := range snapshot {
		if tenant == "_global" {
			continue
		}
		attrs := telemetry.Attrs{"tenant": tenant}
		rec.Counter("multisite_tenant_requests_total", float64(counters.Requests), attrs)
		rec.Counter("multisite_tenant_request_errors_total", float64(counters.Errors), attrs)
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
