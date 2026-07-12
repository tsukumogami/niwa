package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/watch"
)

// guardFsRoot is the instance root the review-session hook bakes in via --root;
// it is the authoritative containment root the guard confines writes to.
var guardFsRoot string

// guardFsCmd is the PreToolUse filesystem guard for contained review sessions.
// It is not a user-facing verb: the review-session settings wire it as the
// Write/Edit/MultiEdit/NotebookEdit PreToolUse hook (`niwa watch guard-fs --root
// <instance>`). It reads the hook payload from stdin and exits 0 to allow an
// in-instance write or 2 to block a write that resolves outside --root. It is
// hidden from help.
var guardFsCmd = &cobra.Command{
	Use:           "guard-fs",
	Short:         "internal: PreToolUse filesystem guard for contained review sessions",
	Hidden:        true,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	Run: func(_ *cobra.Command, _ []string) {
		os.Exit(watch.GuardFSDecision(os.Stdin, os.Stderr, guardFsRoot))
	},
}

func init() {
	guardFsCmd.Flags().StringVar(&guardFsRoot, "root", "",
		"instance root the write must stay within (baked in by the review-session hook)")
	watchCmd.AddCommand(guardFsCmd)
}
