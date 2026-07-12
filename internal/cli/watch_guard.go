package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/watch"
)

// guardFsCmd is the PreToolUse filesystem guard for contained review sessions.
// It is not a user-facing verb: the review-session settings wire it as the
// Write/Edit/NotebookEdit PreToolUse hook (`niwa watch guard-fs`). It reads the
// hook payload from stdin and exits 0 to allow an in-instance write or 2 to block
// a write that resolves outside the instance. It is hidden from help.
var guardFsCmd = &cobra.Command{
	Use:           "guard-fs",
	Short:         "internal: PreToolUse filesystem guard for contained review sessions",
	Hidden:        true,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	Run: func(_ *cobra.Command, _ []string) {
		os.Exit(watch.GuardFSDecision(os.Stdin, os.Stderr))
	},
}

func init() {
	watchCmd.AddCommand(guardFsCmd)
}
