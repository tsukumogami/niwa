package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/config"
)

func init() {
	configSetCmd.AddCommand(configSetAuthorityLevelCmd)
	configUnsetCmd.AddCommand(configUnsetAuthorityLevelCmd)
}

// authorityLevelSubcommandAliases lets the TOML spelling work as a subcommand
// name too, the courtesy dispatch-permission-mode extends: a developer who
// read `authority_level` in a config file and typed it back gets the command
// they meant instead of "unknown command".
var authorityLevelSubcommandAliases = []string{"authority_level"}

var configSetAuthorityLevelCmd = &cobra.Command{
	Use:     "authority-level <level>",
	Aliases: authorityLevelSubcommandAliases,
	Short:   "Set this machine's authority-level ceiling for dispatched sessions",
	Long: `Set the widest authority level a session launched by "niwa dispatch" may run
at on this machine.

The value is written to [global].authority_level in your personal niwa config
(~/.config/niwa/config.toml, or $XDG_CONFIG_HOME/niwa/config.toml).

Accepted values, from widest to narrowest: ` + strings.Join(authority.Names(), ", ") + `.

A dispatch runs at the narrowest of:

  [workspace].authority_level        the workspace (autonomous when unset)
  [global].authority_level           this machine
  niwa dispatch --authority <level>  one command

The machine setting only ever narrows. It can make this machine's dispatches
stricter than the workspace asks, but never looser, so a team's rule about who
decides holds on every machine. That is the opposite of
dispatch-permission-mode, where the machine outranks the workspace both ways.`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigSetAuthorityLevel,
}

func runConfigSetAuthorityLevel(cmd *cobra.Command, args []string) error {
	// An empty argument is not a request to clear the setting. authority.Parse
	// reads "" as "not set", which is right for a config file and wrong for a
	// value a developer typed, and cobra.ExactArgs(1) counts "" as an
	// argument. Without this check a scripted `niwa config set authority-level
	// "$LEVEL"` with LEVEL unset would report success and change nothing the
	// script can see.
	raw := strings.TrimSpace(args[0])
	if raw == "" {
		return fmt.Errorf("niwa config set authority-level needs a level; accepted values are %s. To clear the setting, run: niwa config unset authority-level", strings.Join(authority.Names(), ", "))
	}

	// Validate before touching the file, through the same boundary dispatch
	// uses, so a typo fails here rather than stopping the next dispatch with
	// the bad value already written. The error is the config package's, so it
	// names the key the value would have gone into.
	if _, err := authority.Parse(raw); err != nil {
		return &config.InvalidAuthorityLevelError{Table: "global", Value: raw}
	}

	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("loading global config: %w", err)
	}
	globalCfg.Global.AuthorityLevel = raw

	cfgPath, err := config.GlobalConfigPath()
	if err != nil {
		return fmt.Errorf("determining global config path: %w", err)
	}
	if err := config.SaveGlobalConfigTo(cfgPath, globalCfg); err != nil {
		return fmt.Errorf("saving global config: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Authority level set to %s in %s\n", raw, cfgPath)
	return nil
}

var configUnsetAuthorityLevelCmd = &cobra.Command{
	Use:     "authority-level",
	Aliases: authorityLevelSubcommandAliases,
	Short:   "Remove this machine's authority-level ceiling for dispatched sessions",
	Long: `Remove [global].authority_level from your personal niwa config.

Afterwards a dispatch with no --authority runs at the workspace's level, or at
autonomous when the workspace sets none.`,
	Args: cobra.NoArgs,
	RunE: runConfigUnsetAuthorityLevel,
}

func runConfigUnsetAuthorityLevel(cmd *cobra.Command, args []string) error {
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("loading global config: %w", err)
	}

	// Checked raw rather than through the accessor: unset is the way out of an
	// invalid value, so it must not refuse one.
	if strings.TrimSpace(globalCfg.Global.AuthorityLevel) == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "No authority level set.")
		return nil
	}

	globalCfg.Global.AuthorityLevel = ""

	cfgPath, err := config.GlobalConfigPath()
	if err != nil {
		return fmt.Errorf("determining global config path: %w", err)
	}
	if err := config.SaveGlobalConfigTo(cfgPath, globalCfg); err != nil {
		return fmt.Errorf("saving global config: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Authority level removed.")
	return nil
}
