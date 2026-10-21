package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WorktreeUnchanged reports whether a session's worktree holds nothing beyond
// what it was created with, so removing it can't lose anything. It is the
// condition automatic teardown requires, stricter than DestroySession's dirty
// guard, which only looks at the working tree:
//
//   - the record has a base commit and ignored-file fingerprints (older
//     records never qualify);
//   - `git status` shows nothing, untracked files included;
//   - the git-ignored files are exactly the ones there when niwa finished
//     setting the worktree up, with the same contents (IgnoredFingerprints).
//     `git worktree remove --force` deletes ignored files, so an ignored file
//     added since (build output, a `.env.local`, notes, even under niwa's own
//     `*.local*` pattern), one deleted since, or one edited since (a
//     materialized settings.local.json, a copied env file) counts as a change;
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
	if state.IgnoredFingerprints == nil || state.IgnoredFingerprints.Files == nil {
		return false, "no fingerprints of its ignored files are recorded", nil
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
	// A tracked file marked assume-unchanged or skip-worktree hides its edits
	// from `git status`, so such a mark alone counts as a change. `ls-files
	// -v` tags the first with a lowercase letter and the second with "S".
	tags, err := git("ls-files", "-v")
	if err != nil {
		return false, "", err
	}
	for _, line := range strings.Split(tags, "\n") {
		if line != "" && (line[0] == 'S' || (line[0] >= 'a' && line[0] <= 'z')) {
			return false, "a tracked file is marked assume-unchanged or skip-worktree: " + strings.TrimSpace(line[1:]), nil
		}
	}
	now, err := FingerprintIgnoredFiles(ctx, state.WorktreePath, gitInvoker)
	if err != nil {
		return false, "", err
	}
	if reason := compareFingerprints(state.IgnoredFingerprints.Files, now.Files); reason != "" {
		return false, reason, nil
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

// compareFingerprints returns why the current ignored files differ from the
// baseline, or "" when they are the same files with the same fingerprints.
// Paths are visited in sorted order so the reason is stable.
func compareFingerprints(baseline, current map[string]string) string {
	paths := make([]string, 0, len(baseline)+len(current))
	for p := range baseline {
		paths = append(paths, p)
	}
	for p := range current {
		if _, ok := baseline[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		was, inBaseline := baseline[p]
		is, inCurrent := current[p]
		switch {
		case !inBaseline:
			return "it has git-ignored files that weren't there when niwa set it up, such as " + p
		case !inCurrent:
			return "a git-ignored file that was there when niwa set it up has been deleted: " + p
		case was != is:
			return "a git-ignored file that was there when niwa set it up has changed: " + p
		}
	}
	return ""
}

// FingerprintIgnoredFiles fingerprints every git-ignored file in a worktree;
// see IgnoredFingerprints for the forms. A link is fingerprinted by its target
// and never followed, so nothing outside the worktree is read. The hook
// records the result once it has set the worktree up.
//
// Every ignored file is read in full, at create and again at each check. That
// cost is accepted: the files are local, an agent worktree rarely holds large
// ignored trees, and a slow or failed check only keeps the worktree (a
// SubagentStop that times out is a non-blocking error), never removes one.
func FingerprintIgnoredFiles(ctx context.Context, worktreePath string, gitInvoker GitInvoker) (IgnoredFingerprints, error) {
	paths, err := ListIgnoredFiles(ctx, worktreePath, gitInvoker)
	if err != nil {
		return IgnoredFingerprints{}, err
	}
	files := make(map[string]string, len(paths))
	for _, p := range paths {
		fp, err := fingerprintFile(filepath.Join(worktreePath, p))
		if err != nil {
			return IgnoredFingerprints{}, err
		}
		files[p] = fp
	}
	return IgnoredFingerprints{Files: files}, nil
}

func fingerprintFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("fingerprinting %s: %w", path, err)
	}
	switch {
	case info.Mode().IsRegular():
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("fingerprinting %s: %w", path, err)
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return "", fmt.Errorf("fingerprinting %s: %w", path, err)
		}
		// The permission bits are part of the fingerprint, so a chmod
		// counts as a change too.
		return fmt.Sprintf("sha256:%s %04o", hex.EncodeToString(h.Sum(nil)), info.Mode().Perm()), nil
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return "", fmt.Errorf("fingerprinting %s: %w", path, err)
		}
		sum := sha256.Sum256([]byte(target))
		return "symlink:" + hex.EncodeToString(sum[:]), nil
	default:
		return "mode:" + info.Mode().String(), nil
	}
}

// ListIgnoredFiles returns the git-ignored files in a worktree, sorted, as
// paths relative to it.
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
