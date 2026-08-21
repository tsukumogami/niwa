package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

func init() {
	configSetCmd.AddCommand(configSetDefaultHarnessCmd)
	configUnsetCmd.AddCommand(configUnsetDefaultHarnessCmd)
}

// defaultHarnessSubcommandAliases lets the TOML spelling work as a subcommand
// name too. A developer who read `default_dispatch_harness` in a config file
// and typed it back gets the command they meant instead of "unknown command".
var defaultHarnessSubcommandAliases = []string{"default_dispatch_harness"}

var configSetDefaultHarnessCmd = &cobra.Command{
	Use:     "default-dispatch-harness <agent>",
	Aliases: defaultHarnessSubcommandAliases,
	Short:   "Set the machine-wide default dispatch harness",
	Long: `Set which coding agent harnesses niwa's dispatched work by default on
this machine.

The value is written to [global].default_dispatch_harness in your personal niwa
config (~/.config/niwa/config.toml, or $XDG_CONFIG_HOME/niwa/config.toml). That
file is yours and local -- unlike a workspace's .niwa/ directory, which is often
a snapshot materialized from a source repo and replaced wholesale on the next
refresh, so an edit there does not survive.

Accepted values are ` + acceptedAgentNames() + `.

This is the broadest rung of the resolution. Anything more specific wins:

  niwa dispatch --harness <agent>                     one command
  NIWA_DISPATCH_HARNESS=<agent> niwa dispatch ...     one shell
  [workspace].default_agent                           one workspace, for everyone in it
  [global].default_dispatch_harness                   this machine, when nothing else says

So a workspace that states its own default_agent keeps launching that agent;
this setting fills in for every workspace that states nothing. It does not
change what "niwa apply" prepares -- every apply prepares the tree for every
agent niwa supports, whatever any of these say.`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigSetDefaultHarness,
}

func runConfigSetDefaultHarness(cmd *cobra.Command, args []string) error {
	// An empty argument is not a request for the default. ParseAgent reads ""
	// as "this source is unset", which is right for the resolver and wrong for
	// a value a developer typed, and cobra.ExactArgs(1) counts "" as an
	// argument. Without this check a scripted `niwa config set
	// default-dispatch-harness "$AGENT"` with AGENT unset writes a machine-wide
	// claude and reports success -- and a setup script is exactly where this
	// command is most useful.
	raw := strings.TrimSpace(args[0])
	if raw == "" {
		return fmt.Errorf("niwa config set default-dispatch-harness needs an agent name; accepted values are %s. To clear the setting, run: niwa config unset default-dispatch-harness", acceptedAgentNames())
	}

	// Validate before touching the file, at the same boundary every other
	// source goes through, so a typo fails here rather than at the next
	// dispatch with the config already written.
	chosen, err := agent.ParseAgent(raw)
	if err != nil {
		return err
	}

	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("loading global config: %w", err)
	}
	globalCfg.Global.DefaultDispatchHarness = string(chosen)

	cfgPath, err := config.GlobalConfigPath()
	if err != nil {
		return fmt.Errorf("determining global config path: %w", err)
	}
	if err := config.SaveGlobalConfigTo(cfgPath, globalCfg); err != nil {
		return fmt.Errorf("saving global config: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Default dispatch harness set to %s in %s\n", chosen, cfgPath)
	fmt.Fprintln(cmd.OutOrStdout(), "A workspace that sets [workspace].default_agent still launches that agent; NIWA_DISPATCH_HARNESS and --harness override both.")
	return nil
}

var configUnsetDefaultHarnessCmd = &cobra.Command{
	Use:     "default-dispatch-harness",
	Aliases: defaultHarnessSubcommandAliases,
	Short:   "Remove the machine-wide default dispatch harness",
	Long: `Remove [global].default_dispatch_harness from your personal niwa config.

Afterwards a launch with no workspace default_agent, no NIWA_DISPATCH_HARNESS,
and no --harness runs ` + builtinDefaultAgentName() + `, which is niwa's built-in
default.`,
	Args: cobra.NoArgs,
	RunE: runConfigUnsetDefaultHarness,
}

func runConfigUnsetDefaultHarness(cmd *cobra.Command, args []string) error {
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("loading global config: %w", err)
	}

	if globalCfg.Global.DefaultDispatchHarness == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "No machine-wide default dispatch harness set.")
		return nil
	}

	globalCfg.Global.DefaultDispatchHarness = ""

	cfgPath, err := config.GlobalConfigPath()
	if err != nil {
		return fmt.Errorf("determining global config path: %w", err)
	}
	if err := config.SaveGlobalConfigTo(cfgPath, globalCfg); err != nil {
		return fmt.Errorf("saving global config: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Machine-wide default dispatch harness removed.")
	return nil
}
