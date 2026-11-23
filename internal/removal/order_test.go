package removal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateListsEveryKeepingCheckInRuleOrder(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.instance, DispatchRetainMarker), "kept for review\n")
	writeFile(t, filepath.Join(f.clone, "notes.txt"), "n\n")
	writeFile(t, filepath.Join(f.instance, "loose.txt"), "l\n")
	p := f.policy(t)
	p.Processes = &Listing{Procs: []Process{{PID: 4242, Cwd: filepath.Join(f.instance, "tools")}}}

	v := Evaluate(context.Background(), f.unit(), p)
	var names []CheckName
	for _, c := range v.Checks {
		names = append(names, c.Name)
		if c.Evidence == "" || len(c.Paths) == 0 {
			t.Errorf("check %+v lacks paths or evidence", c)
		}
	}
	want := []CheckName{CheckLiveness, CheckHold, CheckRepository, CheckOutside}
	if strings.Join(checkNames(names), ",") != strings.Join(checkNames(want), ",") {
		t.Fatalf("checks %v, want %v", names, want)
	}
	first, _ := v.First()
	if first.Name != CheckLiveness || !strings.Contains(first.Evidence, "pid 4242") {
		t.Fatalf("first check %+v; want liveness naming the process", first)
	}
	if !strings.Contains(v.Checks[1].Evidence, "kept for review") {
		t.Errorf("hold evidence %q; want the recorded reason", v.Checks[1].Evidence)
	}
}

func checkNames(n []CheckName) []string {
	out := make([]string, len(n))
	for i, x := range n {
		out[i] = string(x)
	}
	return out
}

func TestLivenessKeepsWhenTheProcessListingFailed(t *testing.T) {
	f := newFixture(t)
	p := f.policy(t)
	p.Processes = &Listing{Err: errors.New("lsof timed out")}
	v := Evaluate(context.Background(), f.unit(), p)
	first, _ := v.First()
	if v.Kind != Keep || first.Name != CheckLiveness || !strings.Contains(first.Evidence, "lsof timed out") {
		t.Fatalf("verdict %s %+v; want liveness to keep on the failed listing", v.Kind, v.Checks)
	}
	p.Processes = nil
	if v := Evaluate(context.Background(), f.unit(), p); v.Kind != Keep {
		t.Fatalf("verdict %s with no listing; want keep", v.Kind)
	}
}

func TestHoldKeepsAnUnmappedInstanceWhetherOrNotTheMarkerReads(t *testing.T) {
	instance := t.TempDir()
	marker := filepath.Join(instance, DispatchRetainMarker)
	writeFile(t, marker, "worker produced work\n")
	u := Unit{InstancePath: instance, Liveness: LivenessGone}
	p := Policy{Processes: &Listing{}}
	v := Evaluate(context.Background(), u, p)
	first, _ := v.First()
	if first.Name != CheckHold || !strings.Contains(first.Evidence, "worker produced work") {
		t.Fatalf("checks %+v; want the hold with its reason", v.Checks)
	}
	if err := os.Chmod(marker, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(marker, 0o644) })
	v = Evaluate(context.Background(), u, p)
	if first, _ := v.First(); first.Name != CheckHold {
		t.Fatalf("checks %+v; want an unreadable marker to hold too", v.Checks)
	}
}

// orderRepo builds a clone that fails every per-repository check: a broken
// ref, an unpushed branch, a stash, a modified tracked file, an
// assume-unchanged entry and an untracked file.
func orderRepo(t *testing.T) fixture {
	f := checkedOut(t)
	writeFile(t, filepath.Join(f.clone, "b.txt"), "b\n")
	gitRun(t, f.clone, "add", "b.txt")
	gitRun(t, f.clone, "commit", "-q", "-m", "b")
	gitRun(t, f.clone, "push", "-q", "origin", "HEAD")
	gitRun(t, f.clone, "checkout", "-q", "-b", "work")
	writeFile(t, filepath.Join(f.clone, "c.txt"), "c\n")
	gitRun(t, f.clone, "add", "c.txt")
	gitRun(t, f.clone, "commit", "-q", "-m", "c")
	writeFile(t, filepath.Join(f.clone, "README"), "stashed\n")
	gitRun(t, f.clone, "stash", "-q")
	writeFile(t, filepath.Join(f.clone, "README"), "modified\n")
	gitRun(t, f.clone, "update-index", "--assume-unchanged", "b.txt")
	writeFile(t, filepath.Join(f.clone, "notes.txt"), "n\n")
	writeFile(t, filepath.Join(f.clone, ".git", "refs", "heads", "zz-broken"), "not-an-object-id\n")
	return f
}

