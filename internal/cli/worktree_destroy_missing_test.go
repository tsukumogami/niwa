package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// goneIsolatedHome points the home directory and the removal environment the
// destroy builds at a temporary directory, so the test never reads the real
// home or the person's git configuration and credential helpers.
func goneIsolatedHome(t *testing.T) {
	t.Helper()
	home := canonicalTempDir(t)
	prevHome, prevEnv := userHomeDir, newRemovalEnv
	userHomeDir = func() string { return home }
	newRemovalEnv = func(r, _, pathEnv string) *removal.Env { return removal.NewEnv(r, home, pathEnv) }
	t.Cleanup(func() { userHomeDir, newRemovalEnv = prevHome, prevEnv })
}

// Red-first tests for a session whose worktree directory is already gone
// while its record still reads Active, which happens when the Ended write
// fails after a successful removal. `niwa worktree destroy <id> --force` is
// the only way to clear such a record, and it must not take the session
// branch with it: the branch carries a commit the base branch doesn't have,
// and the worktree that might have explained it is gone. The output must
// also say the worktree was already gone.
//
// Each subtest logs every git branch -D call the destroy made with its exit
// status and git's stderr, so the run shows what git did. A leftover admin
// directory doesn't save the branch: the discarded git worktree remove
// --force exits 0 on a missing work tree and clears the admin directory, so
// the branch -D that follows succeeds in both subtests.
//
// Everything is under t.TempDir().

// goneLoggingGit puts a git on PATH that runs the real git and appends each
// call's arguments, exit status and stderr to a log, one line per call. It
// returns the log's path.
func goneLoggingGit(t *testing.T) string {
	t.Helper()
	real := realGit(t)
	dir := canonicalTempDir(t)
	log := filepath.Join(canonicalTempDir(t), "git-calls.log")
	script := fmt.Sprintf(`#!/bin/sh
err=$(mktemp)
'%s' "$@" 2>"$err"
rc=$?
cat "$err" >&2
printf '%%s => exit %%d: %%s\n' "$*" "$rc" "$(tr '\n' ' ' < "$err")" >> '%s'
rm -f "$err"
exit $rc
`, real, log)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// goneAdminDir reads the worktree's .git file and returns the admin
// directory it points at under the repository's .git/worktrees/.
func goneAdminDir(t *testing.T, worktreePath string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(worktreePath, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, "gitdir: ") {
		t.Fatalf("the worktree's .git file reads %q, want a gitdir line", line)
	}
	admin := strings.TrimPrefix(line, "gitdir: ")
	if !filepath.IsAbs(admin) {
		admin = filepath.Join(worktreePath, admin)
	}
	if _, err := os.Stat(admin); err != nil {
		t.Fatalf("admin dir %s: %v", admin, err)
	}
	return admin
}

// goneSaysAlreadyGone reports whether out has a line that names the
// worktree path or the session id and says the worktree was already gone.
func goneSaysAlreadyGone(out, root string, st worktree.SessionLifecycleState) bool {
	for _, line := range strings.Split(out, "\n") {
		if !outputNamesPath(line, root, st.WorktreePath) && !strings.Contains(line, st.SessionID) {
			continue
		}
		lower := strings.ToLower(line)
		for _, word := range []string{"gone", "missing", "not exist", "no longer exists", "no such"} {
			if strings.Contains(lower, word) {
				return true
			}
		}
	}
	return false
}

func TestWorktreeDestroyForce_GoneWorktreeKeepsAnUnpushedBranch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		removeAdmin bool
	}{
		{"admin dir still present", false},
		{"admin dir gone too", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCreateFlowFixture(t)
			goneIsolatedHome(t)
			resetSessionCreateFlags(t)
			t.Cleanup(func() { resetSessionCreateFlags(t) })
			st := createViaHook(t, f, "gone")
			if st.CreatedBy != worktree.CreatedByWorktreeHook {
				t.Fatalf("record created by %q, want the hook", st.CreatedBy)
			}
			branch := st.EffectiveBranchName()

			// A commit on the session branch that main doesn't have.
			gitIn(t, st.WorktreePath, "commit", "-q", "--allow-empty", "-m", "unpushed work")
			commit := gitIn(t, st.WorktreePath, "rev-parse", "HEAD")
			if out := gitIn(t, f.repoPath, "branch", "--list", "--contains", commit, "main"); out != "" {
				t.Fatalf("main already contains %s: %q", commit, out)
			}

			admin := goneAdminDir(t, st.WorktreePath)
			if err := os.RemoveAll(st.WorktreePath); err != nil {
				t.Fatal(err)
			}
			if tc.removeAdmin {
				if err := os.RemoveAll(admin); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := worktree.ReadSessionLifecycleState(f.sessionsDir, st.SessionID); err != nil || got.Status != worktree.SessionStatusActive {
				t.Fatalf("before the destroy the record is %q (%v), want active", got.Status, err)
			}

			log := goneLoggingGit(t)
			sessionDestroyForce = true
			cmd, out, errb := newCallerCmd()
			err := destroyOneWorktree(cmd, f.root, st.SessionID)
			sessionDestroyForce = false
			output := out.String() + errb.String()
			t.Logf("destroyOneWorktree --force returned %v\noutput:\n%s", err, output)

			raw, readErr := os.ReadFile(log)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			sawDelete := false
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				if strings.Contains(line, " branch -D ") || strings.Contains(line, " worktree remove ") {
					t.Logf("git call: %s", line)
				}
				if strings.Contains(line, " branch -D ") {
					sawDelete = true
				}
			}
			if !sawDelete {
				t.Logf("git call: no branch -D was run")
			}
			if got, err := worktree.ReadSessionLifecycleState(f.sessionsDir, st.SessionID); err == nil {
				t.Logf("record after the destroy: %s", got.Status)
			}

			if listed := gitIn(t, f.repoPath, "branch", "--list", branch); listed == "" {
				t.Errorf("the forced destroy of a session whose worktree was already gone deleted branch %s and its unpushed commit %s", branch, commit)
			} else if tip := gitIn(t, f.repoPath, "rev-parse", "refs/heads/"+branch); tip != commit {
				t.Errorf("branch %s points at %s after the destroy, want its unpushed commit %s", branch, tip, commit)
			}
			if !goneSaysAlreadyGone(output, f.root, st) {
				t.Errorf("the destroy's output doesn't say worktree %s (session %s) was already gone:\n%s", st.WorktreePath, st.SessionID, output)
			}
		})
	}
}
