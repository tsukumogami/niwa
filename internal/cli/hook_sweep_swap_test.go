package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/removal/removaltest"
)

// The swap runs on the hook worktree sweep's path: the removal walk's test
// hook swaps a directory for a link to an outside directory whose listing,
// sizes and hashes were recorded first. The swapped directory is a deep/
// entry, the worktree's own directory or its worktrees/<id> admin directory.
// In every run the outside directory is unchanged, its marker file is still
// there, and the sweep keeps the worktree with "changed during the pass" and
// the swapped path named, with no git worktree remove, prune or --force.
//
// Everything is under t.TempDir().

// deepSeed commits deep/a/b/file.txt in the repository's base commit, so
// the worktree checks out a deep entry and stays unchanged.
func deepSeed(t *testing.T) func(string) {
	return func(repo string) {
		writeTestFile(t, filepath.Join(repo, "deep", "a", "b", "file.txt"), "deep\n")
		passGit(t, repo, "add", "deep")
		passGit(t, repo, "commit", "-q", "-m", "deep")
	}
}

// sweepSwapRun is one of the three swap runs: its name and the directory it
// swaps.
type sweepSwapRun struct {
	name   string
	target func(t *testing.T, fx sweepRuleFixture) string
}

var sweepSwapRuns = []sweepSwapRun{
	{"deep entry", func(t *testing.T, fx sweepRuleFixture) string {
		return filepath.Join(fx.st.WorktreePath, "deep")
	}},
	{"worktree directory", func(t *testing.T, fx sweepRuleFixture) string {
		return fx.st.WorktreePath
	}},
	{"admin directory", func(t *testing.T, fx sweepRuleFixture) string {
		return gitIn(t, fx.st.WorktreePath, "rev-parse", "--absolute-git-dir")
	}},
}

// installSweepSwap makes the outside directory and puts the walk's hook in
// to swap target, in either spelling, for a link to it the first time the
// walk is about to open it.
func installSweepSwap(t *testing.T, target string) (*removaltest.Outside, *onceSwap) {
	t.Helper()
	outside := removaltest.NewOutside(t)
	swap := &onceSwap{targets: pathSpellings(target), linkTo: outside.Dir}
	t.Cleanup(removal.SetBeforeOpenHook(swap.hook))
	return outside, swap
}

// outputNamesPath reports whether out names target in either spelling,
// absolute or relative to root.
func outputNamesPath(out, root, target string) bool {
	targets := pathSpellings(target)
	if namesAnyPath(out, targets) {
		return true
	}
	for _, r := range pathSpellings(root) {
		for _, s := range targets {
			if rel, err := filepath.Rel(r, s); err == nil && !strings.HasPrefix(rel, "..") && strings.Contains(out, rel) {
				return true
			}
		}
	}
	return false
}

// assertSweepSwapKept checks one swap run after the sweep: the swap fired,
// nothing outside changed, the output says "changed during the pass" and
// names the swapped path, no git call removed, pruned or forced, and with
// the directory put back the worktree is still there.
func assertSweepSwapKept(t *testing.T, fx sweepRuleFixture, target string, swap *onceSwap, outside *removaltest.Outside, out, log string) {
	t.Helper()
	if swap.fired == "" {
		t.Fatalf("the sweep never walked %s, so the swap never ran\nstderr:\n%s", target, out)
	}
	if swap.err != nil {
		t.Fatalf("swapping %s: %v", swap.fired, swap.err)
	}
	outside.AssertUnchanged(t)
	if !strings.Contains(out, "changed during the pass") {
		t.Errorf("sweep output does not say \"changed during the pass\":\n%s", out)
	}
	if !outputNamesPath(out, fx.f.root, target) {
		t.Errorf("sweep output does not name the swapped path %s:\n%s", target, out)
	}
	assertNoWorktreeRemoval(t, log, false)
	swap.undo(t)
	assertKept(t, fx.f, fx.st)
}

func TestSweepRule_SwapRunsKeepWithChangedDuringThePass(t *testing.T) {
	for _, run := range sweepSwapRuns {
		t.Run(run.name, func(t *testing.T) {
			fx := newSweepRuleFixture(t, deepSeed(t))
			target := run.target(t, fx)
			log := gitCallLog(t)
			outside, swap := installSweepSwap(t, target)

			n, out := sweep(t, fx.f, fx.jobs)
			assertSweepSwapKept(t, fx, target, swap, outside, out, log)
			if n != 0 {
				t.Errorf("removed %d, want the worktree kept\nstderr:\n%s", n, out)
			}
		})
	}
}
