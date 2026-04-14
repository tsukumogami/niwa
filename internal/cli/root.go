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
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// NIWA_RESPONSE_FILE is the shell-wrapper/CLI protocol channel for
		// landing-path delivery. Capture its value into a package-level cache
		// and unset the environment variable so subprocesses (git, gh, hook
		// scripts, etc.) don't inherit it -- a buggy or malicious child that
		// writes to the response file would redirect the shell wrapper's cd
		// target. See docs/designs/current/DESIGN-shell-navigation-protocol.md.
		return captureNiwaResponseFile()
	},
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
