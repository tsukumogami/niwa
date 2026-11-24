package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The order of a reap's last steps: no Claude session is removed and no
// on-reap command runs until every keep check has passed, and each unforced
// worktree removal waits on a fresh read of its repository's configuration.

// laterKeepFixture is the pass fixture with its session listed, its job
// directory in place and an on-reap command that logs each run. It returns
// the fixture, the claude fake and the on-reap log.
func laterKeepFixture(t *testing.T) (passFixture, string, *fakeClaudeSessions, string) {
	t.Helper()
	f := newPassFixture(t)
	home := lockTestHome(t)
	passJobDir(t, f)
	log := filepath.Join(t.TempDir(), "on-reap.log")
	appendOnReapConfig(t, f.root, fmt.Sprintf(`command = "echo ran >> '%s'"`, log))
	fake := &fakeClaudeSessions{rows: []claudeSessionRow{passRow()}}
	installFakeClaudeSessions(t, fake)
	return f, home, fake, log
}

// assertNoSessionStep checks that no claude rm was made and no on-reap
// command ran.
func assertNoSessionStep(t *testing.T, fake *fakeClaudeSessions, log string) {
	t.Helper()
	if len(fake.removed) != 0 {
		t.Errorf("claude rm ran for a unit a later check kept: %q", fake.removed)
	}
	if b, err := os.ReadFile(log); err == nil {
		t.Errorf("the on-reap command ran for a unit a later check kept: %q", b)
	}
}

// A unit kept at the tombstone lock or at the tombstone check, both after
// the destroy is prepared, has had no session removed and no on-reap run.
func TestPassOrder_ALaterKeepRemovesNoSessionAndRunsNoOnReap(t *testing.T) {
	t.Run("tombstone lock held", func(t *testing.T) {
		f, home, fake, log := laterKeepFixture(t)
		release, err := workspace.TryFileLock(workspace.TombstoneLockPath(home, f.root), lockTestHolder)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(release)
		keptAtActing(t, f, "the tombstone lock is held")
		assertNoSessionStep(t, fake, log)
	})
	t.Run("tombstone check", func(t *testing.T) {
		f, _, fake, log := laterKeepFixture(t)
		// A file written once the pass takes the tombstone lock, after the
		// re-read and the worktree step: the check on the tombstone finds it.
		prev := tryTombstoneLockFunc
		tryTombstoneLockFunc = func(root string, shared bool) (func(), error) {
			if shared {
				if err := os.WriteFile(filepath.Join(f.clone, "late.txt"), []byte("late\n"), 0o644); err != nil {
					t.Error(err)
				}
			}
			return prev(root, shared)
		}
		t.Cleanup(func() { tryTombstoneLockFunc = prev })
		keptAtActing(t, f, "changed during the pass: tools/app/late.txt")
		assertNoSessionStep(t, fake, log)
	})
}

// A clean unit has its session removed and then its on-reap command run,
// once the instance is gone, with the instance's original path.
func TestPassOrder_OnReapRunsAfterTheRemovalWithTheOriginalPath(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	passJobDir(t, f)
	log := filepath.Join(t.TempDir(), "on-reap.log")
	appendOnReapConfig(t, f.root, fmt.Sprintf(`command = "test ! -e \"$NIWA_INSTANCE_PATH\" && echo \"$NIWA_INSTANCE_PATH\" >> '%s'"`, log))
	fake := &fakeClaudeSessions{rows: []claudeSessionRow{passRow()}}
	installFakeClaudeSessions(t, fake)
	removed := realRemoval(t)
	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reaped != 1 || len(*removed) != 1 {
		t.Fatalf("reaped %d, removed %v; want the unit removed\nstderr:\n%s", res.Reaped, *removed, f.stderr.String())
	}
	if strings.Join(fake.removed, ",") != passSessionID[:8] {
		t.Fatalf("claude rm calls %q; want one for %s", fake.removed, passSessionID[:8])
	}
	if b, err := os.ReadFile(log); err != nil || string(b) != f.inst+"\n" {
		t.Fatalf("on-reap log = %q (%v); want one run, after the removal, given %s\nstderr:\n%s", b, err, f.inst, f.stderr.String())
	}
}

// worktreeUnit adds a linked worktree at <instance>/wt on a pushed branch to
// the pass fixture's clone, and returns its path.
func worktreeUnit(t *testing.T, f passFixture) string {
	t.Helper()
	wt := filepath.Join(f.inst, "wt")
	passGit(t, f.clone, "checkout", "-q", "-B", "main", "origin/main")
	passGit(t, f.clone, "worktree", "add", "-q", wt, "-b", "wt-branch", "main")
	passGit(t, f.clone, "push", "-q", "origin", "wt-branch")
	return wt
}

