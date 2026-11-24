package removal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func worktreeEnv(t *testing.T, f wtFixture) *Env {
	t.Helper()
	e := NewEnv(filepath.Dir(f.instance), t.TempDir(), os.Getenv("PATH"))
	t.Cleanup(e.Close)
	return e
}

func TestFindWorktrees_ListsTheLinkedWorktreesInsideTheInstance(t *testing.T) {
	f := newWorktreeFixture(t)
	got := FindWorktrees(context.Background(), f.instance)
	if len(got) != 1 || got[0].Rel != "wt" || got[0].WorkTree != f.wt || got[0].AdminDir != f.admin {
		t.Fatalf("FindWorktrees = %+v; want wt with %s", got, f.admin)
	}
}

func TestWorktreeStatus_ReportsEveryChangeAndNothingWhenClean(t *testing.T) {
	f := newWorktreeFixture(t)
	gitRun(t, f.wt, "add", "deep")
	gitRun(t, f.wt, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "deep")
	e := worktreeEnv(t, f)
	w := Worktree{Rel: "wt", WorkTree: f.wt, AdminDir: f.admin}
	got, err := e.WorktreeStatus(context.Background(), w)
	if err != nil || len(got) != 0 {
		t.Fatalf("clean status = %q, %v; want no entry", got, err)
	}
	writeFile(t, filepath.Join(f.wt, "new.txt"), "new\n")
	writeFile(t, filepath.Join(f.wt, "README"), "changed\n")
	got, err = e.WorktreeStatus(context.Background(), w)
	if err != nil || len(got) != 2 || !strings.Contains(strings.Join(got, ","), "new.txt") || !strings.Contains(strings.Join(got, ","), "README") {
		t.Fatalf("status = %q, %v; want README and new.txt", got, err)
	}
}

func TestWorktreeStatus_AKeyThatNamesAProgramIsTheError(t *testing.T) {
	f := newWorktreeFixture(t)
	gitRun(t, f.clone, "config", "filter.x.clean", "cat")
	e := worktreeEnv(t, f)
	_, err := e.WorktreeStatus(context.Background(), Worktree{Rel: "wt", WorkTree: f.wt, AdminDir: f.admin})
	if err == nil || !strings.Contains(err.Error(), "repository configuration names a program: filter.x.clean") {
		t.Fatalf("status err = %v; want the key named", err)
	}
}
