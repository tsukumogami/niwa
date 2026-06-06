package cli

import (
	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
)

func init() {
	sessionCmd.AddCommand(sessionAttachCmd)
	sessionCmd.AddCommand(sessionDetachCmd)
}

var (
	sessionDetachForce bool
)

func init() {
	sessionDetachCmd.Flags().BoolVar(&sessionDetachForce, "force", false,
		"release the attach lock even if held by a live process")
}

var sessionAttachCmd = &cobra.Command{
	Use:   "attach <session-id>",
	Short: "Attach to a mesh session interactively",
	Long: `Attach to a mesh session interactively.

Validates the session worktree, acquires the in-use lock (attach.state +
flock) so no other process attaches concurrently, validates the worker's
claude transcript, and launches Claude Code with --resume so you can step
into the conversation, prompt the agent, or fix things manually. When you
exit Claude Code (Ctrl-D or /exit), niwa releases the lock.

Discovery: 'niwa session list' shows the AVAILABILITY column for each
session. Use 'niwa session detach <id> --force' to break a stale lock.

Exit codes: 0 (clean exit), 1 (validation), 2 (usage), 3 (lock held),
or the propagated claude exit code (capped at 125).`,
	// We don't use cobra.ExactArgs because its default error exits 1 with a
	// generic message. RunE validates arg count itself and returns an
	// *sessionattach.ExitCodeError with Code=2 plus the PRD-mandated usage
	// string when no session_id is supplied.
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeSessionIDs,
	RunE:              runSessionAttach,
	SilenceErrors:     true,
	SilenceUsage:      true,
}

var sessionDetachCmd = &cobra.Command{
	Use:   "detach <session-id>",
	Short: "Release a stale attach lock (operator escape hatch)",
	Long: `Release a stale attach lock.

Normal attach release happens automatically when Claude Code exits; this
command exists to break stale locks left behind by an SSH disconnect or
terminal crash.

Without --force: succeeds silently if the holder is dead (auto-recovery);
fails with exit 3 if the holder is alive.

With --force: SIGTERMs the holder, waits NIWA_DESTROY_GRACE_SECONDS
(default 5s), SIGKILLs if needed, then releases the lock. Exits with
code 4 to signal that a live holder was killed.`,
	// Same reasoning as sessionAttachCmd: RunE handles missing-arg with the
	// PRD R10 usage string and exit code 2.
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeSessionIDs,
	RunE:              runSessionDetach,
	SilenceErrors:     true,
	SilenceUsage:      true,
}

func runSessionAttach(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return &sessionattach.ExitCodeError{
			Code: 2,
			Msg: "niwa: usage: niwa session attach <session_id>. " +
				"Run `niwa session list --status active` to discover available sessions.",
		}
	}
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return err
	}
	return sessionattach.AttachRun(cmd.Context(), sessionattach.Options{
		InstanceRoot: instanceRoot,
		SessionID:    args[0],
		Stdin:        cmd.InOrStdin(),
		Stdout:       cmd.OutOrStdout(),
		Stderr:       cmd.ErrOrStderr(),
	})
}

func runSessionDetach(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return &sessionattach.ExitCodeError{
			Code: 2,
			Msg: "niwa: usage: niwa session detach <session_id> [--force]. " +
				"Normal attach release happens automatically when claude code exits; " +
				"this command exists to break stale locks.",
		}
	}
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return err
	}
	return sessionattach.DetachRun(cmd.Context(), sessionattach.DetachOptions{
		InstanceRoot: instanceRoot,
		SessionID:    args[0],
		Force:        sessionDetachForce,
		Stdout:       cmd.OutOrStdout(),
		Stderr:       cmd.ErrOrStderr(),
	})
}
