package removal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A linked worktree inside an instance renamed to its tombstone: its .git
// file still names the admin directory under the instance's original path,
// which the walk inside the tombstone maps onto the tombstone, and the check
// on the tombstone reads it there by explicit directories.

// movedWorktreeFixture renames the worktree fixture's instance to a
// tombstone beside it and returns the tombstone's path.
func movedWorktreeFixture(t *testing.T, f wtFixture) string {
	t.Helper()
	tomb := filepath.Join(filepath.Dir(f.instance), ".niwa-tombstone-inst-0011aabb")
	if err := os.Rename(f.instance, tomb); err != nil {
		t.Fatal(err)
	}
	return tomb
}

func TestRemoveMovedWorktree_RemovesTheWorktreeInsideTheTombstone(t *testing.T) {
	f := newWorktreeFixture(t)
	gitRun(t, f.wt, "add", "deep")
	gitRun(t, f.wt, "commit", "-q", "-m", "deep")
	tomb := movedWorktreeFixture(t, f)
	moved := Worktree{Rel: "wt", WorkTree: f.wt, AdminDir: f.admin}.Moved(f.instance, tomb)
	if moved.WorkTree != filepath.Join(tomb, "wt") || moved.AdminDir != filepath.Join(tomb, "app", ".git", "worktrees", "wt") {
		t.Fatalf("moved %+v; want both directories under %s", moved, tomb)
	}

	// Without the original path the .git file doesn't name the admin
	// directory where it now is, and the worktree is kept.
	err := RemoveWorktree(tomb, moved.WorkTree, moved.AdminDir)
	wantStop(t, err, "doesn't name the worktree's admin directory", false)
	if _, err := os.Lstat(filepath.Join(moved.WorkTree, "README")); err != nil {
		t.Fatalf("the worktree lost a file on a keep: %v", err)
	}

	if err := RemoveMovedWorktree(tomb, []string{f.instance}, moved.WorkTree, moved.AdminDir); err != nil {
		t.Fatalf("remove: %v", err)
	}
	for _, p := range []string{moved.WorkTree, moved.AdminDir, filepath.Join(tomb, "app", ".git", "worktrees")} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is still there: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(tomb, "app", "README")); err != nil {
		t.Errorf("the clone lost a file: %v", err)
	}
	if out := gitRun(t, filepath.Join(tomb, "app"), "worktree", "list", "--porcelain"); strings.Contains(out, "wt-branch") {
		t.Errorf("git still lists the worktree:\n%s", out)
	}
}

// An original path that isn't the one the .git file names keeps the
// worktree.
func TestRemoveMovedWorktree_KeepsWhenNoOriginalPathMatches(t *testing.T) {
	f := newWorktreeFixture(t)
	tomb := movedWorktreeFixture(t, f)
	moved := Worktree{Rel: "wt", WorkTree: f.wt, AdminDir: f.admin}.Moved(f.instance, tomb)
	err := RemoveMovedWorktree(tomb, []string{filepath.Join(filepath.Dir(f.instance), "elsewhere")}, moved.WorkTree, moved.AdminDir)
	wantStop(t, err, "doesn't name the worktree's admin directory", false)
	if _, err := os.Lstat(filepath.Join(moved.WorkTree, ".git")); err != nil {
		t.Fatalf("the worktree's .git file was removed: %v", err)
	}
}

// The check on the tombstone reads a worktree the pass read before the
// rename at its tombstone paths: nothing differs for an unchanged one, a
// file written there after the rename is named, and a linked worktree the
// pass didn't read still counts as changed.
func TestTakeMovedSnapshot_ReadsACheckedWorktreeAtItsTombstonePaths(t *testing.T) {
	setup := func(t *testing.T) (fixture, Policy, Verdict, Snapshot) {
		t.Helper()
		f := newFixture(t)
		checkoutMain(t, f)
		gitRun(t, f.clone, "worktree", "add", "-q", filepath.Join(f.instance, "wt"), "-b", "wt-branch", "main")
		gitRun(t, f.clone, "push", "-q", "origin", "wt-branch")
		p := f.policy(t)
		v := Evaluate(context.Background(), f.unit(), p)
		if v.Kind != Reap {
			t.Fatalf("verdict %s, checks %+v; want reap", v.Kind, v.Checks)
		}
		before := p.Env.TakeSnapshot(context.Background(), v, f.instance, f.instance, nil)
		if d := before.RuleDifferences(v); len(d) != 0 {
			t.Fatalf("re-read differences: %v", d)
		}
		return f, p, v, before
	}
	t.Run("unchanged", func(t *testing.T) {
		f, p, v, before := setup(t)
		tomb := renameToTombstone(t, f)
		after := p.Env.TakeMovedSnapshot(context.Background(), v, f.instance, tomb, []string{"wt"})
		if d := CompareSnapshots(before, after, nil); len(d) != 0 {
			t.Fatalf("differences across the rename of an unchanged unit with a checked worktree: %v", d)
		}
	})
	t.Run("a file written after the rename", func(t *testing.T) {
		f, p, v, before := setup(t)
		tomb := renameToTombstone(t, f)
		if err := os.WriteFile(filepath.Join(tomb, "wt", "late.txt"), []byte("late\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		after := p.Env.TakeMovedSnapshot(context.Background(), v, f.instance, tomb, []string{"wt"})
		if d := CompareSnapshots(before, after, nil); !containsPart(d, "wt/late.txt") {
			t.Fatalf("differences %v; want the late file in the worktree named", d)
		}
	})
	t.Run("a worktree the pass didn't read", func(t *testing.T) {
		f, p, v, before := setup(t)
		tomb := renameToTombstone(t, f)
		after := p.Env.TakeMovedSnapshot(context.Background(), v, f.instance, tomb, nil)
		if d := CompareSnapshots(before, after, nil); !containsPart(d, "wt: a linked worktree names its admin directory by a path the rename moved") {
			t.Fatalf("differences %v; want the unread worktree counted as changed", d)
		}
	})
}
