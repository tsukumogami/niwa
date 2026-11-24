package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/removal/removaltest"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// A reap decides first and removes after: every check that can keep a unit
// holding a linked worktree runs before the first worktree is removed, and
// the worktrees are removed inside the tombstone by the no-follow walk alone,
// after the check on the tombstone, the last configuration read and the last
// full job-directory vet.

// decideFixture is the pass fixture with its session listed, its job
// directory in place, an on-reap command that logs each run, and a linked
// worktree at <instance>/wt on a pushed branch. It returns the fixture, the
// home, the claude fake and the on-reap log.
func decideFixture(t *testing.T) (passFixture, string, *fakeClaudeSessions, string) {
	t.Helper()
	f, home, fake, log := laterKeepFixture(t)
	worktreeUnit(t, f)
	return f, home, fake, log
}

// assertWorktreeIntact checks the worktree at <instance>/wt, its .git file
// and its admin directory are all still there.
func assertWorktreeIntact(t *testing.T, f passFixture) {
	t.Helper()
	for _, p := range []string{
		filepath.Join(f.inst, "wt", "README"),
		filepath.Join(f.inst, "wt", ".git"),
		filepath.Join(f.clone, ".git", "worktrees", "wt"),
	} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("the worktree isn't intact for a unit a check kept: %v", err)
		}
	}
}

// onListing calls hook with the call's number, from 1, before each claude
// agents listing the pass makes, then lists as before.
func onListing(t *testing.T, hook func(call int)) {
	t.Helper()
	prev := listClaudeSessionsFunc
	calls := 0
	listClaudeSessionsFunc = func(ctx context.Context) ([]claudeSessionRow, error) {
		calls++
		hook(calls)
		return prev(ctx)
	}
	t.Cleanup(func() { listClaudeSessionsFunc = prev })
}

// onSharedTombstoneLock calls hook right before the pass takes the
// tombstone lock shared, the step before the rename.
func onSharedTombstoneLock(t *testing.T, hook func()) {
	t.Helper()
	prev := tryTombstoneLockFunc
	tryTombstoneLockFunc = func(root string, shared bool) (func(), error) {
		if shared {
			hook()
		}
		return prev(root, shared)
	}
	t.Cleanup(func() { tryTombstoneLockFunc = prev })
}

// realGit is the git on PATH with its links resolved.
func realGit(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	if real, err = filepath.EvalSymlinks(real); err != nil {
		t.Fatal(err)
	}
	return real
}

