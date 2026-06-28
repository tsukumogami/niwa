package cli

import (
	"github.com/tsukumogami/niwa/internal/config"
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

// configurePluginAutoInstall wires the plugin auto-installer onto an
// Applier. The applier's InstallNiwaPlugin function-field seam stays
// nil if this helper is never called — meaning the rank-2-triggered
// install is a no-op. Every CLI surface that constructs an Applier
// (apply, create, reset, …) must call this helper so the auto-install
// fires consistently regardless of which command surfaced the rank-2
// notice. flagOptOut is the per-invocation --no-install-plugins value;
// the persistent auto_install_plugins = false global-config setting
// is OR'd in here so callers don't have to load GlobalConfig twice.
func configurePluginAutoInstall(applier *workspace.Applier, flagOptOut bool) {
	skipFromGlobal := false
	if globalCfg, gErr := config.LoadGlobalConfig(); gErr == nil {
		skipFromGlobal = globalCfg.SkipPluginInstall()
	}
	applier.SkipPluginInstall = flagOptOut || skipFromGlobal
	applier.InstallNiwaPlugin = installNiwaPluginAdapter
	applier.PrewarmDeclaredPlugins = prewarmDeclaredPlugins
}
