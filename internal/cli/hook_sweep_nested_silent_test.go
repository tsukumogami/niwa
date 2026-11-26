package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// Red-first: the silent-skip case. A nested repository (its own git init,
// with a commit no remote holds) under a swept hook worktree, with no
// ignored-file baseline recorded, is untracked in the worktree, so the
// sweep's status check (WorktreeUnchanged) keeps the worktree first, and
// today that keep prints nothing. The worktree is kept, the nested commit
// survives and the record stays Active (guards); the sweep's stderr must
// also carry a keep line naming the nested repository or the worktree (red
// today).
//
// Everything is under t.TempDir().

func TestSweepRule_SilentSkipNestedRepositoryKeptByStatusCheckSaysSo(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	nested := filepath.Join(fx.st.WorktreePath, "scratch")
	passGit(t, fx.st.WorktreePath, "init", "-q", "-b", "main", nested)
	writeTestFile(t, filepath.Join(nested, "notes.txt"), "only here\n")
	passGit(t, nested, "add", "notes.txt")
	passGit(t, nested, "commit", "-q", "-m", "local only")
	commit := gitIn(t, nested, "rev-parse", "HEAD")
	// No recordIgnoredBaseline call: the nested repository is untracked, and
	// the status check is what keeps the worktree.

	n, out := sweep(t, fx.f, fx.jobs)
	if n != 0 {
		t.Fatalf("removed %d, want the worktree holding a nested repository kept\nstderr:\n%s", n, out)
	}
	assertKept(t, fx.f, fx.st)
	if got := gitIn(t, nested, "rev-parse", "HEAD"); got != commit {
		t.Errorf("the nested repository's HEAD is %s, want %s", got, commit)
	}
	if got := gitIn(t, nested, "cat-file", "-t", commit); got != "commit" {
		t.Errorf("the nested repository's commit %s reads as %q, want commit", commit, got)
	}

	keepLine := false
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(strings.ToLower(line), "kept") && !strings.Contains(strings.ToLower(line), "keep") {
			continue
		}
		if outputNamesPath(line, fx.f.root, nested) || outputNamesPath(line, fx.f.root, fx.st.WorktreePath) {
			keepLine = true
			break
		}
	}
	if !keepLine {
		t.Errorf("the sweep kept worktree %s for its untracked nested repository %s without a keep line naming either\nstderr:\n%s", fx.st.WorktreePath, nested, out)
	}
}
