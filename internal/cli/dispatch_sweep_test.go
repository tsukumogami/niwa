package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// The hook worktree sweep reached through niwa dispatch, in process: a
// dispatch run at the workspace root runs the opportunistic reap, which
// sweeps the root's hook-made worktrees before the new instance is made. The
// dispatch seams are fakes; the sweep, its git calls and its removal are
// real. The sweep prints to os.Stderr, which the test captures.
//
// Every repository, driver and marker file here is under t.TempDir().

// dispatchSweep runs niwa dispatch from the fixture's root and returns what
// it printed on os.Stderr and on the command's stderr.
func dispatchSweep(t *testing.T, fx sweepRuleFixture) string {
	t.Helper()
	chdir(t, fx.f.root)
	installDispatchFakes(t, fx.f.root)
	var cmdErr string
	var err error
	osErr := captureOSStderr(t, func() {
		_, cmdErr, err = runDispatchCmd(t, "check the sweep")
	})
	if err != nil {
		t.Logf("dispatch returned %v", err)
	}
	return osErr + cmdErr
}

// assertSweepReached fails the test when no logged git call names the
// worktree, which means the dispatch never got as far as the sweep.
func assertSweepReached(t *testing.T, log string, fx sweepRuleFixture, out string) {
	t.Helper()
	spell := pathSpellings(fx.st.WorktreePath)
	for _, line := range scanOrderLog(t, log) {
		if namesAnyPath(line, spell) {
			return
		}
	}
	t.Fatalf("no git call named the worktree %s: the dispatch never reached the sweep\noutput:\n%s", fx.st.WorktreePath, out)
}

func TestDispatchSweep_FsmonitorNeverRunsAndKeeps(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	passGit(t, fx.f.repoPath, "config", "core.fsmonitor", markerScript(t, marker))
	log := gitCallLog(t)

	out := dispatchSweep(t, fx)
	assertNoMarker(t, marker)
	assertSweepReached(t, log, fx, out)
	assertKept(t, fx.f, fx.st)
	if !strings.Contains(out, "core.fsmonitor") {
		t.Errorf("dispatch output does not name core.fsmonitor:\n%s", out)
	}
	assertNoWorktreeRemoval(t, log, false)
}

func TestDispatchSweep_CleanFilterKeepsWithKeyNamed(t *testing.T) {
	fx := newSweepRuleFixture(t, filterAttributesSeed(t))
	marker := filepath.Join(t.TempDir(), "filter-ran")
	passGit(t, fx.f.repoPath, "config", "filter.x.clean", markerScript(t, marker))
	touchLater(t, filepath.Join(fx.st.WorktreePath, "a.txt"))
	log := gitCallLog(t)

	out := dispatchSweep(t, fx)
	assertNoMarker(t, marker)
	assertSweepReached(t, log, fx, out)
	assertKept(t, fx.f, fx.st)
	if !strings.Contains(out, "filter.x.clean") {
		t.Errorf("dispatch output does not name filter.x.clean:\n%s", out)
	}
	assertNoWorktreeRemoval(t, log, false)
}

func TestDispatchSweep_SwapRuns(t *testing.T) {
	for _, run := range sweepSwapRuns {
		t.Run(run.name, func(t *testing.T) {
			fx := newSweepRuleFixture(t, deepSeed(t))
			target := run.target(t, fx)
			log := gitCallLog(t)
			outside, swap := installSweepSwap(t, target)

			out := dispatchSweep(t, fx)
			assertSweepReached(t, log, fx, out)
			assertSweepSwapKept(t, fx, target, swap, outside, out, log)
		})
	}
}