// configPlants are the two ways a repository's configuration read keeps a
// worktree: a key that names a program, and a read that fails. Each plants
// its change in the clone the worktree belongs to.
var configPlants = []struct {
	name  string
	plant func(t *testing.T, clone string)
	want  string
}{
	{
		name:  "program key",
		plant: func(t *testing.T, clone string) { passGit(t, clone, "config", "filter.x.clean", "cat") },
		want:  "repository configuration names a program: filter.x.clean",
	},
	{
		name: "failed read",
		plant: func(t *testing.T, clone string) {
			if err := os.Chmod(filepath.Join(clone, ".git", "config"), 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(filepath.Join(clone, ".git", "config"), 0o644) })
		},
		want: "config",
	},
}

// A key that names a program, or a configuration read that fails, planted
// after the re-read keeps the unit before its worktree is removed: the
// worktree is still there, and no session was removed.
func TestPassOrder_AConfigurationReasonKeepsTheUnitBeforeItsWorktreeGoes(t *testing.T) {
	for _, c := range configPlants {
		t.Run(c.name, func(t *testing.T) {
			f := newPassFixture(t)
			lockTestHome(t)
			wt := worktreeUnit(t, f)
			passJobDir(t, f)
			fake := &fakeClaudeSessions{rows: []claudeSessionRow{passRow()}}
			installFakeClaudeSessions(t, fake)
			// The pass's one listing is taken at the session step, after the
			// re-read and before the worktree step.
			list := listClaudeSessionsFunc
			listClaudeSessionsFunc = func(ctx context.Context) ([]claudeSessionRow, error) {
				c.plant(t, f.clone)
				return list(ctx)
			}
			removed := realRemoval(t)
			res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			if res.Reaped != 0 || len(*removed) != 0 {
				t.Fatalf("reaped %d, removed %v; want the unit kept\nstderr:\n%s", res.Reaped, *removed, f.stderr.String())
			}
			if len(res.Kept) != 1 || !strings.Contains(res.Kept[0].Reason, c.want) {
				t.Fatalf("kept %+v; want one unit kept with %q\nstderr:\n%s", res.Kept, c.want, f.stderr.String())
			}
			if len(fake.removed) != 0 {
				t.Errorf("claude rm ran for a unit its worktree's configuration kept: %q", fake.removed)
			}
			if _, err := os.Stat(filepath.Join(wt, "README")); err != nil {
				t.Errorf("the worktree is gone: %v", err)
			}
			if _, err := os.Stat(filepath.Join(f.inst, ".niwa", "instance.json")); err != nil {
				t.Errorf("the instance isn't at its own path: %v", err)
			}
			assertNoTombstone(t, f.root)
		})
	}
}

// destroyWorktreeInstance is a workspace with instance alpha holding a clone
// of a pushed repository at alpha/app and a linked worktree of it at
// alpha/wt on a pushed branch. It returns the root, the clone and the
// worktree.
func destroyWorktreeInstance(t *testing.T) (string, string, string) {
	t.Helper()
	root := destroyTestSetup(t, []string{"alpha"})
	remote := filepath.Join(canonicalTempDir(t), "app.git")
	passGit(t, root, "init", "-q", "--bare", "-b", "main", remote)
	seed := filepath.Join(canonicalTempDir(t), "seed")
	passGit(t, root, "init", "-q", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	passGit(t, seed, "add", "README")
	passGit(t, seed, "commit", "-q", "-m", "init")
	passGit(t, seed, "push", "-q", remote, "main")
	clone := filepath.Join(root, "alpha", "app")
	passGit(t, root, "clone", "-q", remote, clone)
	wt := filepath.Join(root, "alpha", "wt")
	passGit(t, clone, "worktree", "add", "-q", wt, "-b", "wt-branch", "main")
	passGit(t, clone, "push", "-q", "-u", "origin", "wt-branch")
	return root, clone, wt
}

// An unforced destroy refuses, with the reason named, when the configuration
// read before a worktree's removal names a program or fails; the instance
// and the worktree are both still there. The scan reads the repositories
// with git of its own before that read, so the failed read here is the
// removal environment's: its filtered PATH holds no git, which fails the
// read and leaves the scan alone.
func TestDestroyOrder_AConfigurationReasonStopsTheDestroyBeforeAWorktreeGoes(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, clone string)
		want  string
	}{
		configPlants[0],
		{
			name: "failed read",
			plant: func(t *testing.T, clone string) {
				prev := newRemovalEnv
				newRemovalEnv = func(r, home, pathEnv string) *removal.Env {
					return removal.NewEnv(r, home, "", removal.WithFileRemotes())
				}
				t.Cleanup(func() { newRemovalEnv = prev })
			},
			want: "no git on the filtered PATH",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, clone, wt := destroyWorktreeInstance(t)
			c.plant(t, clone)
			chdirTo(t, root)
			quietDestroyCmd()
			setDestroyFlags(t, false, false, "")
			err := runDestroy(destroyCmd, []string{"alpha"})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("destroy = %v; want it refused with %q", err, c.want)
			}
			if _, err := os.Stat(filepath.Join(wt, "README")); err != nil {
				t.Errorf("the worktree is gone: %v", err)
			}
			assertInstancesExist(t, root, "alpha")
		})
	}
}
