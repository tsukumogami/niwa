package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/tsukumogami/niwa/internal/workspace"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// sweepHookWorktrees is the backstop for the SubagentStop hook: it removes
// worktrees niwa's WorktreeCreate hook made that are still active, unchanged
// (worktree.WorktreeUnchanged), and whose Claude Code session has ended. That
// covers a subagent that ended without a SubagentStop, such as an interrupted
// run, and a session that entered a worktree and never left it.
//
// How a session counts as ended depends on what the hook recorded; see
// hookSessionEnded. A worktree made for a session that had a Claude Code job
// entry is kept for as long as that entry is listed under jobsDir, however
// idle its transcript: a held background worker is still resumable. Any other
// goes by its transcript (claudeSessionEnded). Either way the record must be
// older than the reap idle grace (NIWA_REAP_IDLE_GRACE, 24h by default), and
// a record without the hook's marker or a base commit is left alone.
//
// Before removing one it asks the workspace's pre-removal command, with step
// worktree-sweep and the recorded Claude session id; a refusal keeps the
// worktree for this pass and prints the gate's line.
//
// The destroy is DestroySession without force, so its attach-lock and dirty
// guards still apply on top of the unchanged check. Unlike the hook's
// teardown it doesn't release an attach lock first: it acts for no agent, so
// a held lock is someone else's and keeps the worktree. The check and the removal
// aren't atomic, but a session idle for the whole grace period is not writing
// into the worktree in between. It returns how many worktrees it removed;
// failures are reported on stderr and skipped.
func sweepHookWorktrees(workspaceRoot, jobsDir string, now time.Time, stderr io.Writer) int {
	instances, err := workspace.EnumerateInstances(workspaceRoot)
	if err != nil {
		return 0
	}
	if workspace.IsSingleInstanceLayout(workspaceRoot) {
		instances = append(instances, workspaceRoot)
	}
	return sweepHookWorktreesIn(workspaceRoot, instances, jobsDir, now, stderr)
}

// sweepHookWorktreesIn is sweepHookWorktrees over a given list of instances.
func sweepHookWorktreesIn(workspaceRoot string, instances []string, jobsDir string, now time.Time, stderr io.Writer) int {
	grace := reapIdleGrace()
	ctx := context.Background()
	// One gate per sweep, so the whole sweep shares one time budget, as each
	// reap pass does.
	gate := newRemovalGate(workspaceRoot, removeStepWorktreeSweep, stderr)
	removed := 0
	for _, instance := range instances {
		// An opted-out instance's worktrees aren't niwa's to tear down, as
		// on WorktreeRemove and SubagentStop.
		if state, err := workspace.LoadState(instance); err == nil && state.NoWorktreeDelegation {
			continue
		}
		states, err := worktree.ListSessionLifecycleStates(filepathJoinSessions(instance))
		if err != nil {
			continue
		}
		for _, st := range states {
			if st.Status != worktree.SessionStatusActive || st.CreatedBy != worktree.CreatedByWorktreeHook {
				continue
			}
			if !hookSessionEnded(st, jobsDir, now, grace) {
				continue
			}
			unchanged, _, err := worktree.WorktreeUnchanged(ctx, st, worktree.StdGitInvoker{})
			if err != nil || !unchanged {
				continue
			}
			var ids []string
			if st.ClaudeSessionID != "" {
				ids = []string{st.ClaudeSessionID}
			}
			if !gate.ask(st.WorktreePath, ids) {
				continue
			}
			if _, err := worktree.DestroySession(ctx, instance, st.SessionID, false /* force */, worktree.StdGitInvoker{}); err != nil {
				fmt.Fprintf(stderr, "niwa: warning: removing unchanged worktree %s: %v\n", st.WorktreePath, err)
				continue
			}
			fmt.Fprintf(stderr, "niwa: removed unchanged worktree %s: the Claude Code session that asked for it has ended\n", st.WorktreePath)
			removed++
		}
	}
	return removed
}

// hookSessionEnded reports whether the Claude Code session that asked for the
// worktree has ended. A record whose hook run found a job entry for a
// well-formed session id is job-keyed: it has ended once the record is at
// least grace old and the jobs directory can be read and lists no entry for
// that session. Its transcript isn't consulted, since a held worker's
// transcript goes quiet while the worker is still resumable. Every other
// record (no job entry at create, a malformed id, or a record from before the
// hook recorded either) goes by claudeSessionEnded.
func hookSessionEnded(st worktree.SessionLifecycleState, jobsDir string, now time.Time, grace time.Duration) bool {
	if st.ClaudeSessionID == "" || st.ClaudeJobListed == nil || !*st.ClaudeJobListed {
		return claudeSessionEnded(st, now, grace)
	}
	created, err := time.Parse(time.RFC3339, st.CreationTime)
	if err != nil || now.Sub(created) < grace {
		return false
	}
	return jobEntryGone(jobsDir, st.ClaudeSessionID)
}

// claudeSessionEnded reports whether the Claude Code session that asked for
// the worktree has ended. The record must be at least grace old, and then its
// transcript must be gone or idle for at least grace. The age floor matters:
// Claude Code may not have written the transcript yet when the hook runs, and
// a missing file alone must never read as an ended session. An empty path, an
// unreadable file or an unparseable creation time is "can't tell", which is
// never ended.
func claudeSessionEnded(st worktree.SessionLifecycleState, now time.Time, grace time.Duration) bool {
	if st.ClaudeTranscriptPath == "" {
		return false
	}
	created, err := time.Parse(time.RFC3339, st.CreationTime)
	if err != nil || now.Sub(created) < grace {
		return false
	}
	info, err := os.Stat(st.ClaudeTranscriptPath)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	return now.Sub(info.ModTime()) >= grace
}
