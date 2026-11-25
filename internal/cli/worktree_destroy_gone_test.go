package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// A forced `niwa worktree destroy` of an Active session whose worktree
// directory is already gone judges the session branch against the proof
// remotes first: an unheld branch keeps, with the record Active; a held one
// goes on to Ended with git's exit codes kept.
//
// Everything is under t.TempDir(); the home directory and the removal
// environment point there too.

// newGoneHeldFixture is a workspace whose configuration names the repository
// by a local bare remote that holds main, with file remotes admitted in the
// removal environment, and one hook session in it.
func newGoneHeldFixture(t *testing.T, name string) (*createFlowFixture, worktree.SessionLifecycleState) {
	t.Helper()
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	t.Cleanup(func() { resetSessionCreateFlags(t) })

	bare := filepath.Join(canonicalTempDir(t), "myrepo.git")
	passGit(t, f.repoPath, "init", "-q", "--bare", "-b", "main", bare)
	passGit(t, f.repoPath, "remote", "add", "origin", "file://"+bare)
	passGit(t, f.repoPath, "push", "-q", "origin", "main")
	cfg := "[workspace]\nname = \"testws\"\n\n[groups." + f.group + "]\n\n[repos." + f.repo + "]\nurl = \"file://" + bare + "\"\ngroup = \"" + f.group + "\"\n\n[repos." + f.repo + ".claude]\nenabled = false\n"
	if err := os.WriteFile(workspaceConfigPath(f.root), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	home := canonicalTempDir(t)
	prevHome, prevEnv := userHomeDir, newRemovalEnv
	userHomeDir = func() string { return home }
	newRemovalEnv = func(r, _, pathEnv string) *removal.Env {
		return removal.NewEnv(r, home, pathEnv, removal.WithFileRemotes())
	}
	t.Cleanup(func() { userHomeDir, newRemovalEnv = prevHome, prevEnv })

	st := createViaHook(t, f, name)
	if st.CreatedBy != worktree.CreatedByWorktreeHook {
		t.Fatalf("record created by %q, want the hook", st.CreatedBy)
	}
	return f, st
}

// goneForcedDestroy removes the worktree (and its admin directory when
// removeAdmin is set), checks the record still reads Active, and runs the
// forced destroy.
func goneForcedDestroy(t *testing.T, f *createFlowFixture, st worktree.SessionLifecycleState, removeAdmin bool) (stdout, stderr string, err error) {
	t.Helper()
	admin := goneAdminDir(t, st.WorktreePath)
	if err := os.RemoveAll(st.WorktreePath); err != nil {
		t.Fatal(err)
	}
	if removeAdmin {
		if err := os.RemoveAll(admin); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := worktree.ReadSessionLifecycleState(f.sessionsDir, st.SessionID); err != nil || got.Status != worktree.SessionStatusActive {
		t.Fatalf("before the destroy the record is %q (%v), want active", got.Status, err)
	}
	sessionDestroyForce = true
	cmd, out, errb := newCallerCmd()
	err = destroyOneWorktree(cmd, f.root, st.SessionID)
	sessionDestroyForce = false
	return out.String(), errb.String(), err
}

func goneLogLines(t *testing.T, log string) []string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func goneLogHas(lines []string, part string) bool {
	for _, line := range lines {
		if strings.Contains(line, part) {
			return true
		}
	}
	return false
}

func goneRecordStatus(t *testing.T, f *createFlowFixture, st worktree.SessionLifecycleState) string {
	t.Helper()
	got, err := worktree.ReadSessionLifecycleState(f.sessionsDir, st.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	return got.Status
}

func goneExitCode(err error) int {
	var ec *sessionattach.ExitCodeError
	if errors.As(err, &ec) {
		return ec.Code
	}
	return -1
}

func TestWorktreeDestroyGone_AKeepLeavesTheRecordActiveAndTheBranch(t *testing.T) {
	f := newCreateFlowFixture(t)
	goneIsolatedHome(t)
	resetSessionCreateFlags(t)
	t.Cleanup(func() { resetSessionCreateFlags(t) })
	st := createViaHook(t, f, "gone-keep")
	branch := st.EffectiveBranchName()
	gitIn(t, st.WorktreePath, "commit", "-q", "--allow-empty", "-m", "unpushed work")
	commit := gitIn(t, st.WorktreePath, "rev-parse", "HEAD")

	log := goneLoggingGit(t)
	stdout, stderr, err := goneForcedDestroy(t, f, st, false)

	if code := goneExitCode(err); code != 1 {
		t.Errorf("destroyOneWorktree --force = %v; want exit 1 for a kept branch", err)
	}
	if status := goneRecordStatus(t, f, st); status != worktree.SessionStatusActive {
		t.Errorf("record after the keep is %q, want active", status)
	}
	if tip := gitIn(t, f.repoPath, "rev-parse", "refs/heads/"+branch); tip != commit {
		t.Errorf("branch %s points at %s after the keep, want %s", branch, tip, commit)
	}
	if got := gitIn(t, f.repoPath, "cat-file", "-t", commit); got != "commit" {
		t.Errorf("commit %s reads as %q after the keep", commit, got)
	}
	if !strings.Contains(stderr, "keeping branch "+branch) || !strings.Contains(stderr, st.SessionID) {
		t.Errorf("stderr doesn't name the kept branch and the session:\n%s", stderr)
	}
	if strings.Contains(stdout, "session: destroyed") {
		t.Errorf("stdout says destroyed after a keep:\n%s", stdout)
	}
	lines := goneLogLines(t, log)
	for _, part := range []string{" branch -D ", " branch -d ", "worktree remove", "worktree prune"} {
		if goneLogHas(lines, part) {
			t.Errorf("the kept destroy ran git %q:\n%s", part, strings.Join(lines, "\n"))
		}
	}
}

func TestWorktreeDestroyGone_AHeldBranchGoesOnToEnded(t *testing.T) {
	for _, tc := range []struct {
		name        string
		removeAdmin bool
	}{
		{"admin dir still present", false},
		{"admin dir gone too", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, st := newGoneHeldFixture(t, "gone-held")
			branch := st.EffectiveBranchName()
			gitIn(t, st.WorktreePath, "commit", "-q", "--allow-empty", "-m", "pushed work")
			commit := gitIn(t, st.WorktreePath, "rev-parse", "HEAD")
			gitIn(t, st.WorktreePath, "push", "-q", "origin", branch)
			admin := goneAdminDir(t, st.WorktreePath)

			log := goneLoggingGit(t)
			stdout, stderr, err := goneForcedDestroy(t, f, st, tc.removeAdmin)

			if err != nil {
				t.Fatalf("destroyOneWorktree --force = %v; want a held branch removed\nstderr:\n%s", err, stderr)
			}
			if status := goneRecordStatus(t, f, st); status != worktree.SessionStatusEnded {
				t.Errorf("record after the destroy is %q, want ended", status)
			}
			if listed := gitIn(t, f.repoPath, "branch", "--list", branch); listed != "" {
				t.Errorf("branch %s is still there after the destroy of a held branch: %q", branch, listed)
			}
			if got := gitIn(t, f.repoPath, "ls-remote", "origin", "refs/heads/"+branch); !strings.HasPrefix(got, commit) {
				t.Errorf("the proof remote's %s reads %q, want %s", branch, got, commit)
			}
			if _, err := os.Lstat(admin); !os.IsNotExist(err) {
				t.Errorf("admin dir %s is still there (lstat %v)", admin, err)
			}
			if !strings.Contains(stdout, "session: destroyed "+st.SessionID) {
				t.Errorf("stdout = %q; want the destroyed line", stdout)
			}
			if !goneSaysAlreadyGone(stderr, f.root, st) {
				t.Errorf("stderr doesn't say the worktree was already gone:\n%s", stderr)
			}
			lines := goneLogLines(t, log)
			if !goneLogHas(lines, " branch -D ") {
				t.Errorf("no git branch -D ran:\n%s", strings.Join(lines, "\n"))
			}
			ranRemove := goneLogHas(lines, " worktree remove --force ")
			if tc.removeAdmin && ranRemove {
				t.Errorf("git worktree remove ran with the admin dir already gone:\n%s", strings.Join(lines, "\n"))
			}
			if !tc.removeAdmin && !ranRemove {
				t.Errorf("git worktree remove --force didn't run with the admin dir present:\n%s", strings.Join(lines, "\n"))
			}
		})
	}
}

// failingBranchDeleteGit puts a git on PATH that fails git branch -D without
// running it and runs the real git for everything else.
func failingBranchDeleteGit(t *testing.T) {
	t.Helper()
	real := realGit(t)
	dir := canonicalTempDir(t)
	script := fmt.Sprintf(`#!/bin/sh
prev=""
for a in "$@"; do
	if [ "$prev" = branch ] && [ "$a" = -D ]; then
		echo "fatal: branch delete failed by the test" >&2
		exit 1
	fi
	prev="$a"
done
exec '%s' "$@"
`, real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestWorktreeDestroyGone_AFailingBranchDeleteIsReportedAndReturned(t *testing.T) {
	f, st := newGoneHeldFixture(t, "gone-dfail")
	branch := st.EffectiveBranchName()
	gitIn(t, st.WorktreePath, "commit", "-q", "--allow-empty", "-m", "pushed work")
	commit := gitIn(t, st.WorktreePath, "rev-parse", "HEAD")
	gitIn(t, st.WorktreePath, "push", "-q", "origin", branch)

	failingBranchDeleteGit(t)
	stdout, stderr, err := goneForcedDestroy(t, f, st, false)

	if code := goneExitCode(err); code != 1 {
		t.Errorf("destroyOneWorktree --force = %v; want exit 1 for a failed branch -D", err)
	}
	if !strings.Contains(stderr, "branch "+branch+" was not deleted") || !strings.Contains(stderr, "branch delete failed by the test") {
		t.Errorf("stderr doesn't report the failed branch -D with git's line:\n%s", stderr)
	}
	if strings.Contains(stdout, "session: destroyed") {
		t.Errorf("stdout says destroyed after a failed branch -D:\n%s", stdout)
	}
	if tip := gitIn(t, f.repoPath, "rev-parse", "refs/heads/"+branch); tip != commit {
		t.Errorf("branch %s points at %s, want %s", branch, tip, commit)
	}
	if status := goneRecordStatus(t, f, st); status != worktree.SessionStatusEnded {
		t.Errorf("record is %q, want ended: the removal succeeded before the branch delete", status)
	}
}

// A guard: on the forced path with the worktree present, a failing branch -D
// is still dropped as before.
func TestWorktreeDestroyGone_APresentWorktreeKeepsTheForcedPathAsBefore(t *testing.T) {
	f := newCreateFlowFixture(t)
	goneIsolatedHome(t)
	resetSessionCreateFlags(t)
	t.Cleanup(func() { resetSessionCreateFlags(t) })
	st := createViaHook(t, f, "gone-present")

	failingBranchDeleteGit(t)
	sessionDestroyForce = true
	cmd, out, errb := newCallerCmd()
	err := destroyOneWorktree(cmd, f.root, st.SessionID)
	sessionDestroyForce = false

	if err != nil {
		t.Fatalf("destroyOneWorktree --force = %v; the present-worktree path drops a failed branch -D\nstderr:\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "session: destroyed "+st.SessionID) {
		t.Errorf("stdout = %q; want the destroyed line", out.String())
	}
	if strings.Contains(errb.String(), "already gone") {
		t.Errorf("stderr calls a present worktree gone:\n%s", errb.String())
	}
}
