package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/removal/removaltest"
)

// The walk's root swapped for a link after every read and before the first
// worktree removal: the tombstone in a reap (after the tombstone check), the
// instance in niwa destroy (after the fresh status). The root is moved aside
// and a link to it put in its place, through the seam each flow removes its
// worktrees by. The walk must not open its root through the link, so the unit
// is kept, the destroy refuses, and nothing in the link's target is removed.

// swapRootOnce moves root aside to root+".swapped" and puts a link to it at
// root. It returns the link's target.
func swapRootOnce(t *testing.T, root string) string {
	t.Helper()
	target := root + ".swapped"
	swap := removaltest.NewSwap(root, target)
	swap.Hook(root)
	swap.AssertFired(t)
	return target
}

// assertRootTargetKept checks each of rels is still in the link's target.
func assertRootTargetKept(t *testing.T, target string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if _, err := os.Lstat(filepath.Join(target, filepath.FromSlash(rel))); err != nil {
			t.Errorf("the link's target lost %s: %v", rel, err)
		}
	}
}

func TestRootSwap_TheReapKeepsATombstoneSwappedForALinkAndRemovesNothingInItsTarget(t *testing.T) {
	f, fake := swapPassUnit(t)
	var target string
	prev := removeMovedWorktreeFunc
	removeMovedWorktreeFunc = func(tomb string, forms []string, workTree, adminDir string) error {
		if target == "" {
			target = swapRootOnce(t, tomb)
		}
		return prev(tomb, forms, workTree, adminDir)
	}
	t.Cleanup(func() { removeMovedWorktreeFunc = prev })
	removed := realRemoval(t)
	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if target == "" {
		t.Fatalf("the pass never reached the worktree removal\nstderr:\n%s", f.stderr.String())
	}
	assertRootTargetKept(t, target, "wt/.git", "wt/deep/a/b/file.txt", "tools/app/.git/worktrees/wt/HEAD")
	if res.Reaped != 0 || len(*removed) != 0 {
		t.Fatalf("reaped %d, removed %v; want the unit kept\nstderr:\n%s", res.Reaped, *removed, f.stderr.String())
	}
	want := "worktree wt was not removed: changed during the pass: ."
	if len(res.Kept) != 1 || !strings.Contains(res.Kept[0].Reason, want) {
		t.Fatalf("kept %+v; want the unit kept with %q\nstderr:\n%s", res.Kept, want, f.stderr.String())
	}
	if len(fake.removed) != 0 {
		t.Errorf("claude rm ran for a kept unit: %q", fake.removed)
	}
}

// rootSwapRemover swaps the instance for a link to it before its first
// removal, then removes through the no-follow walk.
type rootSwapRemover struct {
	t      *testing.T
	target *string
}

func (r rootSwapRemover) RemoveWorktree(instance, workTree, adminDir string) error {
	if *r.target == "" {
		*r.target = swapRootOnce(r.t, instance)
	}
	return removal.RemoveWorktree(instance, workTree, adminDir)
}

func TestRootSwap_AnUnforcedDestroyRefusesAnInstanceSwappedForALinkAndRemovesNothingInItsTarget(t *testing.T) {
	root, _, _ := destroyWorktreeInstance(t)
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, false, false, "")
	scanFileRemotes(t)
	var target string
	prev := worktreeRemover
	worktreeRemover = rootSwapRemover{t: t, target: &target}
	t.Cleanup(func() { worktreeRemover = prev })
	err := runDestroy(destroyCmd, []string{"alpha"})
	if target == "" {
		t.Fatalf("the destroy never reached the worktree removal: %v", err)
	}
	assertRootTargetKept(t, target, "wt/.git", "wt/README", "app/README", "app/.git/worktrees/wt/HEAD")
	want := "destroy stopped before worktree wt was removed: changed during the pass: ."
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("destroy = %v; want it refused with %q", err, want)
	}
}
