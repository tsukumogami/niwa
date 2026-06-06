// Package worktree holds the per-session git-worktree lifecycle: creating a
// worktree on a fresh branch, scaffolding its .niwa layout, persisting the
// session lifecycle state, and tearing the worktree down again. It is a leaf
// package — it imports neither internal/mcp nor internal/workspace — so the
// CLI, the bootstrap orchestrator, and (until it is removed) the mcp server
// can all share one implementation of the worktree+session primitives.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitInvoker is the test-injection seam for the session-create pipeline's
// git subprocess calls. It is the structural equivalent of
// workspace.GitInvoker — using a local interface here avoids an import
// cycle while letting production callers pass workspace.StdGitInvoker()
// directly. Go's structural typing makes the cross-package interface
// assignment compile without an explicit adapter.
type GitInvoker interface {
	CommandContext(ctx context.Context, args ...string) *exec.Cmd
}

// CreateSessionParams collects the inputs to CreateSession. The struct is
// what the CLI builds for the `session/<sid>` default and what RunBootstrap
// builds for the `niwa-bootstrap/<sid>` prefix.
//
// BranchPrefix carries the load-bearing decision for branch naming. Empty
// means "session/" (back-compat for every existing caller); a non-empty
// value (e.g., "niwa-bootstrap/") prepends to the generated session ID. The
// resulting branch name is persisted into SessionLifecycleState.BranchName so
// destroy and the push-hint warning resolve to the right ref.
//
// GitInvoker is the seam the worktree-add and branch-delete subprocess calls
// flow through so unit tests can record argv and inject failures without
// forking real git.
type CreateSessionParams struct {
	InstanceRoot    string
	Repo            string
	Purpose         string
	ParentSessionID string
	BranchPrefix    string
	GitInvoker      GitInvoker
}

// ErrSessionUnknownRole is returned by CreateSession when the role
// directory under <instanceRoot>/.niwa/roles/<repo> does not exist.
// Callers map this to the UNKNOWN_ROLE structured error code.
var ErrSessionUnknownRole = errors.New("unknown role")

// ErrSessionAttached is returned by DestroySession when the session's
// worktree is held by a live attach process and force is false. It guards
// the preserved worktree-attach primitive: destroying a worktree out from
// under a live `niwa session attach` process would force-remove a directory
// that process is actively using, risking loss of uncommitted work. Callers
// (e.g. the CLI destroy command) map this to the SESSION_ATTACHED structured
// error code. Passing force=true bypasses the guard.
var ErrSessionAttached = errors.New("session attached")

// scaffoldWorktreeNiwa creates the minimal .niwa layout for a per-session
// worktree. It creates:
//
//   - .niwa/roles/<repo>/inbox/{in-progress,cancelled,expired,read}/
//   - .niwa/tasks/
//   - .niwa/sessions/
//
// It does NOT create mcp.json or workspace-context.md — those are
// main-instance artifacts that are not needed in session worktrees.
func scaffoldWorktreeNiwa(worktreePath, repo string) error {
	niwaDir := filepath.Join(worktreePath, ".niwa")
	dirs := []string{
		niwaDir,
		filepath.Join(niwaDir, "tasks"),
		filepath.Join(niwaDir, "sessions"),
		filepath.Join(niwaDir, "roles", repo, "inbox"),
		filepath.Join(niwaDir, "roles", repo, "inbox", "in-progress"),
		filepath.Join(niwaDir, "roles", repo, "inbox", "cancelled"),
		filepath.Join(niwaDir, "roles", repo, "inbox", "expired"),
		filepath.Join(niwaDir, "roles", repo, "inbox", "read"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("scaffoldWorktreeNiwa: creating %s: %w", d, err)
		}
	}
	return nil
}

