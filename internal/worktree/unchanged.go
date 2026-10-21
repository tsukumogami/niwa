package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// WorktreeUnchanged reports whether a session's worktree holds nothing beyond
// what it was created with, so removing it can't lose anything. It is the
// condition automatic teardown requires, stricter than DestroySession's dirty
// guard, which only looks at the working tree:
//
//   - the record has a base commit and an ignored-file baseline (older records
//     never qualify);
//   - `git status` shows nothing, untracked files included;
//   - every git-ignored file was already there when niwa finished setting the
//     worktree up (IgnoredBaseline). `git worktree remove --force` deletes
//     ignored files, so build output, an env file or notes left under any
//     ignore pattern count as a change, including one niwa's own `*.local*`
//     pattern would hide;
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
	if state.IgnoredBaseline == nil {
		return false, "no baseline of its ignored files is recorded", nil
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
	ignored, err := ListIgnoredFiles(ctx, state.WorktreePath, gitInvoker)
	if err != nil {
		return false, "", err
	}
	baseline := make(map[string]bool, len(*state.IgnoredBaseline))
	for _, p := range *state.IgnoredBaseline {
		baseline[p] = true
	}
	for _, p := range ignored {
		if !baseline[p] {
			return false, "it has git-ignored files that weren't there when niwa set it up, such as " + p, nil
		}
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

// ListIgnoredFiles returns the git-ignored files in a worktree, sorted, as
// paths relative to it. The hook records it as the session's IgnoredBaseline
// once it has set the worktree up.
func ListIgnoredFiles(ctx context.Context, worktreePath string, gitInvoker GitInvoker) ([]string, error) {
	out, err := gitInvoker.CommandContext(ctx, "-C", worktreePath, "ls-files", "-z", "--others", "--ignored", "--exclude-standard").Output()
	if err != nil {
		return nil, fmt.Errorf("listing ignored files in %s: %w", worktreePath, err)
	}
	files := []string{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			files = append(files, p)
		}
	}
	sort.Strings(files)
	return files, nil
}
