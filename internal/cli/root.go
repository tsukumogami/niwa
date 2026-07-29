package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/buildinfo"
	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/onboard"
	"github.com/tsukumogami/niwa/internal/workspace"
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
	// Issue 10 / cobra UX cleanup: SilenceErrors and SilenceUsage suppress
	// cobra's auto-printing of errors and the usage banner on every
	// RunE failure. These settings inherit to children (cobra walks the
	// parent chain), so individual commands no longer need to set them.
	// Execute() below prints the error exactly once on stderr — the
	// single source of truth for error output. Commands that *do* want
	// the usage banner on user-input errors can set SilenceUsage:false
	// on themselves.
	SilenceErrors: true,
	SilenceUsage:  true,
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
//
// Most errors propagate through cobra's default handling: print to stderr
// and exit 1. Commands that need a specific exit code (currently
// `niwa session attach` and `niwa session detach`) return a
// *sessionattach.ExitCodeError; we type-assert here, print the message if
// present, and use the Code field for os.Exit. This lets scripts wrap the
// commands and read exit codes per the PRD's Exit Code Mapping table.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		var ece *sessionattach.ExitCodeError
		if errors.As(err, &ece) {
			if ece.Msg != "" {
				fmt.Fprintln(os.Stderr, ece.Msg)
			}
			os.Exit(ece.Code)
		}
		// PRD R23: *workspace.InitConflictError carries an ExitCode
		// field populated by the bootstrap classifier and the R25/R9/R13
		// dispatch paths. Print the error's rendered text (the display
		// wrapper produces the legacy "Detail\n  Suggestion" shape) and
		// exit with the typed code. ExitCode == 0 means the field was
		// not populated by the caller; fall back to the default exit 1
		// so older code paths constructing InitConflictError without an
		// explicit code keep their historical behavior.
		var ice *workspace.InitConflictError
		if errors.As(err, &ice) && ice.ExitCode > 0 {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(ice.ExitCode)
		}
		// *onboard.ExitCodeError carries niwa onboard's own exit-code
		// vocabulary (Decision 2): a third errors.As arm, same shape as
		// the sessionattach arm above.
		var oce *onboard.ExitCodeError
		if errors.As(err, &oce) {
			if oce.Msg != "" {
				fmt.Fprintln(os.Stderr, oce.Msg)
			}
			os.Exit(oce.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
