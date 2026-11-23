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
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "niwa %s\n", buildinfo.Version())
		fmt.Fprintf(out, "  commit: %s\n", buildinfo.Commit())
		fmt.Fprintf(out, "  built:  %s\n", buildinfo.Date())
		// Only the niwa_functional build prints this line, so a binary
		// built for the functional suite is never mistaken for a release.
		if buildinfo.IsTestBuild() {
			fmt.Fprintln(out, "  build:  test build")
		}
	},
}
