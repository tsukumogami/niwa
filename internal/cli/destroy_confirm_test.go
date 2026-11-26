package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// confirmDestroyAt answers niwa destroy's confirmation with name, as a
// person at a terminal would.
func confirmDestroyAt(t *testing.T, name string) {
	t.Helper()
	prevTTY := IsStdinTTY
	IsStdinTTY = func() bool { return true }
	destroyCmd.SetIn(strings.NewReader(name + "\n"))
	t.Cleanup(func() {
		IsStdinTTY = prevTTY
		destroyCmd.SetIn(nil)
	})
}

// An unforced destroy at a terminal lists a worktree the walk won't remove
// (one locked, one holding a repository) with the scan's findings, and once
// the instance name is typed, leaves it to go with the instance directory:
// the walk is never asked to remove it, and the destroy goes through.
func TestDestroyWorktree_AConfirmedDestroyLeavesAWorktreeTheWalkWontRemoveToTheInstanceDirectory(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, clone, wt string)
		want  string
	}{
		{"locked", func(t *testing.T, clone, wt string) {
			passGit(t, clone, "worktree", "lock", wt)
		}, "wt: a worktree niwa won't remove on its own (the worktree is locked: app/.git/worktrees/wt/locked)"},
		{"holding a repository", func(t *testing.T, _, wt string) {
			passGit(t, wt, "init", "-q", filepath.Join(wt, "nested"))
		}, "wt: a worktree niwa won't remove on its own (a repository at wt/nested/.git)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, clone, wt := destroyWorktreeInstance(t)
			c.plant(t, clone, wt)
			chdirTo(t, root)
			stderr := quietDestroyCmd()
			setDestroyFlags(t, false, false, "")
			confirmDestroyAt(t, "alpha")
			var asked []string
			prev := worktreeRemover
			worktreeRemover = recordingRemover{asked: &asked}
			t.Cleanup(func() { worktreeRemover = prev })
			if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
				t.Fatalf("destroy after the instance name was typed: %v\nstderr:\n%s", err, stderr.String())
			}
			if !strings.Contains(stderr.String(), c.want) {
				t.Fatalf("listing %q; want it to hold %q", stderr.String(), c.want)
			}
			if len(asked) != 0 {
				t.Fatalf("the walk was asked to remove %q; want the worktree left to the instance directory", asked)
			}
			if _, err := os.Lstat(filepath.Join(root, "alpha")); !os.IsNotExist(err) {
				t.Fatalf("the instance is still there: %v", err)
			}
		})
	}
}

// recordingRemover records each worktree the destroy asks the walk to
// remove, and removes nothing.
type recordingRemover struct {
	asked *[]string
}

func (r recordingRemover) RemoveWorktree(instance, workTree, adminDir string) error {
	*r.asked = append(*r.asked, workTree)
	return nil
}
