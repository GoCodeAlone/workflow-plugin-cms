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
	"github.com/GoCodeAlone/workflow-plugin-cms/internal"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

func main() {
	sdk.Serve(internal.NewPlugin(), sdk.WithBuildVersion(sdk.ResolveBuildVersion(internal.Version)))
}
