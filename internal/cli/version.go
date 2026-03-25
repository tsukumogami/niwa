package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/buildinfo"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("niwa %s\n", buildinfo.Version())
		fmt.Printf("  commit: %s\n", buildinfo.Commit())
		fmt.Printf("  built:  %s\n", buildinfo.Date())
	},
}
