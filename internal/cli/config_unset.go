package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
)

func init() {
	configCmd.AddCommand(configUnsetCmd)
	configUnsetCmd.AddCommand(configUnsetGlobalCmd)
}

var configUnsetCmd = &cobra.Command{
	Use:   "unset",
	Short: "Remove a configuration value",
}

var configUnsetGlobalCmd = &cobra.Command{
	Use:   "global",
	Short: "Unregister the global config repo",
	Long: `Unregister the global config repo and remove its local clone.

After running this command, niwa apply will no longer apply global
config overlays to workspace instances.`,
	Args: cobra.NoArgs,
	RunE: runConfigUnsetGlobal,
}

func runConfigUnsetGlobal(cmd *cobra.Command, args []string) error {
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("loading global config: %w", err)
	}

	if globalCfg.GlobalConfig.Repo == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "No global config registered.")
		return nil
	}

	globalConfigDir, err := config.GlobalConfigDir()
	if err != nil {
		return fmt.Errorf("determining global config directory: %w", err)
	}

	if err := os.RemoveAll(globalConfigDir); err != nil {
		return fmt.Errorf("removing global config clone: %w", err)
	}

	globalCfg.GlobalConfig = config.GlobalConfigSource{}

	cfgPath, err := config.GlobalConfigPath()
	if err != nil {
		return fmt.Errorf("determining global config path: %w", err)
	}
	if err := config.SaveGlobalConfigTo(cfgPath, globalCfg); err != nil {
		return fmt.Errorf("saving global config: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Global config unregistered.")
	return nil
}
