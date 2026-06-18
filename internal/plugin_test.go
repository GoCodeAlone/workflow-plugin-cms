package internal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

func TestManifest_Identity(t *testing.T) {
	p := NewPlugin()
	cms, ok := p.(*CMSPlugin)
	if !ok {
		t.Fatalf("expected *CMSPlugin, got %T", p)
	}
	m := cms.Manifest()
	if m.Name != "workflow-plugin-cms" {
		t.Errorf("name: got %q want workflow-plugin-cms", m.Name)
	}
	if m.Author != "GoCodeAlone" {
		t.Errorf("author: got %q want GoCodeAlone", m.Author)
	}
}

func TestModuleTypes_All(t *testing.T) {
	p := NewPlugin().(*CMSPlugin)
	want := map[string]bool{
		"cms.tenant_resolver":             true,
		"cms.static_serve_before_dynamic": true,
		"cms.engine":                      true,
		"analytics.injection":             true,
	}
	got := p.ModuleTypes()
	if len(got) != len(want) {
		t.Errorf("module count: got %d want %d (%v)", len(got), len(want), got)
	}
	for _, mt := range got {
		if !want[mt] {
			t.Errorf("unexpected module type: %s", mt)
		}
	}
}

func TestCreateModule_AllTypesInstantiate(t *testing.T) {
	p := NewPlugin().(*CMSPlugin)
	for _, mt := range p.ModuleTypes() {
		m, err := p.CreateModule(mt, "test", map[string]any{})
		if err != nil {
			t.Errorf("CreateModule(%s): %v", mt, err)
		}
		if m == nil {
			t.Errorf("CreateModule(%s) returned nil module", mt)
		}
	}
}

func TestCreateModule_UnknownType(t *testing.T) {
	p := NewPlugin().(*CMSPlugin)
	_, err := p.CreateModule("unknown.type", "x", nil)
	if err == nil {
		t.Fatal("expected error for unknown module type, got nil")
	}
}

func TestStepTypes_Listed(t *testing.T) {
	p := NewPlugin().(*CMSPlugin)
	want := map[string]bool{
		"step.cms_render_page":     true,
		"step.cms_bundle_activate": true,
	}
	for _, st := range p.StepTypes() {
		if !want[st] {
			t.Errorf("unexpected step type: %s", st)
		}
	}
}

