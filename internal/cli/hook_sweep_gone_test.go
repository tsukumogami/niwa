package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/worktree"
)

// A guard: the hook sweep on an Active hook-session record whose worktree
// directory is already gone reports it on stderr, naming the worktree path
// or the session id, and removes nothing: the record stays Active, the
// session branch and its unpushed commit stay, and the admin directory under
// .git/worktrees/ is left alone. Before the sweep went through the removal
// rule it skipped such a record without a line; since then the walk keeps
// it with a line naming the path, so every part passes today and holds that.
//
// Everything is under t.TempDir().

func TestSweepHookWorktrees_GoneWorktreeIsReportedAndNothingRemoved(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	st := fx.st
	branch := st.EffectiveBranchName()
	gitIn(t, st.WorktreePath, "commit", "-q", "--allow-empty", "-m", "unpushed work")
	commit := gitIn(t, st.WorktreePath, "rev-parse", "HEAD")
	admin := goneAdminDir(t, st.WorktreePath)
	if err := os.RemoveAll(st.WorktreePath); err != nil {
		t.Fatal(err)
	}

	n, out := sweep(t, fx.f, fx.jobs)
	t.Logf("sweep stderr:\n%s", out)
	if n != 0 {
		t.Errorf("removed %d, want nothing removed for a worktree that is already gone\nstderr:\n%s", n, out)
	}
	got, err := worktree.ReadSessionLifecycleState(fx.f.sessionsDir, st.SessionID)
	if err != nil || got.Status != worktree.SessionStatusActive {
		t.Errorf("record is %q (%v) after the sweep, want active", got.Status, err)
	}
	if listed := gitIn(t, fx.f.repoPath, "branch", "--list", branch); listed == "" {
		t.Errorf("branch %s was deleted", branch)
	} else if tip := gitIn(t, fx.f.repoPath, "rev-parse", "refs/heads/"+branch); tip != commit {
		t.Errorf("branch %s points at %s, want %s", branch, tip, commit)
	}
	if _, err := os.Stat(admin); err != nil {
		t.Errorf("the admin dir %s was removed: %v", admin, err)
	}
	if _, err := os.Stat(st.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("something was made at the gone worktree's path %s: %v", st.WorktreePath, err)
	}
	if !outputNamesPath(out, fx.f.root, st.WorktreePath) && !strings.Contains(out, st.SessionID) {
		t.Errorf("the sweep said nothing naming the gone worktree %s or session %s\nstderr:\n%s", st.WorktreePath, st.SessionID, out)
	}
}
