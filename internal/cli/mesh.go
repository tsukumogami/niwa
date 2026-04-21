package cli

import "github.com/spf13/cobra"

func init() {
	rootCmd.AddCommand(meshCmd)
}

var meshCmd = &cobra.Command{
	Use:   "mesh",
	Short: "Manage the workspace session mesh",
	Long: `Manage the workspace session mesh.

Subcommands:
  watch   Run the mesh watch daemon that auto-resumes dead Claude sessions`,
}
