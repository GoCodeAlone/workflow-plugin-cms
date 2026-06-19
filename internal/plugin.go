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

	"github.com/GoCodeAlone/workflow-plugin-cms/internal/contracts"
	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
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

// ContractRegistry returns the CMS plugin's strict protobuf contracts.
func (p *CMSPlugin) ContractRegistry() *pb.ContractRegistry {
	return &pb.ContractRegistry{
		FileDescriptorSet: &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{
			protodesc.ToFileDescriptorProto(contracts.File_internal_contracts_cms_proto),
		}},
		Contracts: []*pb.ContractDescriptor{
			cmsModuleContract("cms.tenant_resolver", "TenantResolverConfig"),
			cmsModuleContract("cms.static_serve_before_dynamic", "StaticServeBeforeDynamicConfig"),
			cmsModuleContract("cms.engine", "EngineConfig"),
			cmsModuleContract("analytics.injection", "AnalyticsInjectionConfig"),
			cmsStepContract("step.cms_render_page", "CMSRenderPageStepConfig", "CMSRenderPageStepInput", "CMSRenderPageStepOutput"),
			cmsStepContract("step.cms_bundle_activate", "CMSBundleActivateStepConfig", "CMSBundleActivateStepInput", "CMSBundleActivateStepOutput"),
			cmsServiceContract("cms.engine", "CMSEngine", "AdminContribution", "CMSAdminContributionInput", "CMSAdminContributionOutput"),
		},
	}
}

func cmsModuleContract(moduleType, configMessage string) *pb.ContractDescriptor {
	const pkg = "workflow.plugins.cms.v1."
	return &pb.ContractDescriptor{
		Kind:          pb.ContractKind_CONTRACT_KIND_MODULE,
		ModuleType:    moduleType,
		ConfigMessage: pkg + configMessage,
		Mode:          pb.ContractMode_CONTRACT_MODE_STRICT_PROTO,
	}
}

func cmsStepContract(stepType, configMessage, inputMessage, outputMessage string) *pb.ContractDescriptor {
	const pkg = "workflow.plugins.cms.v1."
	return &pb.ContractDescriptor{
		Kind:          pb.ContractKind_CONTRACT_KIND_STEP,
		StepType:      stepType,
		ConfigMessage: pkg + configMessage,
		InputMessage:  pkg + inputMessage,
		OutputMessage: pkg + outputMessage,
		Mode:          pb.ContractMode_CONTRACT_MODE_STRICT_PROTO,
	}
}

func cmsServiceContract(moduleType, serviceName, method, inputMessage, outputMessage string) *pb.ContractDescriptor {
	const pkg = "workflow.plugins.cms.v1."
	return &pb.ContractDescriptor{
		Kind:          pb.ContractKind_CONTRACT_KIND_SERVICE,
		ModuleType:    moduleType,
		ServiceName:   serviceName,
		Method:        method,
		InputMessage:  pkg + inputMessage,
		OutputMessage: pkg + outputMessage,
		Mode:          pb.ContractMode_CONTRACT_MODE_STRICT_PROTO,
	}
}
