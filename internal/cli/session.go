package cli

import "github.com/spf13/cobra"

func init() {
	rootCmd.AddCommand(sessionCmd)
}

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage sessions in the workspace mesh",
	Long: `Manage sessions in the workspace mesh.

Subcommands:
  register    Register this session with the workspace mesh
  unregister  Remove this session from the workspace mesh
  list        List all registered sessions`,
}