// findRepoInWorkspace scans instanceRoot two levels deep for a directory
// named repoName that contains a .git entry, returning its absolute path.
// Returns an error if no such directory is found.
func findRepoInWorkspace(instanceRoot, repoName string) (string, error) {
	topEntries, err := os.ReadDir(instanceRoot)
	if err != nil {
		return "", fmt.Errorf("scanning workspace: %w", err)
	}
	for _, top := range topEntries {
		if !top.IsDir() || strings.HasPrefix(top.Name(), ".") {
			continue
		}
		groupDir := filepath.Join(instanceRoot, top.Name())
		subEntries, err := os.ReadDir(groupDir)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if !sub.IsDir() || sub.Name() != repoName {
				continue
			}
			candidate := filepath.Join(groupDir, sub.Name())
			if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("repo %q not found in workspace %s", repoName, instanceRoot)
}

// CreateSession validates the role, generates a session ID, creates a git
// worktree on a new branch, scaffolds the .niwa layout, and writes the
// session state file. On any failure after the worktree is created the
// worktree is removed before returning.
//
// params.BranchPrefix controls the branch name: empty == historic
// `session/<sid>`, non-empty == `<prefix><sid>` (e.g. `niwa-bootstrap/<sid>`
// for the bootstrap orchestrator). The chosen branch name is persisted into
// SessionLifecycleState.BranchName via NewSessionLifecycleState so destroy
// and warning paths resolve correctly.
//
// All git invocations route through params.GitInvoker so unit tests can
// record argv and inject faults without a real git binary. Returns
// (sessionID, worktreePath, branchName, error). A non-nil error may have a
// non-empty branchName when the caller needs to clean up after CreateSession
// partially succeeded.
func CreateSession(ctx context.Context, params CreateSessionParams) (sessionID, worktreePath, branchName string, err error) {
	if params.Repo == "" {
		return "", "", "", errors.New("repo is required")
	}
	if params.Purpose == "" {
		return "", "", "", errors.New("purpose is required")
	}
	if params.GitInvoker == nil {
		return "", "", "", errors.New("git invoker not configured")
	}

	// Validate role directory exists.
	roleDir := filepath.Join(params.InstanceRoot, ".niwa", "roles", params.Repo)
	if _, statErr := os.Stat(roleDir); errors.Is(statErr, os.ErrNotExist) {
		return "", "", "", fmt.Errorf("%w: role %q not found at %s", ErrSessionUnknownRole, params.Repo, roleDir)
	}

	// Generate a session ID.
	sessionsDir := filepath.Join(params.InstanceRoot, ".niwa", "sessions")
	if mkErr := os.MkdirAll(sessionsDir, 0o700); mkErr != nil {
		return "", "", "", fmt.Errorf("creating sessions dir: %w", mkErr)
	}
	sid, idErr := newSessionLifecycleID(sessionsDir)
	if idErr != nil {
		return "", "", "", fmt.Errorf("generating session ID: %w", idErr)
	}

	// Find the actual git repo on disk.
	repoPath, repoErr := findRepoInWorkspace(params.InstanceRoot, params.Repo)
	if repoErr != nil {
		return "", "", "", fmt.Errorf("%w: %v", ErrSessionUnknownRole, repoErr)
	}

	// Worktree under <instanceRoot>/.niwa/worktrees/<repo>-<sid>/.
	worktreesDir := filepath.Join(params.InstanceRoot, ".niwa", "worktrees")
	if mkErr := os.MkdirAll(worktreesDir, 0o700); mkErr != nil {
		return "", "", "", fmt.Errorf("creating worktrees dir: %w", mkErr)
	}
	wtPath := filepath.Join(worktreesDir, params.Repo+"-"+sid)
	prefix := params.BranchPrefix
	if prefix == "" {
		prefix = "session/"
	}
	branch := prefix + sid

	// Create the worktree on a new branch via the injected invoker.
	addCmd := params.GitInvoker.CommandContext(ctx, "-C", repoPath, "worktree", "add", wtPath, "-b", branch)
	out, addErr := addCmd.CombinedOutput()
	if addErr != nil {
		return "", "", "", fmt.Errorf("git worktree add: %w\n%s", addErr, out)
	}

	// From here, any failure must clean up the worktree.
	cleanupWorktree := func() {
		removeCmd := params.GitInvoker.CommandContext(ctx, "-C", repoPath, "worktree", "remove", "--force", wtPath)
		_ = removeCmd.Run()
	}

	// Scaffold the .niwa layout in the worktree.
	if scaffoldErr := scaffoldWorktreeNiwa(wtPath, params.Repo); scaffoldErr != nil {
		cleanupWorktree()
		return "", "", branch, fmt.Errorf("scaffold: %w", scaffoldErr)
	}

	// Write the session state file. The branch name is persisted so destroy
	// and the push-hint warning resolve to the right ref.
	state := NewSessionLifecycleState(sid, params.Repo, params.Purpose, params.ParentSessionID, wtPath, branch)
	if writeErr := WriteSessionLifecycleState(sessionsDir, state); writeErr != nil {
		cleanupWorktree()
		return "", "", branch, fmt.Errorf("writing session state: %w", writeErr)
	}

	return sid, wtPath, branch, nil
}

// DestroySession tears down the worktree and branch for a session: it reads
// the session state, marks it ended, removes the git worktree, and deletes
// the session branch. It is the worktree-removal core extracted from the MCP
// destroy handler; mesh-side cleanup (worker kills, change cancellation,
// daemon teardown) is handled separately by the caller.
//
// When force is false the branch is deleted with `git branch -d` (only if
// merged); unmerged branches are left in place and a BranchWarning is
// returned on the state. When force is true `git branch -D` removes the
// branch regardless of merge status.
//
// DestroySession is idempotent: a session already in a terminal status is
// returned unchanged with no git operations performed.
//
// When the session's worktree is held by a live attach process and force is
// false, DestroySession performs no teardown and returns ErrSessionAttached
// so the preserved attach primitive is not clobbered. force=true bypasses
// this guard.
func DestroySession(ctx context.Context, instanceRoot, sessionID string, force bool, gitInvoker GitInvoker) (SessionLifecycleState, error) {
	if gitInvoker == nil {
		return SessionLifecycleState{}, errors.New("git invoker not configured")
	}
	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	state, err := ReadSessionLifecycleState(sessionsDir, sessionID)
	if err != nil {
		return SessionLifecycleState{}, err
	}

	// Idempotent: already terminal.
	if state.Status == SessionStatusEnded || state.Status == SessionStatusAbandoned {
		return state, nil
	}

	worktreePath := state.WorktreePath

	// Reject when an attach lock is held by a live process and force is not
	// set. This protects the preserved worktree-attach primitive: removing the
	// worktree out from under a live `niwa session attach` process would
	// force-delete a directory it is actively using. reapStale is false so a
	// genuinely live holder is never silently reaped. The message references
	// the recovery command and the holder PID; callers pattern-match the
	// ErrSessionAttached sentinel to surface the SESSION_ATTACHED error code.
	if attachState, attachAvail, _ := ReadAttachState(worktreePath, false); attachAvail == AttachAttached && !force {
		return state, fmt.Errorf(
			"%w: session %s is currently attached (pid=%d, started=%s); "+
				"run `niwa session detach %s --force` to release the attach lock first, "+
				"or pass force=true to destroy regardless",
			ErrSessionAttached, state.SessionID, attachState.OwnerPID, attachState.StartedAt, state.SessionID,
		)
	}

	// Write terminal state.
	state.Status = SessionStatusEnded
	if err := WriteSessionLifecycleState(sessionsDir, state); err != nil {
		return state, fmt.Errorf("writing session state: %w", err)
	}

	// Find the git repo to remove the worktree and delete the branch.
	repoPath, repoErr := findRepoInWorkspace(instanceRoot, state.Repo)
	if repoErr == nil && worktreePath != "" {
		_ = gitInvoker.CommandContext(ctx, "-C", repoPath, "worktree", "remove", "--force", worktreePath).Run()
		// Delete the session branch. With force=false, use git branch -d which
		// only succeeds when the branch is already merged; unmerged branches are
		// left in place so unfinished work is not discarded. With force=true,
		// git branch -D removes the branch regardless of merge status.
		branchArg := "-d"
		if force {
			branchArg = "-D"
		}
		// Resolve the branch name from session state so bootstrap-created
		// sessions and historic `session/<sid>` sessions both delete the
		// correct ref. EffectiveBranchName falls back to `session/<sid>` for
		// pre-v1.1 state files that pre-date the BranchName field.
		branchName := state.EffectiveBranchName()
		if err := gitInvoker.CommandContext(ctx, "-C", repoPath, "branch", branchArg, branchName).Run(); err != nil && !force {
			state.BranchWarning = fmt.Sprintf(
				"branch %s was not deleted (unmerged commits remain); review and delete manually: git -C %s branch -D %s",
				branchName, repoPath, branchName,
			)
		}
	}

	return state, nil
}

// StdGitInvoker is the production GitInvoker. Sibling of
// workspace.StdGitInvoker() — duplicated here so the worktree package stays a
// leaf and does not import workspace.
type StdGitInvoker struct{}

// CommandContext delegates to exec.CommandContext(ctx, "git", args...).
func (StdGitInvoker) CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "git", args...)
}
