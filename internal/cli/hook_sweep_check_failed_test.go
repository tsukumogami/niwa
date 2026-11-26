package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// When the hook sweep's unchanged check fails with an error, the sweep keeps
// the worktree, as before, and now says so on stderr with the error. The
// check fails here for real: the worktree sits detached at its base commit,
// so it is otherwise unchanged, and its session branch is deleted, so the
// check's read of refs/heads/<branch> fails.
//
// Everything is under t.TempDir().
func TestSweepRule_AFailedUnchangedCheckKeepsAndSaysSo(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	branch := fx.st.EffectiveBranchName()
	gitIn(t, fx.st.WorktreePath, "checkout", "-q", "--detach", fx.st.BaseCommit)
	gitIn(t, fx.f.repoPath, "branch", "-D", "-q", "--", branch)

	n, out := sweep(t, fx.f, fx.jobs)

	if n != 0 {
		t.Fatalf("removed %d, want the worktree kept when its check fails\nstderr:\n%s", n, out)
	}
	if _, err := os.Lstat(fx.st.WorktreePath); err != nil {
		t.Errorf("worktree %s after the sweep: %v; want it kept", fx.st.WorktreePath, err)
	}
	got, err := worktree.ReadSessionLifecycleState(fx.f.sessionsDir, fx.st.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != worktree.SessionStatusActive {
		t.Errorf("record after the sweep is %q, want active", got.Status)
	}
	// The path passes the scrub on stderr, as every sweep keep line's does.
	want := "niwa: kept worktree " + removal.ScrubEvidence(fx.st.WorktreePath, 400) + ": check failed: "
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, want) {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("stderr has no %q line:\n%s", want, out)
	}
	if !strings.Contains(line, "refs/heads/"+branch) {
		t.Errorf("the kept line doesn't carry the check's error about refs/heads/%s: %q", branch, line)
	}
}
