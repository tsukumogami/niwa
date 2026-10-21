package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorktreeUnchanged reports whether a session's worktree holds nothing beyond
// what it was created with, so removing it can't lose anything. It is the
// condition automatic teardown requires, stricter than DestroySession's dirty
// guard, which only looks at the working tree:
//
//   - the record has a base commit (older records never qualify);
//   - `git status` shows nothing, untracked files included;
//   - no git-ignored file exists beyond the ones the repo's info/exclude
//     covers, which is where niwa writes the patterns for its own files.
//     `git worktree remove --force` deletes ignored files, so build output,
//     an env file or notes a subagent left under a .gitignore pattern count
//     as a change;
//   - HEAD and the session branch both still point at the base commit, so
//     nothing was committed and the checkout wasn't moved;
//   - no stash entry was made from the session branch. The stash is shared by
//     every worktree of the repo, so entries are matched by the branch named
//     in their message.
//
// When it returns false the string says which condition failed. Any git
// failure is returned as an error, which callers treat as "changed".
func WorktreeUnchanged(ctx context.Context, state SessionLifecycleState, gitInvoker GitInvoker) (bool, string, error) {
	if state.BaseCommit == "" {
		return false, "no base commit is recorded", nil
	}
	if state.WorktreePath == "" {
		return false, "", errors.New("worktree path is empty")
	}
	if _, err := os.Stat(state.WorktreePath); err != nil {
		return false, "", fmt.Errorf("checking worktree %s: %w", state.WorktreePath, err)
	}
	git := func(args ...string) (string, error) {
		out, err := gitInvoker.CommandContext(ctx, append([]string{"-C", state.WorktreePath}, args...)...).Output()
		if err != nil {
			return "", fmt.Errorf("git %s in %s: %w", strings.Join(args, " "), state.WorktreePath, err)
		}
		return strings.TrimSpace(string(out)), nil
	}

	status, err := git("status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, "", err
	}
	if status != "" {
		return false, "it has uncommitted or untracked files", nil
	}
	if extra, err := ignoredBeyondExclude(state.WorktreePath, git); err != nil {
		return false, "", err
	} else if extra != "" {
		return false, "it has git-ignored files niwa didn't write, such as " + extra, nil
	}
	head, err := git("rev-parse", "HEAD")
	if err != nil {
		return false, "", err
	}
	if head != state.BaseCommit {
		return false, "its HEAD is no longer the commit it started at", nil
	}
	branch := state.EffectiveBranchName()
	tip, err := git("rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return false, "", err
	}
	if tip != state.BaseCommit {
		return false, "branch " + branch + " has moved off the commit it started at", nil
	}
	stashes, err := git("stash", "list", "--format=%gs")
	if err != nil {
		return false, "", err
	}
	for _, line := range strings.Split(stashes, "\n") {
		if strings.HasPrefix(line, "WIP on "+branch+":") || strings.HasPrefix(line, "On "+branch+":") {
			return false, "a stash entry was made from branch " + branch, nil
		}
	}
	return true, "", nil
}

// ignoredBeyondExclude returns the first git-ignored file in the worktree that
// the repo's info/exclude patterns don't match, or "" when there is none.
// niwa writes the patterns for everything it materializes into info/exclude
// (internal/gitexclude), so a file only a .gitignore or a global excludes file
// ignores was put there by someone else.
func ignoredBeyondExclude(worktreePath string, git func(...string) (string, error)) (string, error) {
	ignored, err := git("ls-files", "-z", "--others", "--ignored", "--exclude-standard")
	if err != nil || ignored == "" {
		return "", err
	}
	excludeFile, err := git("rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(excludeFile) {
		excludeFile = filepath.Join(worktreePath, excludeFile)
	}
	owned := map[string]bool{}
	if _, statErr := os.Stat(excludeFile); statErr == nil {
		out, err := git("ls-files", "-z", "--others", "--ignored", "--exclude-from="+excludeFile)
		if err != nil {
			return "", err
		}
		for _, p := range strings.Split(out, "\x00") {
			owned[p] = true
		}
	}
	for _, p := range strings.Split(ignored, "\x00") {
		if p != "" && !owned[p] {
			return p, nil
		}
	}
	return "", nil
}
