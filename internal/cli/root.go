package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/buildinfo"
)

var rootCmd = &cobra.Command{
	Use:   "niwa",
	Short: "Declarative workspace manager for AI-assisted development",
	Long: `niwa manages multi-repo workspaces with layered Claude Code configuration.

It clones repositories into a structured workspace directory, generates
CLAUDE.md files at each level of the hierarchy, and keeps everything
in sync when configuration changes.`,
}

func init() {
	rootCmd.Version = buildinfo.Version()
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
