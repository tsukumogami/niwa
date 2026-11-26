package removal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/removal/removaltest"
)

// The walk's root swapped for a link after the check and before the
// removal: the instance (niwa destroy's root) or the tombstone (the pass's)
// is moved aside and a link to it put in its place. The removal must not
// open its root through the link, so it stops with nothing removed and every
// file of the link's target still there.

// swapRootForLink moves root aside to root+".swapped" and puts a link to it
// at root, the way removaltest.Swap swaps a directory the walk reaches. It
// returns the link's target.
func swapRootForLink(t *testing.T, root string) string {
	t.Helper()
	target := root + ".swapped"
	swap := removaltest.NewSwap(root, target)
	swap.Hook(root)
	swap.AssertFired(t)
	return target
}

// assertTargetKept checks every file of the worktree, its admin directory
// and the clone is still in the link's target, and that root is still the
// link.
func assertTargetKept(t *testing.T, root, target string) {
	t.Helper()
	for _, rel := range []string{
		"wt/.git",
		"wt/README",
		"wt/deep/a/b/file.txt",
		"app/README",
		"app/.git/worktrees/wt/HEAD",
	} {
		if _, err := os.Lstat(filepath.Join(target, filepath.FromSlash(rel))); err != nil {
			t.Errorf("the link's target lost %s: %v", rel, err)
		}
	}
	if fi, err := os.Lstat(root); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%s is no longer the link: %v", root, err)
	}
}

func TestRemoveWorktree_RefusesAnInstanceSwappedForALinkAfterTheCheck(t *testing.T) {
	f := newWorktreeFixture(t)
	if err := CheckWorktree(f.instance, f.wt, f.admin); err != nil {
		t.Fatalf("check: %v", err)
	}
	target := swapRootForLink(t, f.instance)
	err := RemoveWorktree(f.instance, f.wt, f.admin)
	assertTargetKept(t, f.instance, target)
	wantStop(t, err, "changed during the pass: .", true)
}

func TestRemoveMovedWorktree_RefusesATombstoneSwappedForALinkAfterTheCheck(t *testing.T) {
	f := newWorktreeFixture(t)
	if err := CheckWorktree(f.instance, f.wt, f.admin); err != nil {
		t.Fatalf("check: %v", err)
	}
	tomb := movedWorktreeFixture(t, f)
	moved := Worktree{Rel: "wt", WorkTree: f.wt, AdminDir: f.admin}.Moved(f.instance, tomb)
	target := swapRootForLink(t, tomb)
	err := RemoveMovedWorktree(tomb, []string{f.instance}, moved.WorkTree, moved.AdminDir)
	assertTargetKept(t, tomb, target)
	wantStop(t, err, "changed during the pass: .", true)
}
