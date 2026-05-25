// Package internal implements the workflow-plugin-cms plugin — the CMS
// engine used by gocodealone-multisite to serve multi-tenant content
// sites from a single workflow app.
//
// Module types provided:
//   - cms.tenant_resolver         — Host → tenant_id middleware (V1, V2)
//   - cms.static_serve_before_dynamic — static-wins routing (V3)
//   - cms.engine                  — page CRUD + render + dynamic-section
//     substitution + theme resolver
//   - analytics.injection         — per-tenant Google Analytics injection
//     (delegated to workflow-plugin-analytics
//     when present; V25)
//
// Step types provided:
//   - step.cms_render_page        — render a CMS page for the resolved tenant
//   - step.cms_bundle_activate    — atomic-swap a fetched bundle into place
package internal

import (
	"fmt"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// Version is set at build time via -ldflags.
var Version = "0.0.0"

// CMSPlugin implements sdk.PluginProvider.
type CMSPlugin struct{}

// NewPlugin returns a new plugin instance.
func NewPlugin() sdk.PluginProvider {
	return &CMSPlugin{}
}

// Manifest returns plugin metadata.
func (p *CMSPlugin) Manifest() sdk.PluginManifest {
	return sdk.PluginManifest{
		Name:        "workflow-plugin-cms",
		Version:     Version,
		Author:      "GoCodeAlone",
		Description: "Multi-tenant CMS engine — TenantResolver + static-wins routing + WYSIWYG page authoring (TipTap default).",
	}
}

// ModuleTypes returns the module types this plugin provides.
func (p *CMSPlugin) ModuleTypes() []string {
	return []string{
		"cms.tenant_resolver",
		"cms.static_serve_before_dynamic",
		"cms.engine",
		"analytics.injection",
	}
}

// CreateModule creates a module instance of the given type.
func (p *CMSPlugin) CreateModule(typeName, name string, config map[string]any) (sdk.ModuleInstance, error) {
	switch typeName {
	case "cms.tenant_resolver":
		return newTenantResolverModule(name, config)
	case "cms.static_serve_before_dynamic":
		return newStaticServeModule(name, config)
	case "cms.engine":
		return newEngineModule(name, config)
	case "analytics.injection":
		return newAnalyticsInjectionModule(name, config)
	default:
		return nil, fmt.Errorf("unknown module type: %s", typeName)
	}
}

// StepTypes returns the step types this plugin provides.
func (p *CMSPlugin) StepTypes() []string {
	return []string{
		"step.cms_render_page",
		"step.cms_bundle_activate",
	}
}

// CreateStep creates a pipeline step instance of the given type.
func (p *CMSPlugin) CreateStep(typeName, name string, config map[string]any) (sdk.StepInstance, error) {
	switch typeName {
	case "step.cms_render_page":
		return newRenderPageStep(name, config), nil
	case "step.cms_bundle_activate":
		return newBundleActivateStep(name, config), nil
	default:
		return nil, fmt.Errorf("unknown step type: %s", typeName)
	}
}
