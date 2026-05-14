package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/plugin"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	pluginsCmd.AddCommand(pluginsInstallCmd)
	rootCmd.AddCommand(pluginsCmd)
}

var pluginsCmd = &cobra.Command{
	Use:   "plugins",
	Short: "Manage embedded niwa plugins",
	Long: `Manage embedded niwa plugins.

niwa ships the niwa Claude Code plugin (containing the
/niwa:migrate-config skill) bundled in the binary. The plugin is
normally auto-installed once when niwa detects a rank-2 source
layout, but users who opted out via --no-install-plugins or set
auto_install_plugins = false in their global config can install it
manually via:

    niwa plugins install`,
}

var pluginsInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the embedded niwa Claude Code plugin",
	Long: `Install the embedded niwa Claude Code plugin under
~/.claude/plugins/marketplaces/niwa/.

Use this when you previously opted out of the auto-install path
(via --no-install-plugins or auto_install_plugins = false) and
want to install the plugin now. The command is idempotent — when
the on-disk plugin already matches the embedded version it reports
"up to date" and exits.`,
	RunE: runPluginsInstall,
}

func runPluginsInstall(cmd *cobra.Command, args []string) error {
	reporter := workspace.NewReporter(cmd.ErrOrStderr())
	action, err := plugin.Install(nil, reporter, plugin.InstallOpts{SkipInstall: false})
	if err != nil {
		return fmt.Errorf("installing niwa plugin: %w", err)
	}
	switch action {
	case plugin.Installed:
		fmt.Fprintln(cmd.OutOrStdout(), "niwa plugin installed.")
	case plugin.UpToDate:
		fmt.Fprintln(cmd.OutOrStdout(), "niwa plugin already up to date.")
	case plugin.Failed:
		return fmt.Errorf("niwa plugin install failed (check ~/.claude/ permissions)")
	}
	return nil
}