func TestCheckRepoKeepsInTheRuleOrder(t *testing.T) {
	f := orderRepo(t)
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	want := []string{
		"the ref listing reported a problem",
		"commits the clone URL does not show: branch work",
		"stashes: stash@{0}",
		"modified tracked files: README",
		"index flag hides changes: assume-unchanged b.txt",
		"untracked files: notes.txt",
	}
	var got []string
	for _, c := range v.Checks {
		if c.Name == CheckRepository {
			got = append(got, c.Evidence)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("repository keeps %q, want %d in order %q", got, len(want), want)
	}
	for i := range want {
		if !strings.Contains(got[i], want[i]) {
			t.Errorf("keep %d is %q, want it to name %q", i, got[i], want[i])
		}
	}
	for _, e := range got {
		if strings.Contains(e, "refs/stash") {
			t.Errorf("the stash is named as a ref too: %q", e)
		}
	}
}

func TestEvaluateKeepsABrokenRefInACleanClone(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.clone, ".git", "refs", "heads", "broken"), "0000\n")
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	first, _ := v.First()
	if v.Kind != Keep || first.Name != CheckRepository || !strings.Contains(first.Evidence, "broken") {
		t.Fatalf("verdict %s %+v; want the repository check naming the broken ref", v.Kind, v.Checks)
	}
}

func TestIndexFlagsKeepASkipWorktreeEntryAndASparseCheckout(t *testing.T) {
	f := checkedOut(t)
	gitRun(t, f.clone, "update-index", "--skip-worktree", "README")
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	if first, _ := v.First(); !strings.Contains(first.Evidence, "index flag hides changes: skip-worktree README") {
		t.Fatalf("checks %+v; want the skip-worktree flag named", v.Checks)
	}
	g := checkedOut(t)
	gitRun(t, g.clone, "config", "core.sparseCheckout", "true")
	v = Evaluate(context.Background(), g.unit(), g.policy(t))
	if first, _ := v.First(); !strings.Contains(first.Evidence, "sparse checkout") {
		t.Fatalf("checks %+v; want the sparse checkout named", v.Checks)
	}
}

func TestCheckRepoKeepsACloneSwappedForASymlinkAfterTheWalk(t *testing.T) {
	f := newFixture(t)
	p := f.policy(t)
	w := walkInstance(context.Background(), f.instance, p.CloneURL)
	if len(w.clones) != 1 {
		t.Fatalf("walk found %+v", w)
	}
	// A clean clone elsewhere takes the walked clone's place.
	other := filepath.Join(t.TempDir(), "app")
	gitRun(t, t.TempDir(), "clone", "-q", f.remoteURL, other)
	if err := os.Rename(f.clone, f.clone+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, f.clone); err != nil {
		t.Fatal(err)
	}
	c := w.clones[0]
	res := p.Env.checkRepo(context.Background(), repoTarget{
		Rel: c.Rel, Base: f.instance, Dirs: repoDirs{GitDir: c.GitDir, WorkTree: c.WorkTree},
		URL: c.URL, wtInfo: c.wtInfo, gitInfo: c.gitInfo,
	}, repoPolicy{})
	if len(res.keeps) != 1 || !strings.Contains(res.keeps[0].Evidence, "changed since the walk") {
		t.Fatalf("keeps %+v; want the swapped clone kept", res.keeps)
	}
}

func TestCheckRepoKeepsADirectoryMadeUnreadableWithItsError(t *testing.T) {
	f := newFixture(t)
	locked := filepath.Join(f.clone, "locked")
	writeFile(t, filepath.Join(locked, "x"), "x")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	first, _ := v.First()
	if v.Kind != Keep || !strings.Contains(first.Evidence, "tools/app/locked") || !strings.Contains(first.Evidence, "permission denied") {
		t.Fatalf("verdict %s %+v; want the path and the read error", v.Kind, v.Checks)
	}
}

// TestLsRemoteReadsNoRepositoryWhereItRuns plants a repository whose
// configuration rewrites the clone URL to a local remote and names a
// URL-scoped credential helper, in the directory ls-remote runs from. Read,
// the rewrite would make the call succeed; it must fail as unreachable, and
// the helper must never run.
func TestLsRemoteReadsNoRepositoryWhereItRuns(t *testing.T) {
	f := newFixture(t)
	p := f.policy(t)
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	marker := filepath.Join(t.TempDir(), "helper-ran")
	helper := filepath.Join(t.TempDir(), "helper.sh")
	writeFile(t, helper, "#!/bin/sh\ntouch "+marker+"\n")
	if err := os.Chmod(helper, 0o755); err != nil {
		t.Fatal(err)
	}
	url := "https://example.invalid/app"
	gitRun(t, dir, "config", "url."+f.remoteURL+".insteadOf", url)
	gitRun(t, dir, "config", "credential."+url+".helper", helper)

	if _, err := p.Env.lsRemoteFrom(context.Background(), dir, url); err == nil {
		t.Fatal("ls-remote succeeded; the planted repository's configuration was read")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the planted credential helper ran")
	}
}

func TestScratchUseRefusesADirectoryThatIsNotEmpty(t *testing.T) {
	f := newFixture(t)
	p := f.policy(t)
	if err := os.Mkdir(filepath.Join(p.Env.Scratch.Dir, scratchNet, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Env.scratchDir(scratchNet); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("scratch use error %v; want a refusal", err)
	}
	if _, err := p.Env.liveTips(context.Background(), f.remoteURL); err == nil {
		t.Fatal("ls-remote ran from a scratch directory holding a .git")
	}
}
