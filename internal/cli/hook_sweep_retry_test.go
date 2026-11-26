package cli

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/removal/removaltest"
)

// A removal the sweep's rmtree.go remover stops leaves the session record
// Active, so the next sweep, which takes only Active records, tries again and
// removes the worktree once nothing stops it.
//
// The stop is the removal walk's test hook swapping the worktree's own
// directory for a link to an outside directory the first time the walk is
// about to open it. The test then puts the directory back and sweeps again.
// Everything is under t.TempDir().

// onceSwap swaps the first path it is called with that names the worktree,
// in either spelling, for a link to linkTo, renaming the directory aside.
type onceSwap struct {
	mu      sync.Mutex
	targets []string
	linkTo  string
	fired   string
	err     error
}

func (s *onceSwap) hook(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fired != "" {
		return
	}
	p := filepath.Clean(path)
	for _, target := range s.targets {
		if p != target {
			continue
		}
		s.fired = target
		if err := os.Rename(target, target+".swapped"); err != nil {
			s.err = err
			return
		}
		s.err = os.Symlink(s.linkTo, target)
		return
	}
}

// undo puts the swapped directory back where it was.
func (s *onceSwap) undo(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fired == "" {
		return
	}
	if err := os.Remove(s.fired); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(s.fired+".swapped", s.fired); err != nil {
		t.Fatal(err)
	}
}

func TestSweepRule_AStoppedRemovalLeavesTheRecordActiveAndTheNextSweepRetries(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	wt := filepath.Clean(fx.st.WorktreePath)
	targets := []string{wt}
	if resolved := resolvePathForCompare(wt); resolved != wt {
		targets = append(targets, resolved)
	}
	outside := removaltest.NewOutside(t)
	swap := &onceSwap{targets: targets, linkTo: outside.Dir}
	restore := removal.SetBeforeOpenHook(swap.hook)
	t.Cleanup(restore)

	n, out := sweep(t, fx.f, fx.jobs)
	if swap.fired == "" {
		t.Fatalf("the sweep never walked %s to remove it (removed %d), so nothing stopped the removal\nstderr:\n%s", wt, n, out)
	}
	if swap.err != nil {
		t.Fatalf("swapping %s: %v", swap.fired, swap.err)
	}
	if n != 0 {
		t.Errorf("removed %d after the removal stopped, want 0\nstderr:\n%s", n, out)
	}
	outside.AssertUnchanged(t)

	swap.undo(t)
	restore()
	assertKept(t, fx.f, fx.st)
	if !strings.Contains(gitIn(t, fx.f.repoPath, "worktree", "list", "--porcelain"), filepath.Base(wt)) {
		t.Errorf("worktree %s is no longer registered after a removal that stopped", wt)
	}
	if !outputNamesPath(out, fx.f.root, wt) {
		t.Errorf("sweep output does not name %s:\n%s", wt, out)
	}

	n, out = sweep(t, fx.f, fx.jobs)
	if n != 1 {
		t.Fatalf("the next sweep removed %d, want the worktree removed on the retry\nstderr:\n%s", n, out)
	}
	assertRemoved(t, fx.f, fx.st)
}
