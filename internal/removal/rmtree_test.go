package removal

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/tsukumogami/niwa/internal/removal/removaltest"
)

// wtFixture is an instance holding a clone at app and a linked worktree of
// it at wt, with deep/a/b/file.txt in the worktree.
type wtFixture struct {
	instance, clone, wt, admin string
}

func newWorktreeFixture(t *testing.T) wtFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := wtFixture{instance: filepath.Join(base, "inst")}
	f.clone = filepath.Join(f.instance, "app")
	f.wt = filepath.Join(f.instance, "wt")
	f.admin = filepath.Join(f.clone, ".git", "worktrees", "wt")
	gitRun(t, base, "init", "-q", "-b", "main", f.clone)
	writeFile(t, filepath.Join(f.clone, "README"), "hi\n")
	gitRun(t, f.clone, "add", "README")
	gitRun(t, f.clone, "commit", "-q", "-m", "init")
	gitRun(t, f.clone, "worktree", "add", "-q", f.wt, "-b", "wt-branch")
	writeFile(t, filepath.Join(f.wt, "deep", "a", "b", "file.txt"), "deep\n")
	return f
}

// wantStop checks err is a walk stop naming want, and whether it is a
// change.
func wantStop(t *testing.T, err error, want string, isChange bool) {
	t.Helper()
	var s *WalkStop
	if !errors.As(err, &s) {
		t.Fatalf("err = %v; want a walk stop naming %q", err, want)
	}
	if !strings.Contains(s.Reason, want) {
		t.Fatalf("stop %q; want it to name %q", s.Reason, want)
	}
	if WalkChanged(err) != isChange {
		t.Fatalf("stop %q: changed = %v, want %v", s.Reason, WalkChanged(err), isChange)
	}
}

// assertWorktreeKept checks nothing of the worktree or its admin directory
// was removed.
func (f wtFixture) assertKept(t *testing.T) {
	t.Helper()
	for _, p := range []string{
		filepath.Join(f.wt, ".git"),
		filepath.Join(f.wt, "README"),
		filepath.Join(f.admin, "HEAD"),
	} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
}

func TestRemoveWorktree_RemovesTheTreeThenItsAdminDirectory(t *testing.T) {
	f := newWorktreeFixture(t)
	if err := CheckWorktree(f.instance, f.wt, f.admin); err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := RemoveWorktree(f.instance, f.wt, f.admin); err != nil {
		t.Fatalf("remove: %v", err)
	}
	for _, p := range []string{f.wt, f.admin, filepath.Join(f.clone, ".git", "worktrees")} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is still there: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.clone, "README")); err != nil {
		t.Errorf("the clone lost a file: %v", err)
	}
	if out := gitRun(t, f.clone, "worktree", "list", "--porcelain"); strings.Contains(out, f.wt) {
		t.Errorf("git still lists the worktree:\n%s", out)
	}
}

func TestRemoveWorktree_KeepsWorktreesDirWithAnotherAdminDirectory(t *testing.T) {
	f := newWorktreeFixture(t)
	gitRun(t, f.clone, "worktree", "add", "-q", filepath.Join(f.instance, "wt2"), "-b", "wt2")
	if err := RemoveWorktree(f.instance, f.wt, f.admin); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.clone, ".git", "worktrees", "wt2", "HEAD")); err != nil {
		t.Fatalf("the other worktree's admin directory went: %v", err)
	}
}

func TestRemoveWorktree_RemovesALinkAsALink(t *testing.T) {
	f := newWorktreeFixture(t)
	out := removaltest.NewOutside(t)
	if err := os.Symlink(out.Dir, filepath.Join(f.wt, "deep", "to-outside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out.Marker, filepath.Join(f.wt, "marker-link")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorktree(f.instance, f.wt, f.admin); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Lstat(f.wt); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the worktree is still there: %v", err)
	}
	out.AssertUnchanged(t)
}

