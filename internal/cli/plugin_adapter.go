package cli

import (
	"github.com/tsukumogami/niwa/internal/plugin"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// installNiwaPluginAdapter is the cli-side adapter that bridges
// workspace.Applier.InstallNiwaPlugin to plugin.Install. It lives in
// the cli package to break the workspace→plugin→workspace import
// cycle: workspace declares the seam as a function field, cli
// supplies the implementation at construction time.
func installNiwaPluginAdapter(state *workspace.InstanceState, reporter *workspace.Reporter, skipInstall bool) {
	plugin.Install(state, reporter, plugin.InstallOpts{SkipInstall: skipInstall})
}
