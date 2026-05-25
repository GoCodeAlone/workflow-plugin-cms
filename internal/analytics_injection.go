package internal

import (
	"context"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// analyticsInjectionModule injects gtag.js into rendered pages when the
// resolved tenant has `analytics.google.measurement_id` set in their
// multisite.yaml (SPEC §V25).
type analyticsInjectionModule struct {
	name string
	mode string // "per_tenant" — accepts ID from tenant's multisite.yaml
}

func newAnalyticsInjectionModule(name string, config map[string]any) (sdk.ModuleInstance, error) {
	m := &analyticsInjectionModule{name: name, mode: "per_tenant"}
	if v, ok := config["mode"].(string); ok {
		m.mode = v
	}
	return m, nil
}

func (m *analyticsInjectionModule) Name() string { return m.name }

func (m *analyticsInjectionModule) Init() error                     { return nil }
func (m *analyticsInjectionModule) Start(ctx context.Context) error { return nil }
func (m *analyticsInjectionModule) Stop(ctx context.Context) error  { return nil }