func TestRemoveWorktree_StopsAtAnotherDevice(t *testing.T) {
	f := newWorktreeFixture(t)
	var st unix.Stat_t
	if err := unix.Stat(filepath.Join(f.wt, "deep", "a"), &st); err != nil {
		t.Fatal(err)
	}
	prev := statDevice
	statDevice = func(s *unix.Stat_t) uint64 {
		if s.Ino == st.Ino && s.Dev == st.Dev {
			return uint64(s.Dev) + 1
		}
		return prev(s)
	}
	t.Cleanup(func() { statDevice = prev })
	wantStop(t, RemoveWorktree(f.instance, f.wt, f.admin), "another filesystem at wt/deep/a", false)
	f.assertKept(t)
}

func TestRemoveWorktree_StopsAtARepositoryBelowTheTop(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, f wtFixture)
		want  string
	}{
		{"a .git directory", func(t *testing.T, f wtFixture) {
			gitRun(t, f.wt, "init", "-q", filepath.Join(f.wt, "deep", "nested"))
		}, "a repository at wt/deep/nested/.git"},
		{"a .git file", func(t *testing.T, f wtFixture) {
			writeFile(t, filepath.Join(f.wt, "deep", ".git"), "gitdir: /elsewhere\n")
		}, "a repository at wt/deep/.git"},
		{"a git directory by its structure", func(t *testing.T, f wtFixture) {
			gitRun(t, f.wt, "init", "-q", "--bare", filepath.Join(f.wt, "deep", "store"))
		}, "a repository at wt/deep/store"},
		{"a git directory in the admin directory", func(t *testing.T, f wtFixture) {
			gitRun(t, f.wt, "init", "-q", "--bare", filepath.Join(f.admin, "store"))
		}, "a repository at app/.git/worktrees/wt/store"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newWorktreeFixture(t)
			c.plant(t, f)
			wantStop(t, RemoveWorktree(f.instance, f.wt, f.admin), c.want, false)
			f.assertKept(t)
			if _, err := os.Stat(filepath.Join(f.wt, "deep", "a", "b", "file.txt")); err != nil {
				t.Errorf("the walk removed part of the tree: %v", err)
			}
		})
	}
}

func TestRemoveWorktree_KeepsALockedWorktree(t *testing.T) {
	f := newWorktreeFixture(t)
	gitRun(t, f.clone, "worktree", "lock", f.wt)
	wantStop(t, RemoveWorktree(f.instance, f.wt, f.admin), "the worktree is locked: app/.git/worktrees/wt/locked", false)
	f.assertKept(t)
}

func TestRemoveWorktree_KeepsABadGitFile(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, f wtFixture)
		want  string
	}{
		{"a link", func(t *testing.T, f wtFixture) {
			dotGit := filepath.Join(f.wt, ".git")
			if err := os.Rename(dotGit, filepath.Join(f.instance, "dotgit")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(f.instance, "dotgit"), dotGit); err != nil {
				t.Fatal(err)
			}
		}, "unreadable git file: wt/.git"},
		{"another admin directory", func(t *testing.T, f wtFixture) {
			writeFile(t, filepath.Join(f.wt, ".git"), "gitdir: "+filepath.Join(f.clone, ".git")+"\n")
		}, "wt/.git doesn't name the worktree's admin directory"},
		{"two lines", func(t *testing.T, f wtFixture) {
			writeFile(t, filepath.Join(f.wt, ".git"), "gitdir: "+f.admin+"\nmore\n")
		}, "wt/.git doesn't name the worktree's admin directory"},
		{"a named pipe", func(t *testing.T, f wtFixture) {
			dotGit := filepath.Join(f.wt, ".git")
			if err := os.Remove(dotGit); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(dotGit, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "unreadable git file: wt/.git"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newWorktreeFixture(t)
			c.plant(t, f)
			wantStop(t, RemoveWorktree(f.instance, f.wt, f.admin), c.want, false)
			if _, err := os.Stat(filepath.Join(f.wt, "README")); err != nil {
				t.Errorf("the worktree lost a file: %v", err)
			}
			if _, err := os.Stat(filepath.Join(f.admin, "HEAD")); err != nil {
				t.Errorf("the admin directory lost a file: %v", err)
			}
		})
	}
}

