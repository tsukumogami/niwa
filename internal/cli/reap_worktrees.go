package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/tsukumogami/niwa/internal/removal"
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
// Before any other git call in a worktree whose session has ended, the
// sweep walks the instance and runs the program-naming configuration check
// on every repository it finds under the worktree, its own and each
// submodule, gitlink and nested repository (removal.EvaluateSweptWorktree);
// a key that names a program, a read that fails or a walk rule's keep keeps
// the worktree with a line naming it. The evaluation records the worktree's
// HEAD and per-worktree refs. Every git call after it, the unchanged check's
// and DestroySession's, is built by the removal package's gitCommand, and
// every status runs after the configuration check again, with
// --ignore-submodules=all and --untracked-files=all.
//
// The destroy is DestroySession without force, so its attach-lock and dirty
// guards still apply on top of the unchanged check. Unlike the hook's
// teardown it doesn't release an attach lock first: it acts for no agent, so
// a held lock is someone else's and keeps the worktree. DestroySession
// removes the worktree through the evaluation's remover, with no git
// worktree remove or prune: right before the removal it reads HEAD and the
// per-worktree refs again, and any change keeps the worktree with "changed
// during the pass: ref <name>"; it runs the configuration check once more;
// then the no-follow walk removes the work tree and the admin directory, and
// stops, with the path named, on anything it won't remove. A removal that
// stops leaves the session record Active, so the next sweep tries again. It
// returns how many worktrees it removed; keeps and failures are reported on
// stderr and skipped.
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
	// The removal environment every git call of the sweep runs in, made
	// once, for the first worktree whose session has ended.
	var env *removal.Env
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
			if env == nil {
				env = newRemovalEnv(workspaceRoot, userHomeDir(), os.Getenv("PATH"))
				defer env.Close()
			}
			// The walk and the configuration check on every repository
			// under the worktree come before any other git call there. The
			// worktree path is read from the instance's session record and
			// the reasons can quote it, so both pass removal.ScrubEvidence.
			swept, keep := env.EvaluateSweptWorktree(ctx, instance, st.WorktreePath)
			if keep != "" {
				fmt.Fprintf(stderr, "niwa: kept worktree %s: %s\n",
					removal.ScrubEvidence(st.WorktreePath, 400), removal.ScrubEvidence(keep, 400))
				continue
			}
			unchanged, why, err := worktree.WorktreeUnchanged(ctx, st, swept)
			if err != nil || !unchanged {
				// Say why the worktree stays, so a keep is never silent: a
				// check that failed, the reason WorktreeUnchanged gave, or that
				// it gave none. The error and the reason can quote the worktree
				// path, so they pass the scrub like the other keep lines. This
				// only reports; the worktree is kept in every case.
				switch {
				case err != nil:
					fmt.Fprintf(stderr, "niwa: kept worktree %s: check failed: %s\n",
						removal.ScrubEvidence(st.WorktreePath, 400), removal.ScrubEvidence(err.Error(), 400))
				case why != "":
					fmt.Fprintf(stderr, "niwa: kept worktree %s: %s\n",
						removal.ScrubEvidence(st.WorktreePath, 400), removal.ScrubEvidence(why, 400))
				default:
					fmt.Fprintf(stderr, "niwa: kept worktree %s: no reason given\n",
						removal.ScrubEvidence(st.WorktreePath, 400))
				}
				continue
			}
			var ids []string
			if st.ClaudeSessionID != "" {
				ids = []string{st.ClaudeSessionID}
			}
			if !gate.ask(st.WorktreePath, ids) {
				continue
			}
			// git's error text can quote the worktree too.
			if _, err := worktree.DestroySession(ctx, instance, st.SessionID, false /* force */, swept, swept); err != nil {
				fmt.Fprintf(stderr, "niwa: warning: removing unchanged worktree %s: %s\n",
					removal.ScrubEvidence(st.WorktreePath, 400), removal.ScrubEvidence(err.Error(), 400))
				continue
			}
			fmt.Fprintf(stderr, "niwa: removed unchanged worktree %s: the Claude Code session that asked for it has ended\n",
				removal.ScrubEvidence(st.WorktreePath, 400))
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
