package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
)

func init() {
	configSetCmd.AddCommand(configSetPermissionModeCmd)
	configUnsetCmd.AddCommand(configUnsetPermissionModeCmd)
}

// permissionModeSubcommandAliases lets the TOML spelling work as a subcommand
// name too, the same courtesy default-dispatch-harness extends: a developer who
// read `dispatch_permission_mode` in a config file and typed it back gets the
// command they meant instead of "unknown command".
var permissionModeSubcommandAliases = []string{"dispatch_permission_mode"}

var configSetPermissionModeCmd = &cobra.Command{
	Use:     "dispatch-permission-mode <mode>",
	Aliases: permissionModeSubcommandAliases,
	Short:   "Set the machine-wide permission mode for dispatched Claude workers",
	Long: `Set the permission mode Claude workers launched by "niwa dispatch" start in
on this machine.

The value is written to [global].dispatch_permission_mode in your personal niwa
config (~/.config/niwa/config.toml, or $XDG_CONFIG_HOME/niwa/config.toml).

Accepted values are Claude Code's permission modes: ` + strings.Join(config.DispatchPermissionModes, ", ") + `.

The forwarded --permission-mode is the first of:

  niwa dispatch --permission-mode <mode>   one command
  [global].dispatch_permission_mode        this machine
  bypassPermissions                        a workspace whose [claude.settings]
                                           permissions posture is "bypass"
  nothing                                  Claude Code starts in its own default

The machine setting outranks the workspace posture in both directions: a
stricter value (auto, default) replaces a bypass posture, and a looser one
(bypassPermissions) applies where the workspace declares ask or nothing. That is
the opposite order to default-dispatch-harness, where the workspace wins. How
much a worker may do without asking is your call about your own machine.

The setting applies only to agents whose permission flag is Claude's
--permission-mode. A Codex dispatch forwards nothing from it, though a value
outside the accepted set still stops every dispatch until it is fixed.`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigSetPermissionMode,
}

func runConfigSetPermissionMode(cmd *cobra.Command, args []string) error {
	// An empty argument is not a request to clear the setting.
	// ParseDispatchPermissionMode reads "" as "no machine setting", which is
	// right for dispatch and wrong for a value a developer typed, and
	// cobra.ExactArgs(1) counts "" as an argument. Without this check a scripted
	// `niwa config set dispatch-permission-mode "$MODE"` with MODE unset would
	// report success and change nothing the script can see.
	raw := strings.TrimSpace(args[0])
	if raw == "" {
		return fmt.Errorf("niwa config set dispatch-permission-mode needs a mode; accepted values are %s. To clear the setting, run: niwa config unset dispatch-permission-mode", strings.Join(config.DispatchPermissionModes, ", "))
	}

	// Validate before touching the file, through the same boundary dispatch
	// uses, so a typo fails here rather than stopping the next dispatch with
	// the bad value already written.
	mode, err := config.ParseDispatchPermissionMode(raw)
	if err != nil {
		return err
	}

	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("loading global config: %w", err)
	}
	globalCfg.Global.DispatchPermissionMode = mode

	cfgPath, err := config.GlobalConfigPath()
	if err != nil {
		return fmt.Errorf("determining global config path: %w", err)
	}
	if err := config.SaveGlobalConfigTo(cfgPath, globalCfg); err != nil {
		return fmt.Errorf("saving global config: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Dispatch permission mode set to %s in %s\n", mode, cfgPath)
	return nil
}

var configUnsetPermissionModeCmd = &cobra.Command{
	Use:     "dispatch-permission-mode",
	Aliases: permissionModeSubcommandAliases,
	Short:   "Remove the machine-wide permission mode for dispatched Claude workers",
	Long: `Remove [global].dispatch_permission_mode from your personal niwa config.

Afterwards a dispatch with no --permission-mode falls back to the workspace's
declared permissions posture (bypassPermissions under "bypass"), and otherwise
forwards no permission mode at all.`,
	Args: cobra.NoArgs,
	RunE: runConfigUnsetPermissionMode,
}

func runConfigUnsetPermissionMode(cmd *cobra.Command, args []string) error {
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("loading global config: %w", err)
	}

	// Checked raw rather than through the accessor: unset is the way out of an
	// invalid value, so it must not refuse one.
	if strings.TrimSpace(globalCfg.Global.DispatchPermissionMode) == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "No dispatch permission mode set.")
		return nil
	}

	globalCfg.Global.DispatchPermissionMode = ""

	cfgPath, err := config.GlobalConfigPath()
	if err != nil {
		return fmt.Errorf("determining global config path: %w", err)
	}
	if err := config.SaveGlobalConfigTo(cfgPath, globalCfg); err != nil {
		return fmt.Errorf("saving global config: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Dispatch permission mode removed.")
	return nil
}
