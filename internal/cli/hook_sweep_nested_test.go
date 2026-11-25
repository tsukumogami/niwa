package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pins, not red-first tests: they pass at the code that moved the hook
// worktree sweep through the removal rule, and hold that behavior.
//
// An independent repository nested under a swept hook worktree, with its own
// .git directory and a commit no remote holds, keeps that worktree: the sweep
// removes nothing, the nested repository and its commit are still there, and
// the session record stays Active. An untracked nested repository is kept by
// the worktree's status check; one under an ignored path gets past that check
// and is kept by the removal walk, which names it.
//
// Everything is under t.TempDir().

func TestSweepRule_ANestedRepositoryKeepsTheWorktree(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	nested := filepath.Join(fx.st.WorktreePath, "scratch")
	passGit(t, fx.st.WorktreePath, "init", "-q", "-b", "main", nested)
	writeTestFile(t, filepath.Join(nested, "notes.txt"), "only here\n")
	passGit(t, nested, "add", "notes.txt")
	passGit(t, nested, "commit", "-q", "-m", "local only")
	commit := gitIn(t, nested, "rev-parse", "HEAD")

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
	// The output isn't checked here: the untracked scratch/ keeps the
	// worktree at the status check, which the sweep skips without a line.
}

func TestSweepRule_ANestedRepositoryUnderAnIgnoredPathKeepsTheWorktree(t *testing.T) {
	fx := newSweepRuleFixture(t, func(repo string) {
		writeTestFile(t, filepath.Join(repo, ".gitignore"), "ignored/\n")
		passGit(t, repo, "add", ".gitignore")
		passGit(t, repo, "commit", "-q", "-m", "ignore")
	})
	nested := filepath.Join(fx.st.WorktreePath, "ignored", "scratch")
	passGit(t, fx.st.WorktreePath, "init", "-q", "-b", "main", nested)
	// The nested repository is there when niwa finishes setting the
	// worktree up, so its ignored directory is in the recorded fingerprints
	// and the unchanged check passes it; the commit comes after.
	recordIgnoredBaseline(io.Discard, fx.f.root, fx.st.SessionID, fx.st.WorktreePath)
	writeTestFile(t, filepath.Join(nested, "notes.txt"), "only here\n")
	passGit(t, nested, "add", "notes.txt")
	passGit(t, nested, "commit", "-q", "-m", "local only")
	commit := gitIn(t, nested, "rev-parse", "HEAD")

	n, out := sweep(t, fx.f, fx.jobs)
	// The case has to reach DestroySession's remover: the warning line is
	// printed only when the removal stops.
	if !strings.Contains(out, "removing unchanged worktree") {
		t.Fatalf("the sweep didn't reach the removal; want the removal walk to keep the worktree\nstderr:\n%s", out)
	}
	if n != 0 {
		t.Fatalf("removed %d, want the worktree holding a nested repository kept\nstderr:\n%s", n, out)
	}
	assertKept(t, fx.f, fx.st)
	// The walk checks the whole tree before it removes anything, so the
	// worktree's own files are still there too.
	if _, err := os.Stat(filepath.Join(fx.st.WorktreePath, ".gitignore")); err != nil {
		t.Errorf("the worktree's .gitignore is gone after a kept removal: %v", err)
	}
	if got := gitIn(t, nested, "rev-parse", "HEAD"); got != commit {
		t.Errorf("the nested repository's HEAD is %s, want %s", got, commit)
	}
	if got := gitIn(t, nested, "cat-file", "-t", commit); got != "commit" {
		t.Errorf("the nested repository's commit %s reads as %q, want commit", commit, got)
	}
	if !outputNamesPath(out, fx.f.root, nested) {
		t.Errorf("sweep output does not name the nested repository %s:\n%s", nested, out)
	}
}
