package removal

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// outsideDir is a scratch directory outside every fixture's instance, with
// its links resolved, so a path git prints matches it.
func outsideDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// commitRepo makes a repository at dir holding one commit nobody pushed.
func commitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "commit", "-q", "--allow-empty", "-m", "work nobody pushed")
}

// keepWith returns the first keeping check whose evidence holds every one
// of want, failing the test when none does.
func keepWith(t *testing.T, v Verdict, want ...string) Check {
	t.Helper()
	for _, c := range v.Checks {
		ok := true
		for _, w := range want {
			if !strings.Contains(c.Evidence, w) {
				ok = false
				break
			}
		}
		if ok {
			return c
		}
	}
	t.Fatalf("verdict %s; no keeping check holds %q in %+v", v.Kind, want, v.Checks)
	return Check{}
}

func noKeepWith(t *testing.T, v Verdict, text string) {
	t.Helper()
	for _, c := range v.Checks {
		if strings.Contains(c.Evidence, text) {
			t.Errorf("keeping check %+v holds %q", c, text)
		}
	}
}

func TestWalkKeepsAGitIgnoredNestedRepository(t *testing.T) {
	f := checkedOut(t)
	writeFile(t, filepath.Join(f.clone, ".gitignore"), "vendor/\n")
	gitRun(t, f.clone, "add", ".gitignore")
	gitRun(t, f.clone, "commit", "-q", "-m", "ignore vendor")
	gitRun(t, f.clone, "push", "-q", "origin", "HEAD")
	commitRepo(t, filepath.Join(f.clone, "vendor", "lib"))

	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	c := keepWith(t, v, "tools/app/vendor/lib: ", "no clone URL for this repository")
	if c.Name != CheckRepository {
		t.Errorf("kept by %s; want the repository check", c.Name)
	}
	noKeepWith(t, v, "git-ignored")
}

