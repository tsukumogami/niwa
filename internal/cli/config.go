package cli

import "github.com/spf13/cobra"

func init() {
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage niwa configuration",
	Long: `Manage niwa configuration settings.

Subcommands:
  set     Set configuration values
  unset   Remove configuration values`,
}
