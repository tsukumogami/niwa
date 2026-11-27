package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/worktree"
)

// The hook worktree sweep takes a record's worktree path only when it lies
// inside the instance as text, with no link followed below the instance
// root, and names the worktree the walk found there. A record that fails
// keeps the worktree with one "kept worktree" line, stays Active, and no git
// call names the path it carries or the worktree it reaches.
//
// Every repository, link and marker file here is under t.TempDir().

// setRecordedWorktreePath rewrites the fixture's session record to carry
// path as its worktree path.
func setRecordedWorktreePath(t *testing.T, fx sweepRuleFixture, path string) {
	t.Helper()
	got, err := worktree.ReadSessionLifecycleState(fx.f.sessionsDir, fx.st.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	got.WorktreePath = path
	if err := worktree.WriteSessionLifecycleState(fx.f.sessionsDir, got); err != nil {
		t.Fatal(err)
	}
}

// assertNoGitNames fails on any logged git call that names one of paths in
// either spelling.
func assertNoGitNames(t *testing.T, log string, paths ...string) {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var spellings []string
	for _, p := range paths {
		spellings = append(spellings, pathSpellings(p)...)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" && namesAnyPath(line, spellings) {
			t.Errorf("the sweep ran git naming the recorded path: %s", line)
		}
	}
}

// assertBoundKept checks the sweep kept the fixture's worktree on its
// record's path, with one line saying so.
func assertBoundKept(t *testing.T, fx sweepRuleFixture, n int, out string) {
	t.Helper()
	if n != 0 {
		t.Fatalf("removed %d, want the worktree kept\nstderr:\n%s", n, out)
	}
	assertKept(t, fx.f, fx.st)
	if c := strings.Count(out, "niwa: kept worktree "); c != 1 {
		t.Errorf("sweep output has %d kept worktree lines, want 1:\n%s", c, out)
	}
}

func TestSweepBound_ARecordOutsideTheInstanceIsKeptWithNoGitThere(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	outside := canonicalTempDir(t)
	repo := filepath.Join(outside, "repo")
	outWT := filepath.Join(outside, "wt")
	passGit(t, outside, "init", "-q", "-b", "main", repo)
	passGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	passGit(t, repo, "worktree", "add", "-q", outWT, "-b", "outside")
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	passGit(t, repo, "config", "core.fsmonitor", markerScript(t, marker))
	setRecordedWorktreePath(t, fx, outWT)
	log := destroyGitWrapper(t, "")

	n, out := sweep(t, fx.f, fx.jobs)
	assertNoMarker(t, marker)
	assertBoundKept(t, fx, n, out)
	assertNoGitNames(t, log, outWT, outside)
	if _, err := os.Stat(outWT); err != nil {
		t.Errorf("the outside worktree was removed: %v", err)
	}
}

func TestSweepBound_ARecordWithADotDotComponentIsKeptWithNoGitThere(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	wt := fx.st.WorktreePath
	rec := filepath.Dir(wt) + "/../" + filepath.Base(filepath.Dir(wt)) + "/" + filepath.Base(wt)
	setRecordedWorktreePath(t, fx, rec)
	log := destroyGitWrapper(t, "")

	n, out := sweep(t, fx.f, fx.jobs)
	assertBoundKept(t, fx, n, out)
	assertNoGitNames(t, log, rec, wt)
}

func TestSweepBound_ARecordNamingAnOutsideLinkIntoTheInstanceIsKeptWithNoGitThere(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	link := filepath.Join(canonicalTempDir(t), "wt-link")
	if err := os.Symlink(fx.st.WorktreePath, link); err != nil {
		t.Fatal(err)
	}
	setRecordedWorktreePath(t, fx, link)
	log := destroyGitWrapper(t, "")

	n, out := sweep(t, fx.f, fx.jobs)
	assertBoundKept(t, fx, n, out)
	assertNoGitNames(t, log, link, fx.st.WorktreePath)
}

// The control: a record naming the worktree under the other spelling of the
// instance, the one with the links above it resolved or not, is still
// removed.
func TestSweepBound_ARecordUnderEitherSpellingOfTheInstanceIsRemoved(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	given := filepath.Clean(fx.f.root)
	real := resolvePathForCompare(given)
	rec := fx.st.WorktreePath
	if rel, err := filepath.Rel(real, rec); err == nil && !strings.HasPrefix(rel, "..") {
		rec = filepath.Join(given, rel)
	} else if rel, err := filepath.Rel(given, rec); err == nil && !strings.HasPrefix(rel, "..") {
		rec = filepath.Join(real, rel)
	}
	for _, spelling := range []string{fx.st.WorktreePath, rec} {
		if !strings.HasPrefix(spelling, given+"/") && !strings.HasPrefix(spelling, real+"/") {
			t.Fatalf("record %s is under neither %s nor %s", spelling, given, real)
		}
	}
	setRecordedWorktreePath(t, fx, rec)

	n, out := sweep(t, fx.f, fx.jobs)
	if n != 1 {
		t.Fatalf("removed %d, want the worktree removed\nstderr:\n%s", n, out)
	}
	assertRemoved(t, fx.f, fx.st)
}
