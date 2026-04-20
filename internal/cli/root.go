package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/buildinfo"
)

var (
	// noProgress disables the TTY status-line animations when set. When true
	// the Reporter is constructed in non-TTY mode regardless of the actual
	// terminal state. Set by --no-progress on the root command.
	noProgress bool

	// noColor is true when the NO_COLOR environment variable is non-empty.
	// It is populated in PersistentPreRunE and available to all subcommands.
	// NO_COLOR does not affect progress/status-line behavior.
	noColor bool
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
		if err := captureNiwaResponseFile(); err != nil {
			return err
		}
		noColor = os.Getenv("NO_COLOR") != ""
		return nil
	},
}

func init() {
	rootCmd.Version = buildinfo.Version()
	rootCmd.PersistentFlags().BoolVar(&noProgress, "no-progress", false,
		"disable TTY status-line animations; use append-only output regardless of terminal state")
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