// A directory swapped for a link to an outside directory between the walk's
// read of its type and its open stops the walk with the path named; nothing
// outside is touched. The deep entry, the worktree's own directory and its
// admin directory are each swapped.
func TestRemoveWorktree_AnOpenThatFailsStopsTheWalk(t *testing.T) {
	cases := []struct {
		name   string
		target func(f wtFixture) string
		want   string
	}{
		{"deep entry", func(f wtFixture) string { return filepath.Join(f.wt, "deep") }, "changed during the pass: wt/deep"},
		{"worktree directory", func(f wtFixture) string { return f.wt }, "changed during the pass: wt"},
		{"admin directory", func(f wtFixture) string { return f.admin }, "changed during the pass: app/.git/worktrees/wt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newWorktreeFixture(t)
			out := removaltest.NewOutside(t)
			swap := removaltest.NewSwap(c.target(f), out.Dir)
			t.Cleanup(SetBeforeOpenHook(swap.Hook))
			wantStop(t, RemoveWorktree(f.instance, f.wt, f.admin), c.want, true)
			swap.AssertFired(t)
			out.AssertUnchanged(t)
			aside := c.target(f) + ".swapped"
			if _, err := os.Stat(aside); err != nil {
				t.Errorf("the swapped-aside directory went: %v", err)
			}
		})
	}
}

// A swap the read of the tree doesn't see, made as the removal reaches the
// entry, stops the removal there: the rest of the tree and the admin
// directory stay, and nothing outside is touched.
func TestRemoveWorktree_ASwapDuringTheRemovalStopsIt(t *testing.T) {
	f := newWorktreeFixture(t)
	out := removaltest.NewOutside(t)
	deep := filepath.Join(f.wt, "deep")
	swap := removaltest.NewSwap(deep, out.Dir)
	opens := 0
	t.Cleanup(SetBeforeOpenHook(func(p string) {
		if p == deep {
			opens++
			if opens == 2 {
				swap.Hook(p)
			}
		}
	}))
	wantStop(t, RemoveWorktree(f.instance, f.wt, f.admin), "changed during the pass: wt/deep", true)
	swap.AssertFired(t)
	out.AssertUnchanged(t)
	for _, p := range []string{filepath.Join(f.wt, ".git"), filepath.Join(f.admin, "HEAD"), filepath.Join(deep+".swapped", "a", "b", "file.txt")} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
}

func TestRemoveWorktree_RefusesPathsOutsideTheInstance(t *testing.T) {
	f := newWorktreeFixture(t)
	wantStop(t, RemoveWorktree(f.instance, filepath.Dir(f.instance), f.admin), "not inside the instance", false)
	wantStop(t, RemoveWorktree(f.instance, f.wt, filepath.Dir(f.instance)), "not inside the instance", false)
}

// The walk runs on any tree from the instance's descriptor, stopping at a
// repository and at another device the same way.
func TestRemoveTreeAt_RemovesAnArbitraryTreeFromTheInstanceDescriptor(t *testing.T) {
	f := newWorktreeFixture(t)
	out := removaltest.NewOutside(t)
	tree := filepath.Join(f.instance, "build", "out")
	writeFile(t, filepath.Join(tree, "x", "y.txt"), "y\n")
	if err := os.Symlink(out.Dir, filepath.Join(tree, "link")); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(f.instance, instanceOpenFlags, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err := RemoveTreeAt(fd, f.instance, "build/out"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Lstat(tree); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the tree is still there: %v", err)
	}
	out.AssertUnchanged(t)
	wantStop(t, RemoveTreeAt(fd, f.instance, "app"), "a repository at app/.git", false)
	wantStop(t, RemoveTreeAt(fd, f.instance, "../inst"), "not a path below the instance", false)
}