func TestCreateStep_RenderPageExecutes(t *testing.T) {
	p := NewPlugin().(*CMSPlugin)
	step, err := p.CreateStep("step.cms_render_page", "render", map[string]any{
		"content_type": "text/html; charset=utf-8",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := step.Execute(context.Background(), nil, nil, map[string]any{
		"tenant_id": int64(7),
		"path":      "/about",
		"title":     "About",
		"body_html": "<main>About</main>",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Output["html"] != "<main>About</main>" || got.Output["path"] != "/about" || got.Output["tenant_id"] != int64(7) {
		t.Fatalf("render output = %+v", got.Output)
	}
}

func TestCreateStep_BundleActivateExecutesValidation(t *testing.T) {
	p := NewPlugin().(*CMSPlugin)
	step, err := p.CreateStep("step.cms_bundle_activate", "activate", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := step.Execute(context.Background(), nil, nil, nil, nil, nil); err == nil {
		t.Fatal("expected missing bundle activation config to fail")
	}
}

func TestCreateStep_UnknownType(t *testing.T) {
	p := NewPlugin().(*CMSPlugin)
	if _, err := p.CreateStep("step.unknown", "x", nil); err == nil {
		t.Fatal("expected error for unknown step type")
	}
}

func TestContractRegistry_DeclaresStrictDescriptors(t *testing.T) {
	provider, ok := NewPlugin().(sdk.ContractProvider)
	if !ok {
		t.Fatal("plugin does not expose ContractRegistry")
	}

	registry := provider.ContractRegistry()
	if registry == nil {
		t.Fatal("ContractRegistry returned nil")
	}
	if registry.FileDescriptorSet == nil || len(registry.FileDescriptorSet.File) == 0 {
		t.Fatal("ContractRegistry missing file descriptors")
	}

	contracts := map[string]*pb.ContractDescriptor{}
	for _, contract := range registry.Contracts {
		if contract.Mode != pb.ContractMode_CONTRACT_MODE_STRICT_PROTO {
			t.Fatalf("%s mode = %s, want strict proto", contractKey(contract), contract.Mode)
		}
		key := contractKey(contract)
		if _, exists := contracts[key]; exists {
			t.Fatalf("duplicate contract %q", key)
		}
		contracts[key] = contract
	}

	requireRuntimeContract(t, contracts, "module:cms.tenant_resolver", "workflow.plugins.cms.v1.TenantResolverConfig", "", "")
	requireRuntimeContract(t, contracts, "module:cms.static_serve_before_dynamic", "workflow.plugins.cms.v1.StaticServeBeforeDynamicConfig", "", "")
	requireRuntimeContract(t, contracts, "module:cms.engine", "workflow.plugins.cms.v1.EngineConfig", "", "")
	requireRuntimeContract(t, contracts, "module:analytics.injection", "workflow.plugins.cms.v1.AnalyticsInjectionConfig", "", "")
	requireRuntimeContract(t, contracts, "step:step.cms_render_page", "workflow.plugins.cms.v1.CMSRenderPageStepConfig", "workflow.plugins.cms.v1.CMSRenderPageStepInput", "workflow.plugins.cms.v1.CMSRenderPageStepOutput")
	requireRuntimeContract(t, contracts, "step:step.cms_bundle_activate", "workflow.plugins.cms.v1.CMSBundleActivateStepConfig", "workflow.plugins.cms.v1.CMSBundleActivateStepInput", "workflow.plugins.cms.v1.CMSBundleActivateStepOutput")
	requireRuntimeContract(t, contracts, "service:cms.engine/CMSEngine/AdminContribution", "", "workflow.plugins.cms.v1.CMSAdminContributionInput", "workflow.plugins.cms.v1.CMSAdminContributionOutput")
}

func TestPluginContractsJSONMatchesRuntimeRegistry(t *testing.T) {
	provider := NewPlugin().(sdk.ContractProvider)
	runtimeContracts := map[string]*pb.ContractDescriptor{}
	for _, contract := range provider.ContractRegistry().Contracts {
		runtimeContracts[contractKey(contract)] = contract
	}

	manifestContracts := readPluginContracts(t)
	if len(manifestContracts) != len(runtimeContracts) {
		t.Fatalf("manifest contract count = %d, runtime = %d", len(manifestContracts), len(runtimeContracts))
	}
	for key, manifest := range manifestContracts {
		runtimeContract, ok := runtimeContracts[key]
		if !ok {
			t.Fatalf("%s missing from runtime contracts", key)
		}
		if manifest.Config != runtimeContract.ConfigMessage || manifest.Input != runtimeContract.InputMessage || manifest.Output != runtimeContract.OutputMessage {
			t.Fatalf("%s manifest = %#v, runtime config/input/output = %q/%q/%q", key, manifest, runtimeContract.ConfigMessage, runtimeContract.InputMessage, runtimeContract.OutputMessage)
		}
	}
}

func contractKey(contract *pb.ContractDescriptor) string {
	switch contract.Kind {
	case pb.ContractKind_CONTRACT_KIND_MODULE:
		return "module:" + contract.ModuleType
	case pb.ContractKind_CONTRACT_KIND_STEP:
		return "step:" + contract.StepType
	case pb.ContractKind_CONTRACT_KIND_SERVICE:
		return "service:" + contract.ModuleType + "/" + contract.ServiceName + "/" + contract.Method
	case pb.ContractKind_CONTRACT_KIND_TRIGGER:
		return "trigger:" + contract.TriggerType
	default:
		return "unknown"
	}
}

func requireRuntimeContract(t *testing.T, contracts map[string]*pb.ContractDescriptor, key, config, input, output string) {
	t.Helper()
	contract, ok := contracts[key]
	if !ok {
		t.Fatalf("missing contract %q", key)
	}
	if contract.ConfigMessage != config {
		t.Fatalf("%s config = %q, want %q", key, contract.ConfigMessage, config)
	}
	if contract.InputMessage != input {
		t.Fatalf("%s input = %q, want %q", key, contract.InputMessage, input)
	}
	if contract.OutputMessage != output {
		t.Fatalf("%s output = %q, want %q", key, contract.OutputMessage, output)
	}
}

type manifestContract struct {
	Config string `json:"config"`
	Input  string `json:"input"`
	Output string `json:"output"`
}

func readPluginContracts(t *testing.T) map[string]manifestContract {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "plugin.contracts.json"))
	if err != nil {
		t.Fatalf("read plugin.contracts.json: %v", err)
	}
	var manifest struct {
		Version   string `json:"version"`
		Contracts []struct {
			Kind        string `json:"kind"`
			Type        string `json:"type"`
			ServiceName string `json:"serviceName"`
			Method      string `json:"method"`
			Mode        string `json:"mode"`
			manifestContract
		} `json:"contracts"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse plugin.contracts.json: %v", err)
	}
	if manifest.Version != "v1" {
		t.Fatalf("plugin.contracts.json version = %q, want v1", manifest.Version)
	}
	contracts := make(map[string]manifestContract, len(manifest.Contracts))
	for _, contract := range manifest.Contracts {
		if contract.Mode != "strict" {
			t.Fatalf("%s mode = %q, want strict", contract.Type, contract.Mode)
		}
		var key string
		switch contract.Kind {
		case "module":
			key = "module:" + contract.Type
		case "step":
			key = "step:" + contract.Type
		case "service_method":
			key = "service:" + contract.Type + "/" + contract.ServiceName + "/" + contract.Method
		default:
			t.Fatalf("unexpected contract kind %q", contract.Kind)
		}
		if _, exists := contracts[key]; exists {
			t.Fatalf("duplicate manifest contract %q", key)
		}
		contracts[key] = contract.manifestContract
	}
	return contracts
}
