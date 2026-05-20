package internal

import (
	"context"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// staticServeModule serves static files before falling through to
// dynamic CMS resolution (SPEC §V3, §C7).
//
// STUB — full implementation in T6.
type staticServeModule struct {
	name       string
	bundleRoot string
}

func newStaticServeModule(name string, config map[string]any) (sdk.ModuleInstance, error) {
	m := &staticServeModule{name: name}
	if v, ok := config["bundle_root"].(string); ok {
		m.bundleRoot = v
	}
	return m, nil
}

func (m *staticServeModule) Name() string  { return m.name }

func (m *staticServeModule) Init() error                  { return nil }
func (m *staticServeModule) Start(ctx context.Context) error { return nil }
func (m *staticServeModule) Stop(ctx context.Context) error  { return nil }