// plantAtLastTombstoneRead puts a git on PATH that, on the stash listing of
// the worktree at <tombstone>/wt (the last git call the check on the
// tombstone makes), plants filter.x.clean in the repository's configuration
// before it runs the call. Only a configuration read after the check on the
// tombstone can see the key.
func plantAtLastTombstoneRead(t *testing.T) {
	t.Helper()
	real := realGit(t)
	dir := canonicalTempDir(t)
	script := fmt.Sprintf(`#!/bin/sh
wt=""
gd=""
stash=""
for a in "$@"; do
	case "$a" in
	--work-tree=*) wt="${a#--work-tree=}" ;;
	--git-dir=*) gd="${a#--git-dir=}" ;;
	stash) stash=1 ;;
	esac
done
case "$wt" in
*/%s*/wt)
	if [ -n "$stash" ]; then
		'%s' --git-dir="$gd" config filter.x.clean cat
	fi
	;;
esac
exec '%s' "$@"
`, workspace.TombstonePrefix, real, real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A keep at each check from the destroy's preparation to the last full
// job-directory vet leaves the unit's worktree intact, the instance at its
// own path (renamed back once it was renamed), no Claude session removed
// and no on-reap command run.
func TestDecideFirst_AKeepAtEachCheckLeavesEveryWorktreeIntact(t *testing.T) {
	t.Run("destroy preparation", func(t *testing.T) {
		f, _, fake, log := decideFixture(t)
		// A workspace file planted at the pass's listing, after the re-read:
		// the destroy's preparation refuses an instance that holds one.
		onListing(t, func(int) {
			writeTestFile(t, filepath.Join(f.inst, ".niwa", "workspace.toml"), "[workspace]\nname = \"planted\"\n")
		})
		keptAtActing(t, f, "the destroy was refused")
		assertWorktreeIntact(t, f)
		assertNoSessionStep(t, fake, log)
	})
	t.Run("tombstone lock", func(t *testing.T) {
		f, home, fake, log := decideFixture(t)
		release, err := workspace.TryFileLock(workspace.TombstoneLockPath(home, f.root), lockTestHolder)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(release)
		keptAtActing(t, f, "the tombstone lock is held")
		assertWorktreeIntact(t, f)
		assertNoSessionStep(t, fake, log)
	})
	t.Run("rename", func(t *testing.T) {
		f, _, fake, log := decideFixture(t)
		// The workspace root made read-only right before the rename, so the
		// rename fails.
		onSharedTombstoneLock(t, func() {
			if err := os.Chmod(f.root, 0o555); err != nil {
				t.Error(err)
			}
		})
		t.Cleanup(func() { _ = os.Chmod(f.root, 0o755) })
		keptAtActing(t, f, "renaming it to its tombstone failed")
		assertWorktreeIntact(t, f)
		assertNoSessionStep(t, fake, log)
	})
	t.Run("tombstone check", func(t *testing.T) {
		f, _, fake, log := decideFixture(t)
		onSharedTombstoneLock(t, func() {
			writeTestFile(t, filepath.Join(f.clone, "late.txt"), "late\n")
		})
		keptAtActing(t, f, "changed during the pass: tools/app/late.txt")
		assertWorktreeIntact(t, f)
		assertNoSessionStep(t, fake, log)
	})
	t.Run("final configuration read", func(t *testing.T) {
		f, _, fake, log := decideFixture(t)
		plantAtLastTombstoneRead(t)
		keptAtActing(t, f, "repository configuration names a program: filter.x.clean")
		assertWorktreeIntact(t, f)
		assertNoSessionStep(t, fake, log)
	})
	t.Run("last full vet: a session now running", func(t *testing.T) {
		f, _, fake, log := decideFixture(t)
		onListing(t, func(call int) {
			if call >= 2 {
				row := passRow()
				row.PID = 4242
				fake.rows = []claudeSessionRow{row}
			}
		})
		keptAtActing(t, f, "Claude session "+passSessionID[:8]+" is still running")
		assertWorktreeIntact(t, f)
		assertNoSessionStep(t, fake, log)
	})
	t.Run("last full vet: a session newly listed", func(t *testing.T) {
		f, _, fake, log := decideFixture(t)
		// The pass's first listing doesn't hold the unit's session; the
		// listing after the final configuration read does.
		fake.rows = nil
		onListing(t, func(call int) {
			if call >= 2 {
				fake.rows = []claudeSessionRow{passRow()}
			}
		})
		keptAtActing(t, f, "newly listed")
		assertWorktreeIntact(t, f)
		assertNoSessionStep(t, fake, log)
	})
	t.Run("last full vet: a job directory swapped for a link", func(t *testing.T) {
		f, _, fake, log := decideFixture(t)
		out := removaltest.NewOutside(t)
		jobDir := filepath.Join(f.jobsDir, passSessionID[:8])
		onListing(t, func(call int) {
			if call == 2 {
				if err := os.Rename(jobDir, jobDir+".aside"); err != nil {
					t.Error(err)
				}
				if err := os.Symlink(out.Dir, jobDir); err != nil {
					t.Error(err)
				}
			}
		})
		keptAtActing(t, f, "failed its check")
		assertWorktreeIntact(t, f)
		assertNoSessionStep(t, fake, log)
		out.AssertUnchanged(t)
	})
}

// A per-call re-listing or job-directory vet that fails right before claude
// rm, after the worktree was removed inside the tombstone, keeps the unit:
// what is left is renamed back, no tombstone is left, no on-reap command
// runs, and a directory outside the tombstone is unchanged.
func TestDecideFirst_APerCallFailureAfterTheWorktreeRemovalsRenamesBack(t *testing.T) {
	cases := []struct {
		name string
		fail func(t *testing.T, fake *fakeClaudeSessions, jobDir string, out *removaltest.Outside)
		want string
	}{
		{"re-listing", func(t *testing.T, fake *fakeClaudeSessions, _ string, _ *removaltest.Outside) {
			row := passRow()
			row.PID = 4242
			fake.rows = []claudeSessionRow{row}
		}, "is still running right before claude rm"},
		{"job-directory vet", func(t *testing.T, _ *fakeClaudeSessions, jobDir string, out *removaltest.Outside) {
			if err := os.Rename(jobDir, jobDir+".aside"); err != nil {
				t.Error(err)
			}
			if err := os.Symlink(out.Dir, jobDir); err != nil {
				t.Error(err)
			}
		}, "was not removed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, _, fake, log := decideFixture(t)
			out := removaltest.NewOutside(t)
			jobDir := filepath.Join(f.jobsDir, passSessionID[:8])
			var before string
			onSharedTombstoneLock(t, func() {
				if !exists(filepath.Join(f.inst, "wt", "README")) {
					before = "the worktree was removed before the rename, outside the tombstone"
				}
			})
			var atRm string
			calls := 0
			prevRemove := removeClaudeSessionFunc
			removeClaudeSessionFunc = func(ctx context.Context, id string) (string, error) {
				calls++
				tombs, err := workspace.ListTombstones(f.root)
				switch {
				case err != nil || len(tombs) != 1:
					atRm = fmt.Sprintf("tombstones %v (%v) at claude rm; want one", tombs, err)
				case exists(filepath.Join(tombs[0], "wt")) || exists(filepath.Join(tombs[0], "tools", "app", ".git", "worktrees", "wt")):
					atRm = "the worktree is still in the tombstone at claude rm"
				}
				c.fail(t, fake, jobDir, out)
				return realRemoveClaudeSession(ctx, id)
			}
			t.Cleanup(func() { removeClaudeSessionFunc = prevRemove })

			keptAtActing(t, f, c.want)
			if before != "" {
				t.Error(before)
			}
			if atRm != "" {
				t.Error(atRm)
			}
			if calls != 1 {
				t.Errorf("claude rm calls %d; want one", calls)
			}
			if exists(filepath.Join(f.inst, "wt")) || exists(filepath.Join(f.clone, ".git", "worktrees", "wt")) {
				t.Error("the worktree removed inside the tombstone is back after the rename back")
			}
			if b, err := os.ReadFile(log); err == nil {
				t.Errorf("the on-reap command ran for a kept unit: %q", b)
			}
			out.AssertUnchanged(t)
		})
	}
}

// tombstonePathPart is what every path inside a tombstone holds.
var tombstonePathPart = string(filepath.Separator) + workspace.TombstonePrefix

// The worktree's removal inside the tombstone runs no git: a wrapper on
// PATH logs no call from the walk's first open in the tombstone to the
// tombstone's removal, and a directory outside the tombstone is unchanged.
func TestDecideFirst_TheWalkInsideTheTombstoneCallsNoGit(t *testing.T) {
	f, fake := swapPassUnit(t)
	log := gitCallLog(t)
	out := removaltest.NewOutside(t)
	lines := func() int {
		b, _ := os.ReadFile(log)
		return strings.Count(string(b), "\n")
	}
	start, end := -1, -1
	t.Cleanup(removal.SetBeforeOpenHook(func(p string) {
		if start < 0 && strings.Contains(p, tombstonePathPart) {
			start = lines()
		}
	}))
	prev := removeTombstoneFunc
	removeTombstoneFunc = func(p *workspace.DestroyPlan, tomb string) error {
		end = lines()
		return p.Remove(tomb)
	}
	t.Cleanup(func() { removeTombstoneFunc = prev })

	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reaped != 1 || exists(f.inst) {
		t.Fatalf("reaped %d; want the unit removed\nstderr:\n%s", res.Reaped, f.stderr.String())
	}
	if start < 0 {
		t.Fatal("the walk never opened a directory inside the tombstone: the worktree wasn't removed there")
	}
	if end != start {
		b, _ := os.ReadFile(log)
		calls := strings.Split(strings.TrimSpace(string(b)), "\n")
		t.Fatalf("git calls from the first worktree removal to the tombstone's removal: %q", calls[start:end])
	}
	if strings.Join(fake.removed, ",") != passSessionID[:8] {
		t.Fatalf("claude rm calls %q; want one for %s", fake.removed, passSessionID[:8])
	}
	assertNoTombstone(t, f.root)
	out.AssertUnchanged(t)
}

// A walk stopped inside the tombstone, by a directory swapped for a link
// between the walk's read of its type and its open, keeps the unit: it is
// renamed back with the path named, no session is removed, and the link's
// target is unchanged.
func TestDecideFirst_AWalkStoppedInsideTheTombstoneRenamesBack(t *testing.T) {
	f, fake := swapPassUnit(t)
	out := removaltest.NewOutside(t)
	swapped := ""
	var swapErr error
	t.Cleanup(removal.SetBeforeOpenHook(func(p string) {
		if swapped != "" || !strings.Contains(p, tombstonePathPart) || filepath.Base(p) != "deep" || filepath.Base(filepath.Dir(p)) != "wt" {
			return
		}
		swapped = p
		if swapErr = os.Rename(p, p+".swapped"); swapErr == nil {
			swapErr = os.Symlink(out.Dir, p)
		}
	}))
	keptAtActing(t, f, "changed during the pass: wt/deep")
	if swapped == "" || swapErr != nil {
		t.Fatalf("the swap inside the tombstone didn't run (%q, %v)", swapped, swapErr)
	}
	if len(fake.removed) != 0 {
		t.Errorf("claude rm ran for a kept unit: %q", fake.removed)
	}
	out.AssertUnchanged(t)
}
