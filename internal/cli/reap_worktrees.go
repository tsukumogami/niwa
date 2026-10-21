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
// A session counts as ended when the transcript recorded at create time is
// gone, or hasn't been written for the reap idle grace (NIWA_REAP_IDLE_GRACE,
// 24h by default). A record with no transcript can't be judged and is left
// alone, as is every record without the hook's marker or a base commit.
//
// The destroy is DestroySession without force, so its attach-lock and dirty
// guards still apply on top of the unchanged check. It returns how many
// worktrees it removed; failures are reported on stderr and skipped.
func sweepHookWorktrees(workspaceRoot string, now time.Time, stderr io.Writer) int {
	instances, err := workspace.EnumerateInstances(workspaceRoot)
	if err != nil {
		return 0
	}
	if workspace.IsSingleInstanceLayout(workspaceRoot) {
		instances = append(instances, workspaceRoot)
	}
	return sweepHookWorktreesIn(instances, now, stderr)
}

// sweepHookWorktreesIn is sweepHookWorktrees over a given list of instances.
func sweepHookWorktreesIn(instances []string, now time.Time, stderr io.Writer) int {
	grace := reapIdleGrace()
	ctx := context.Background()
	removed := 0
	for _, instance := range instances {
		states, err := worktree.ListSessionLifecycleStates(filepathJoinSessions(instance))
		if err != nil {
			continue
		}
		for _, st := range states {
			if st.Status != worktree.SessionStatusActive || st.CreatedBy != worktree.CreatedByWorktreeHook {
				continue
			}
			if !claudeSessionEnded(st.ClaudeTranscriptPath, now, grace) {
				continue
			}
			unchanged, _, err := worktree.WorktreeUnchanged(ctx, st, worktree.StdGitInvoker{})
			if err != nil || !unchanged {
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

// claudeSessionEnded reports whether the session that wrote transcript has
// ended: the file is gone, or idle for at least grace. An empty path or an
// unreadable file is "can't tell", which is never ended.
func claudeSessionEnded(transcript string, now time.Time, grace time.Duration) bool {
	if transcript == "" {
		return false
	}
	info, err := os.Stat(transcript)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	return now.Sub(info.ModTime()) >= grace
}
