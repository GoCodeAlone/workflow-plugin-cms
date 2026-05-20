package internal

import (
	"context"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// tenantResolverModule resolves the tenant_id from the request Host header
// (SPEC §V1, §V2, §V22) before any other middleware runs. Unknown domain
// → 404 neutral (V16).
//
// STUB — full implementation in T5.
type tenantResolverModule struct {
	name                  string
	previewSubdomainBase  string
	onUnknownDomain       string
}

func newTenantResolverModule(name string, config map[string]any) (sdk.ModuleInstance, error) {
	m := &tenantResolverModule{name: name}
	if v, ok := config["preview_subdomain_base"].(string); ok {
		m.previewSubdomainBase = v
	}
	if v, ok := config["on_unknown_domain"].(string); ok {
		m.onUnknownDomain = v
	}
	return m, nil
}

func (m *tenantResolverModule) Name() string  { return m.name }

func (m *tenantResolverModule) Init() error                  { return nil }
func (m *tenantResolverModule) Start(ctx context.Context) error { return nil }
func (m *tenantResolverModule) Stop(ctx context.Context) error  { return nil }
