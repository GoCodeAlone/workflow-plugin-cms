package cms_test

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

type releaseManifest struct {
	Name             string              `json:"name"`
	Version          string              `json:"version"`
	MinEngineVersion string              `json:"minEngineVersion"`
	Capabilities     releaseCapabilities `json:"capabilities"`
	Downloads        []releaseDownload   `json:"downloads"`
}

type releaseCapabilities struct {
	ModuleTypes []string `json:"moduleTypes"`
	StepTypes   []string `json:"stepTypes"`
}

type releaseDownload struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	URL  string `json:"url"`
}

type releaseContractsFile struct {
	Contracts []releaseContract `json:"contracts"`
}

type releaseContract struct {
	Kind        string `json:"kind"`
	Type        string `json:"type"`
	ServiceName string `json:"serviceName,omitempty"`
	Method      string `json:"method,omitempty"`
	Mode        string `json:"mode"`
	Config      string `json:"config,omitempty"`
	Input       string `json:"input,omitempty"`
	Output      string `json:"output,omitempty"`
}

func TestReleaseMetadataIsPublishable(t *testing.T) {
	manifestData, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest releaseManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}

	if manifest.MinEngineVersion != "0.64.4" {
		t.Fatalf("minEngineVersion = %q, want 0.64.4", manifest.MinEngineVersion)
	}

	wantDownloads := []string{
		"darwin/amd64",
		"darwin/arm64",
		"linux/amd64",
		"linux/arm64",
	}
	gotDownloads := make([]string, 0, len(manifest.Downloads))
	for _, dl := range manifest.Downloads {
		gotDownloads = append(gotDownloads, dl.OS+"/"+dl.Arch)
		if !strings.Contains(dl.URL, "github.com/GoCodeAlone/workflow-plugin-cms/releases/download/v0.0.0/") {
			t.Fatalf("download URL %q must use the releaser placeholder tag", dl.URL)
		}
		if !strings.HasSuffix(dl.URL, "workflow-plugin-cms-"+dl.OS+"-"+dl.Arch+".tar.gz") {
			t.Fatalf("download URL %q does not match GoReleaser archive naming", dl.URL)
		}
	}
	sort.Strings(gotDownloads)
	if strings.Join(gotDownloads, ",") != strings.Join(wantDownloads, ",") {
		t.Fatalf("downloads = %v, want %v", gotDownloads, wantDownloads)
	}

	contractsData, err := os.ReadFile("plugin.contracts.json")
	if err != nil {
		t.Fatal(err)
	}
	var contracts releaseContractsFile
	if err := json.Unmarshal(contractsData, &contracts); err != nil {
		t.Fatal(err)
	}

	byKindType := map[string]releaseContract{}
	for _, contract := range contracts.Contracts {
		key := contract.Kind + "\x00" + contract.Type
		if contract.Kind == "service_method" {
			key += "\x00" + contract.Method
		}
		byKindType[key] = contract
	}
	for _, typ := range manifest.Capabilities.ModuleTypes {
		contract := byKindType["module\x00"+typ]
		if contract.Mode != "strict" || contract.Config == "" {
			t.Fatalf("module %q contract = %+v, want strict config descriptor", typ, contract)
		}
	}
	for _, typ := range manifest.Capabilities.StepTypes {
		contract := byKindType["step\x00"+typ]
		if contract.Mode != "strict" || contract.Input == "" || contract.Output == "" {
			t.Fatalf("step %q contract = %+v, want strict input/output descriptors", typ, contract)
		}
	}

	adminContribution := byKindType["service_method\x00cms.engine\x00AdminContribution"]
	if adminContribution.ServiceName != "CMSEngine" ||
		adminContribution.Mode != "strict" ||
		adminContribution.Input != "workflow.plugins.cms.v1.CMSAdminContributionInput" ||
		adminContribution.Output != "workflow.plugins.cms.v1.CMSAdminContributionOutput" {
		t.Fatalf("AdminContribution service contract = %+v, want strict CMSEngine descriptors", adminContribution)
	}
}

func TestGoReleaserStagesReleaseManifestWithoutMutatingRoot(t *testing.T) {
	data, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, want := range []string{
		"cp plugin.json cmd/workflow-plugin-cms/plugin.json",
		".release/plugin.json",
		"sed -i.bak 's|/releases/download/v[^/]*/|/releases/download/{{ .Tag }}/|g' .release/plugin.json",
		"plugin validate --file .release/plugin.json --strict-contracts",
		"dst: plugin.json",
		"draft: true",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf(".goreleaser.yaml missing %q", want)
		}
	}
	if strings.Contains(src, "sed -i.bak 's/\\\"version\\\": \\\".*\\\"/\\\"version\\\": \\\"{{ .Version }}\\\"/' plugin.json") {
		t.Fatal(".goreleaser.yaml must not mutate root plugin.json during release")
	}
}

func TestReleaseWorkflowFollowsCurrentPluginPattern(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, want := range []string{
		"GoCodeAlone/setup-wfctl@v1",
		"version: v0.64.4",
		"wfctl plugin validate-contract --for-publish --tag",
		"wfctl plugin verify-capabilities --binary",
		"Verify shipped plugin.json carries tag",
		"peter-evans/repository-dispatch@28959ce8df70de7be546dd1250a005dd32156697",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("release workflow missing %q", want)
		}
	}
	for _, stale := range []string{"wfctl v0.63.2", "workflow/releases/download/v0.63.2", "${{ runner.temp }}/wfctl-bin/wfctl", "repository-dispatch@v3"} {
		if strings.Contains(src, stale) {
			t.Fatalf("release workflow contains stale pattern %q", stale)
		}
	}
}

func TestCommandEmbedsCanonicalPluginManifest(t *testing.T) {
	if _, err := os.Stat("cmd/workflow-plugin-cms/plugin.json"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("cmd/workflow-plugin-cms/main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, want := range []string{"//go:embed plugin.json", "sdk.MustEmbedManifest(pluginJSON)", "sdk.WithManifestProvider(manifest)"} {
		if !strings.Contains(src, want) {
			t.Fatalf("main.go missing %q", want)
		}
	}
}

func TestReadmeDocumentsPageTemplateBackupFields(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, want := range []string{"body_blocks", "template_id", "publish_at", "unpublish_at", "backup", "restore"} {
		if !strings.Contains(src, want) {
			t.Fatalf("README.md missing persistence guidance for %q", want)
		}
	}
}
