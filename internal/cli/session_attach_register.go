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
	sessionAttachForce bool
	sessionDetachForce bool
)

func init() {
	sessionAttachCmd.Flags().BoolVar(&sessionAttachForce, "force", false,
		"SIGTERM the running worker before acquiring the attach lock")
	sessionDetachCmd.Flags().BoolVar(&sessionDetachForce, "force", false,
		"release the attach lock even if held by a live process")
}

var sessionAttachCmd = &cobra.Command{
	Use:   "attach <session-id>",
	Short: "Attach to a mesh session interactively",
	Long: `Attach to a mesh session interactively.

Locks the session against further mesh use, terminates the per-worktree
daemon, validates the worker's claude transcript, and launches Claude Code
with --resume so you can step into the conversation, prompt the agent,
or fix things manually. When you exit Claude Code (Ctrl-D or /exit), niwa
releases the lock and the mesh resumes normally.

Pass --force to SIGTERM a running worker before acquiring the lock.
Without --force, attach waits for any running worker to finish naturally.

Discovery: 'niwa session list' shows the AVAILABILITY column for each
session. Use 'niwa session detach <id> --force' to break a stale lock.

Exit codes: 0 (clean exit), 1 (validation), 2 (usage), 3 (lock held),
or the propagated claude exit code (capped at 125).`,
	Args:          cobra.ExactArgs(1),
	RunE:          runSessionAttach,
	SilenceErrors: true, // we print niwa-shaped error messages ourselves
	SilenceUsage:  true,
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
	Args:          cobra.ExactArgs(1),
	RunE:          runSessionDetach,
	SilenceErrors: true,
	SilenceUsage:  true,
}

func runSessionAttach(cmd *cobra.Command, args []string) error {
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return err
	}
	return sessionattach.AttachRun(cmd.Context(), sessionattach.Options{
		InstanceRoot: instanceRoot,
		SessionID:    args[0],
		Force:        sessionAttachForce,
		Stdin:        cmd.InOrStdin(),
		Stdout:       cmd.OutOrStdout(),
		Stderr:       cmd.ErrOrStderr(),
	})
}

func runSessionDetach(cmd *cobra.Command, args []string) error {
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
