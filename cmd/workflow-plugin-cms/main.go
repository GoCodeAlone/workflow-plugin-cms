// Command workflow-plugin-cms is a workflow engine external plugin
// providing the CMS host functionality for gocodealone-multisite.
//
// It runs as a subprocess and communicates with the host workflow engine
// via the go-plugin gRPC protocol.
//
// See SPEC.md (this repo) for the full surface, and the parent
// gocodealone-multisite SPEC.md §I for how the host wires the plugin.
package main

import (
	_ "embed"

	"github.com/GoCodeAlone/workflow-plugin-cms/internal"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// pluginJSON is copied from the repository root by GoReleaser before builds
// and is committed for local builds/tests.
//
//go:embed plugin.json
var pluginJSON []byte

var manifest = sdk.MustEmbedManifest(pluginJSON)

func main() {
	sdk.Serve(internal.NewPlugin(),
		sdk.WithManifestProvider(manifest),
		sdk.WithBuildVersion(sdk.ResolveBuildVersion(internal.Version)))
}
