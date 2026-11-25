package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Under --force a worktree the walk won't remove still goes with the
// instance directory, and the output names it with the walk's reason.
func TestDestroyWorktree_ForceNamesAWorktreeLeftToTheInstanceDirectory(t *testing.T) {
	root, clone, wt := destroyWorktreeInstance(t)
	passGit(t, clone, "worktree", "lock", wt)
	chdirTo(t, root)
	stderr := quietDestroyCmd()
	setDestroyFlags(t, true, false, "")
	if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
		t.Fatalf("destroy --force: %v", err)
	}
	want := "worktree wt goes with the instance directory: the worktree is locked: app/.git/worktrees/wt/locked"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("destroy --force output %q; want it to hold %q", stderr.String(), want)
	}
	if _, err := os.Lstat(filepath.Join(root, "alpha")); !os.IsNotExist(err) {
		t.Fatalf("the instance is still there: %v", err)
	}
}
