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
	configSetCmd.AddCommand(configSetAuthorityCeilingCmd)
	configUnsetCmd.AddCommand(configUnsetAuthorityLevelCmd)
	configUnsetCmd.AddCommand(configUnsetAuthorityCeilingCmd)
}

var configSetAuthorityLevelCmd = &cobra.Command{
	Use:     "authority-level <level>",
	Aliases: []string{"authority_level"},
	Short:   "Cap the authority level of every dispatch on this machine",
	Long: `Cap the authority level of every session "niwa dispatch" launches on this
machine.

The value is written to [global].authority_level in your personal niwa config
(~/.config/niwa/config.toml, or $XDG_CONFIG_HOME/niwa/config.toml).

Accepted values, widest first: ` + strings.Join(authority.LevelNames(), ", ") + `.

The machine level only narrows: a dispatch's level ceiling is the narrowest of
the workspace level, the overlay level and this one, so it can never make a
resolved level wider. Check the result with "niwa authority policy".`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigSetAuthorityLevel,
}

func runConfigSetAuthorityLevel(cmd *cobra.Command, args []string) error {
	accepted := strings.Join(authority.LevelNames(), ", ")
	raw := strings.TrimSpace(args[0])
	// A blank argument is not a request to clear the setting: ParseLevel
	// reads it as not set, which is right for config and wrong for a value
	// someone typed.
	if raw == "" {
		return fmt.Errorf("niwa config set authority-level needs a level; accepted values are %s. To clear the setting, run: niwa config unset authority-level", accepted)
	}
	// Validated before the file is read, so a bad value leaves it untouched.
	level, err := authority.ParseLevel(raw)
	if err != nil {
		return fmt.Errorf("invalid authority-level %q; accepted values are %s", raw, accepted)
	}

	globalCfg, cfgPath, err := loadGlobalConfigForWrite()
	if err != nil {
		return err
	}
	globalCfg.Global.AuthorityLevel = level.String()
	if err := config.SaveGlobalConfigTo(cfgPath, globalCfg); err != nil {
		return fmt.Errorf("saving global config: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Authority level set to %s in %s\n", level, cfgPath)
	return nil
}

var configSetAuthorityCeilingCmd = &cobra.Command{
	Use:     "authority-ceiling <authority>[,<authority>...] | none",
	Aliases: []string{"authority_ceiling"},
	Short:   "Limit which authorities a dispatch on this machine may be granted",
	Long: `Limit which named authorities a session "niwa dispatch" launches on this machine
may be granted.

The list is written to [global].authority_ceiling in your personal niwa config.
Entries are comma-separated (or given as separate arguments), trimmed, and
kept once each. "none" writes an empty list, which lets nothing be granted.

Accepted authorities: ` + strings.Join(authority.AuthorityNames(), ", ") + `.

The machine ceiling only narrows: the effective ceiling is the authorities in
both it and the workspace's [authority] ceiling, so it can never add one.
Check the result with "niwa authority policy".`,
	Args: cobra.MinimumNArgs(1),
	RunE: runConfigSetAuthorityCeiling,
}

func runConfigSetAuthorityCeiling(cmd *cobra.Command, args []string) error {
	ceiling, err := parseCeilingArgs(args)
	if err != nil {
		return err
	}
	globalCfg, cfgPath, err := loadGlobalConfigForWrite()
	if err != nil {
		return err
	}
	names := make([]string, len(ceiling))
	for i, a := range ceiling {
		names[i] = string(a)
	}
	globalCfg.Global.AuthorityCeiling = names
	if err := config.SaveGlobalConfigTo(cfgPath, globalCfg); err != nil {
		return fmt.Errorf("saving global config: %w", err)
	}
	shown := "none (nothing grantable)"
	if len(names) > 0 {
		shown = strings.Join(names, ", ")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Authority ceiling set to %s in %s\n", shown, cfgPath)
	return nil
}

// parseCeilingArgs validates `config set authority-ceiling` arguments through
// the same comma-list parser --grant and --role use. "none" alone is the
// empty list; it has that meaning here only. Every entry must be an
// authority this niwa knows: unlike a ceiling read from a shared config, this
// is a person typing on this niwa, so an unknown name is refused rather than
// dropped.
func parseCeilingArgs(args []string) ([]authority.Authority, error) {
	accepted := strings.Join(authority.AuthorityNames(), ", ")
	if len(args) == 1 && strings.TrimSpace(args[0]) == "none" {
		return []authority.Authority{}, nil
	}
	entries, err := authority.ParseNameList(args)
	if err != nil {
		return nil, fmt.Errorf("invalid authority-ceiling: %w; accepted values are %s, or none for an empty list", err, accepted)
	}
	seen := map[authority.Authority]bool{}
	for _, e := range entries {
		a, err := authority.ParseAuthority(e)
		if err != nil {
			return nil, fmt.Errorf("invalid authority-ceiling entry %q; accepted values are %s, or none for an empty list", e, accepted)
		}
		seen[a] = true
	}
	// De-duplicated and written in R3 order, the order every authority list
	// uses.
	ordered := []authority.Authority{}
	for _, a := range authority.Authorities() {
		if seen[a] {
			ordered = append(ordered, a)
		}
	}
	return ordered, nil
}

func loadGlobalConfigForWrite() (*config.GlobalConfig, string, error) {
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return nil, "", fmt.Errorf("loading global config: %w", err)
	}
	cfgPath, err := config.GlobalConfigPath()
	if err != nil {
		return nil, "", fmt.Errorf("determining global config path: %w", err)
	}
	return globalCfg, cfgPath, nil
}

var configUnsetAuthorityLevelCmd = &cobra.Command{
	Use:     "authority-level",
	Aliases: []string{"authority_level"},
	Short:   "Remove the machine-wide authority level cap",
	Long:    `Remove [global].authority_level from your personal niwa config, so the machine no longer caps the authority level.`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return unsetGlobalAuthorityKey(cmd, "authority level", func(g *config.GlobalSettings) *any { return &g.AuthorityLevel })
	},
}

var configUnsetAuthorityCeilingCmd = &cobra.Command{
	Use:     "authority-ceiling",
	Aliases: []string{"authority_ceiling"},
	Short:   "Remove the machine-wide authority ceiling",
	Long: `Remove [global].authority_ceiling from your personal niwa config, so the machine
no longer narrows which authorities a dispatch may be granted. This is not the
same as "niwa config set authority-ceiling none", which lets nothing be granted.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return unsetGlobalAuthorityKey(cmd, "authority ceiling", func(g *config.GlobalSettings) *any { return &g.AuthorityCeiling })
	},
}

// unsetGlobalAuthorityKey clears one [global] authority key. The raw value is
// checked, not the accessor: unset is the way out of an invalid value, so it
// must not refuse one.
func unsetGlobalAuthorityKey(cmd *cobra.Command, what string, key func(*config.GlobalSettings) *any) error {
	globalCfg, cfgPath, err := loadGlobalConfigForWrite()
	if err != nil {
		return err
	}
	slot := key(&globalCfg.Global)
	if *slot == nil {
		fmt.Fprintf(cmd.OutOrStdout(), "No %s set.\n", what)
		return nil
	}
	*slot = nil
	if err := config.SaveGlobalConfigTo(cfgPath, globalCfg); err != nil {
		return fmt.Errorf("saving global config: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s removed.\n", strings.ToUpper(what[:1])+what[1:])
	return nil
}