func TestWalkKeepsADeinitializedSubmoduleStore(t *testing.T) {
	f := checkedOut(t)
	src := filepath.Join(outsideDir(t), "sub")
	commitRepo(t, src)
	store := filepath.Join(f.clone, ".git", "modules", "sub")
	gitRun(t, f.clone, "clone", "-q", "--bare", src, store)
	gitRun(t, f.clone, "--git-dir="+store, "config", "core.bare", "false")
	gitRun(t, f.clone, "--git-dir="+store, "config", "core.worktree", "../../../sub")
	if err := os.MkdirAll(filepath.Join(f.clone, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	keepWith(t, v, "tools/app/.git/modules/sub: ", "no clone URL for this repository")
}

func TestWalkKeepsABareRepositoryHoldingACommit(t *testing.T) {
	f := newFixture(t)
	src := filepath.Join(outsideDir(t), "src")
	commitRepo(t, src)
	gitRun(t, f.instance, "clone", "-q", "--bare", src, filepath.Join(f.instance, "keep.git"))

	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	keepWith(t, v, "keep.git: ", "no clone URL for this repository")
	if _, ok := classOf(v, "keep.git/HEAD"); ok {
		t.Errorf("the bare repository's files were classed one by one: %+v", v.Paths)
	}
}

func TestWalkKeepsARepositoryInsideABuildTree(t *testing.T) {
	f := checkedOut(t)
	writeFile(t, filepath.Join(f.clone, ".git", "info", "exclude"), "target/\n")
	writeFile(t, filepath.Join(f.clone, "target", "CACHEDIR.TAG"), cacheDirSignature+"\n")
	commitRepo(t, filepath.Join(f.clone, "target", "dep"))

	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	keepWith(t, v, "tools/app/target/dep: ", "no clone URL for this repository")
	wantClass(t, v, "tools/app/target/", ClassBuildTree, "")
}

func TestWalkJudgesALinkedWorktreeInsideTheInstanceWithTheClonesURL(t *testing.T) {
	f := checkedOut(t)
	wt := filepath.Join(f.instance, "wt")
	gitRun(t, f.clone, "worktree", "add", "-q", "--detach", wt)
	if v := Evaluate(context.Background(), f.unit(), f.policy(t)); v.Kind != Reap {
		t.Fatalf("verdict %s %+v; want a clean linked worktree reaped", v.Kind, v.Checks)
	}

	gitRun(t, wt, "checkout", "-q", "-b", "wt-work")
	gitRun(t, wt, "commit", "-q", "--allow-empty", "-m", "worktree work")
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	keepWith(t, v, "commits the clone URL does not show", "branch wt-work")
	keepWith(t, v, "HEAD of worktree wt")
	noKeepWith(t, v, "no clone URL")
	noKeepWith(t, v, "outside the instance")
}

func TestWalkJudgesARepositoryInTheJobDirectory(t *testing.T) {
	f := newFixture(t)
	jobs := filepath.Join(outsideDir(t), "jobs")
	u := f.unit()
	u.JobDir = filepath.Join(jobs, "11111111")
	p := f.policy(t)
	p.JobsRoot = jobs

	if err := os.MkdirAll(filepath.Join(u.JobDir, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, filepath.Join(u.JobDir, "scratch"), "init", "-q")
	if v := Evaluate(context.Background(), u, p); v.Kind != Reap {
		t.Fatalf("verdict %s %+v; want an empty repository in the job directory reaped", v.Kind, v.Checks)
	}
	gitRun(t, filepath.Join(u.JobDir, "scratch"), "commit", "-q", "--allow-empty", "-m", "job work")
	v := Evaluate(context.Background(), u, p)
	c := keepWith(t, v, "job:scratch: ", "no clone URL for this repository")
	if c.Name != CheckRepository || len(c.Paths) != 1 || c.Paths[0] != "job:scratch" {
		t.Errorf("keep %+v; want the repository check naming job:scratch", c)
	}
	noKeepWith(t, v, "job scratch counted as unique")
}

func TestWalkNeverFollowsASymlinkOutOfTheInstance(t *testing.T) {
	f := newFixture(t)
	target := filepath.Join(outsideDir(t), "secret.txt")
	writeFile(t, target, "SECRET-CONTENT "+strings.Repeat("x", 4096))
	if err := os.Symlink(target, filepath.Join(f.instance, "link")); err != nil {
		t.Fatal(err)
	}
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	wantClass(t, v, "link", ClassUnique, "")
	for _, c := range v.Checks {
		if strings.Contains(c.Evidence, "SECRET") || strings.Contains(c.Evidence, "4096") || strings.Contains(c.Evidence, "4111") {
			t.Errorf("evidence %q reads the link's target", c.Evidence)
		}
	}
}

func TestWalkKeepsADirectoryOnAnotherFilesystem(t *testing.T) {
	f := newFixture(t)
	mnt := filepath.Join(f.instance, "mnt")
	writeFile(t, filepath.Join(mnt, "inside.txt"), "x")
	commitRepo(t, filepath.Join(mnt, "repo"))
	jobs := filepath.Join(outsideDir(t), "jobs")
	u := f.unit()
	u.JobDir = filepath.Join(jobs, "22222222")
	writeFile(t, filepath.Join(u.JobDir, "notes.txt"), "n")
	p := f.policy(t)
	p.JobsRoot = jobs

	prev := walkDevice
	t.Cleanup(func() { walkDevice = prev })
	other := map[string]bool{mnt: true}
	walkDevice = func(path string, info fs.FileInfo) (uint64, bool) {
		d, ok := prev(path, info)
		if other[path] {
			d++
		}
		return d, ok
	}
	v := Evaluate(context.Background(), u, p)
	keepWith(t, v, "another filesystem at mnt")
	noKeepWith(t, v, "mnt/repo")
	noKeepWith(t, v, "inside.txt")
	noKeepWith(t, v, "another filesystem at job:")

	// The job directory is compared with the jobs root, not the instance.
	other = map[string]bool{jobs: true}
	v = Evaluate(context.Background(), u, p)
	keepWith(t, v, "another filesystem at job:")
	noKeepWith(t, v, "notes.txt")
}

func TestWalkKeepsAnExternalGitDirectory(t *testing.T) {
	f := newFixture(t)
	outside := outsideDir(t)
	commitRepo(t, filepath.Join(outside, "repo"))

	// A .git file naming a directory outside the instance.
	writeFile(t, filepath.Join(f.instance, "a", ".git"), "gitdir: "+filepath.Join(outside, "repo", ".git")+"\n")
	// A relative one climbing out of it.
	up, err := filepath.Rel(filepath.Join(f.instance, "b"), filepath.Join(outside, "repo", ".git"))
	if err != nil || !strings.HasPrefix(up, "..") {
		t.Fatalf("relative path %q, %v", up, err)
	}
	writeFile(t, filepath.Join(f.instance, "b", ".git"), "gitdir: "+up+"\n")
	// One reached through a link inside the instance.
	if err := os.Symlink(filepath.Join(outside, "repo"), filepath.Join(f.instance, "lnk")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.instance, "c", ".git"), "gitdir: ../lnk/.git\n")
	// An admin directory inside the instance whose commondir is outside.
	admin := filepath.Join(f.instance, "fake", "worktrees", "d")
	writeFile(t, filepath.Join(admin, "commondir"), filepath.Join(outside, "repo", ".git")+"\n")
	writeFile(t, filepath.Join(admin, "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(f.instance, "d", ".git"), "gitdir: "+admin+"\n")

	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	keepWith(t, v, "external git directory: "+filepath.Join(outside, "repo", ".git"))
	for _, label := range []string{"a/.git", "b/.git", "d/.git"} {
		found := false
		for _, k := range v.Checks {
			if len(k.Paths) == 1 && k.Paths[0] == label && strings.Contains(k.Evidence, "external git directory: ") {
				found = true
			}
		}
		if !found {
			t.Errorf("no external git directory keep names %s: %+v", label, v.Checks)
		}
	}
	keepWith(t, v, "external git directory: "+filepath.Join(f.instance, "lnk", ".git"), "symlink")
	for _, c := range v.Checks {
		if strings.Contains(c.Evidence, "no clone URL") {
			t.Errorf("an external git directory was judged: %+v", c)
		}
	}
}

func TestWalkKeepsAnUnreadableGitFile(t *testing.T) {
	f := checkedOut(t)
	writeFile(t, filepath.Join(f.instance, "a", ".git"), "gitdir: x\ngitdir: y\n")
	writeFile(t, filepath.Join(f.instance, "b", ".git"), "not a git file\n")
	if err := os.Symlink("/nowhere", filepath.Join(f.instance, "c-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.instance, "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.clone, ".git"), filepath.Join(f.instance, "c", ".git")); err != nil {
		t.Fatal(err)
	}
	// A commondir that isn't one line.
	admin := filepath.Join(f.instance, "fake", "worktrees", "d")
	writeFile(t, filepath.Join(admin, "commondir"), "../..\n../..\n")
	writeFile(t, filepath.Join(f.instance, "d", ".git"), "gitdir: "+admin+"\n")

	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	keepWith(t, v, "unreadable git file: a/.git")
	keepWith(t, v, "unreadable git file: b/.git")
	keepWith(t, v, "unreadable git file: c/.git", "symlink")
	keepWith(t, v, "unreadable git file: fake/worktrees/d/commondir")
}

// worktreeOutside adds a linked worktree of the fixture's clone outside the
// instance, detached at the pushed commit, and returns its path.
func worktreeOutside(t *testing.T, f fixture) string {
	t.Helper()
	wt := filepath.Join(outsideDir(t), "wt")
	gitRun(t, f.clone, "worktree", "add", "-q", "--detach", wt)
	return wt
}

func TestWalkKeepsAWorktreeOutsideTheInstance(t *testing.T) {
	f := checkedOut(t)
	pushed := gitRun(t, f.clone, "rev-parse", "HEAD")
	wt := worktreeOutside(t, f)
	gitRun(t, wt, "commit", "-q", "--allow-empty", "-m", "outside work")
	gitRun(t, wt, "update-ref", "refs/worktree/saved", "HEAD")
	gitRun(t, wt, "reset", "-q", "--hard", pushed)
	writeFile(t, filepath.Join(wt, "staged.txt"), "s")
	gitRun(t, wt, "add", "staged.txt")

	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	c := keepWith(t, v, "worktree outside the instance: "+wt)
	if c.Name != CheckRepository {
		t.Errorf("kept by %s; want the repository check", c.Name)
	}
}

func TestWalkKeepsAnAdminDirectoryGitDoesNotReport(t *testing.T) {
	f := checkedOut(t)
	elsewhere := outsideDir(t)
	writeFile(t, filepath.Join(elsewhere, ".git"), "gitdir: "+filepath.Join(f.clone, ".git", "worktrees", "ghost")+"\n")
	writeFile(t, filepath.Join(f.clone, ".git", "worktrees", "ghost", "gitdir"), filepath.Join(elsewhere, ".git")+"\n")

	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	keepWith(t, v, "worktree outside the instance: "+elsewhere)
}

func TestWalkKeepsAnAdminDirectoryWithAnUnreadableGitdir(t *testing.T) {
	f := checkedOut(t)
	wt := worktreeOutside(t, f)
	admin := filepath.Join(f.clone, ".git", "worktrees", filepath.Base(wt))
	writeFile(t, filepath.Join(admin, "gitdir"), filepath.Join(wt, ".git")+"\nHEAD "+strings.Repeat("a", 40)+"\n")

	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	keepWith(t, v, "unreadable git file: tools/app/.git/worktrees/"+filepath.Base(wt)+"/gitdir")
}

func TestWalkReachesTheMainEntryOnlyAtTheWalkedWorkTree(t *testing.T) {
	f := checkedOut(t)
	other := filepath.Join(f.instance, "elsewhere")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, f.clone, "config", "core.worktree", other)
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	keepWith(t, v, "tools/app: configured work tree is not the one the walk found: elsewhere")
}

func TestWalkKeepsAWorkTreeOutsideTheInstance(t *testing.T) {
	t.Run("separate git dir", func(t *testing.T) {
		f := newFixture(t)
		wt := filepath.Join(outsideDir(t), "wt")
		sep := filepath.Join(f.instance, "sep.git")
		gitRun(t, f.instance, "init", "-q", "--separate-git-dir", sep, wt)
		gitRun(t, wt, "commit", "-q", "--allow-empty", "-m", "outside work")
		v := Evaluate(context.Background(), f.unit(), f.policy(t))
		keepWith(t, v, "sep.git: work tree outside the instance: "+sep)
		noKeepWith(t, v, "no clone URL")
	})
	t.Run("core.worktree", func(t *testing.T) {
		f := newFixture(t)
		src := filepath.Join(outsideDir(t), "src")
		commitRepo(t, src)
		repo := filepath.Join(f.instance, "cfg.git")
		gitRun(t, f.instance, "clone", "-q", "--bare", src, repo)
		wt := outsideDir(t)
		gitRun(t, f.instance, "--git-dir="+repo, "config", "core.bare", "false")
		gitRun(t, f.instance, "--git-dir="+repo, "config", "core.worktree", wt)
		v := Evaluate(context.Background(), f.unit(), f.policy(t))
		keepWith(t, v, "cfg.git: work tree outside the instance: "+wt)
	})
}

// prunableWorktree adds a linked worktree inside the instance and deletes
// its working tree, leaving its admin directory, and returns the admin
// directory. before runs in the working tree first.
func prunableWorktree(t *testing.T, f fixture, before func(wt string)) string {
	t.Helper()
	wt := filepath.Join(f.instance, "gone")
	gitRun(t, f.clone, "worktree", "add", "-q", "--detach", wt)
	if before != nil {
		before(wt)
	}
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(f.clone, ".git", "worktrees", "gone")
}

func TestWalkJudgesAPrunableWorktree(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		f := checkedOut(t)
		admin := prunableWorktree(t, f, nil)
		if v := Evaluate(context.Background(), f.unit(), f.policy(t)); v.Kind != Reap {
			t.Fatalf("verdict %s %+v; want a clean prunable worktree reaped", v.Kind, v.Checks)
		}
		w := walkUnit(context.Background(), f.unit(), f.policy(t))
		found := false
		for _, d := range w.Repositories() {
			if d.GitDir == admin && d.WorkTree == "" {
				found = true
			}
		}
		if !found {
			t.Errorf("repositories %+v; want the admin directory with no work tree", w.Repositories())
		}
	})
	t.Run("bisect ref", func(t *testing.T) {
		f := checkedOut(t)
		pushed := gitRun(t, f.clone, "rev-parse", "HEAD")
		prunableWorktree(t, f, func(wt string) {
			gitRun(t, wt, "commit", "-q", "--allow-empty", "-m", "bisect work")
			gitRun(t, wt, "update-ref", "refs/bisect/bad", "HEAD")
			gitRun(t, wt, "reset", "-q", "--hard", pushed)
		})
		v := Evaluate(context.Background(), f.unit(), f.policy(t))
		keepWith(t, v, "tools/app/.git/worktrees/gone: ", "refs/bisect/bad")
	})
	t.Run("staged", func(t *testing.T) {
		f := checkedOut(t)
		prunableWorktree(t, f, func(wt string) {
			writeFile(t, filepath.Join(wt, "staged.txt"), "s")
			gitRun(t, wt, "add", "staged.txt")
		})
		v := Evaluate(context.Background(), f.unit(), f.policy(t))
		keepWith(t, v, "staged changes in a worktree whose working tree is gone: staged.txt")
	})
}

func TestWalkANewlineInGitdirDoesNotHideAnUnpushedHead(t *testing.T) {
	f := checkedOut(t)
	pushed := gitRun(t, f.clone, "rev-parse", "HEAD")
	wt := filepath.Join(f.instance, "wt")
	gitRun(t, f.clone, "worktree", "add", "-q", "--detach", wt)
	gitRun(t, wt, "commit", "-q", "--allow-empty", "-m", "detached work")
	gitdir := filepath.Join(f.clone, ".git", "worktrees", "wt", "gitdir")
	writeFile(t, gitdir, filepath.Join(wt, ".git")+"\nHEAD "+pushed+"\n")

	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	if v.Kind != Keep {
		t.Fatalf("verdict %s; want keep", v.Kind)
	}
	keepWith(t, v, "commits the clone URL does not show", "HEAD")
}

func TestWalkListsEveryRepositoryItFound(t *testing.T) {
	f := checkedOut(t)
	gitRun(t, f.instance, "init", "-q", "--bare", filepath.Join(f.instance, "b.git"))
	wt := filepath.Join(f.instance, "wt")
	gitRun(t, f.clone, "worktree", "add", "-q", "--detach", wt)
	w := walkUnit(context.Background(), f.unit(), f.policy(t))
	want := map[repoDirs]bool{
		{GitDir: filepath.Join(f.clone, ".git"), WorkTree: f.clone}:               false,
		{GitDir: filepath.Join(f.instance, "b.git")}:                              false,
		{GitDir: filepath.Join(f.clone, ".git", "worktrees", "wt"), WorkTree: wt}: false,
	}
	for _, d := range w.Repositories() {
		if _, ok := want[d]; !ok {
			t.Errorf("unexpected repository %+v", d)
		}
		want[d] = true
	}
	for d, seen := range want {
		if !seen {
			t.Errorf("repository %+v not listed in %+v", d, w.Repositories())
		}
	}
}
