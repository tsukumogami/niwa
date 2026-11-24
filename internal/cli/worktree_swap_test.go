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
)

// The swap scenarios: a niwa worktree inside a clean unit, an outside
// directory whose listing, sizes and hashes are recorded first, and the
// removal walk's test hook swapping a directory for a link to the outside
// directory between the walk's read of its type and its open. Through a
// reap, niwa destroy and niwa destroy --force nothing outside the instance
// is deleted. The setup is in helpers the hook worktree sweep's runs can
// share: deepWorktree, swapTargets, installSwap and gitCallLog.

// deepWorktree commits deep/a/b/file.txt on the worktree's branch at wt and
// pushes it, so the unit stays clean with a deep entry in its worktree.
func deepWorktree(t *testing.T, wt string) {
	t.Helper()
	writeTestFile(t, filepath.Join(wt, "deep", "a", "b", "file.txt"), "deep\n")
	passGit(t, wt, "add", "deep")
	passGit(t, wt, "commit", "-q", "-m", "deep")
	passGit(t, wt, "push", "-q", "origin", "wt-branch")
}

// swapTarget is one of the three directories a swap scenario swaps: the
// path to swap and the path the kept reason names it by.
type swapTarget struct {
	name      string
	path, rel string
}

// swapTargets are the deep entry, the worktree's own directory and its
// admin directory, for the worktree at <instance>/wt of the clone at
// <instance>/<cloneRel>.
func swapTargets(instance, cloneRel string) []swapTarget {
	admin := cloneRel + "/.git/worktrees/wt"
	return []swapTarget{
		{"deep entry", filepath.Join(instance, "wt", "deep"), "wt/deep"},
		{"worktree directory", filepath.Join(instance, "wt"), "wt"},
		{"admin directory", filepath.Join(instance, filepath.FromSlash(admin)), admin},
	}
}

// installSwap makes the outside directory and puts the walk's hook in to
// swap target for a link to it.
func installSwap(t *testing.T, target string) (*removaltest.Outside, *removaltest.Swap) {
	t.Helper()
	out := removaltest.NewOutside(t)
	swap := removaltest.NewSwap(target, out.Dir)
	t.Cleanup(removal.SetBeforeOpenHook(swap.Hook))
	return out, swap
}

// gitCallLog puts a git on PATH that logs each call's arguments, one line
// per call, and runs the real git. It returns the log's path.
func gitCallLog(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	if real, err = filepath.EvalSymlinks(real); err != nil {
		t.Fatal(err)
	}
	dir := canonicalTempDir(t)
	log := filepath.Join(canonicalTempDir(t), "git-calls.log")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> '%s'\nexec '%s' \"$@\"\n", log, real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// assertNoWorktreeRemoval checks the git log holds calls and that none is
// git worktree remove or git worktree prune, and, unless forced is set,
// that none carries --force.
func assertNoWorktreeRemoval(t *testing.T, log string, forced bool) {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil || len(b) == 0 {
		t.Fatalf("the git wrapper logged nothing (%v)", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.Contains(line, "worktree remove") || strings.Contains(line, "worktree prune") {
			t.Errorf("git call removes or prunes a worktree: %s", line)
		}
		if !forced && strings.Contains(line, "--force") {
			t.Errorf("git call carries --force: %s", line)
		}
	}
}

// listingFake is a claude fake whose listing drops each session claude rm
// removed, so a listing after the step shows what is left.
type listingFake struct {
	rows    []claudeSessionRow
	removed []string
}

func installListingFake(t *testing.T, f *listingFake) {
	t.Helper()
	prevList, prevRemove := listClaudeSessionsFunc, removeClaudeSessionFunc
	listClaudeSessionsFunc = func(context.Context) ([]claudeSessionRow, error) { return f.listing(), nil }
	removeClaudeSessionFunc = func(_ context.Context, id string) (string, error) {
		f.removed = append(f.removed, id)
		return "", nil
	}
	t.Cleanup(func() { listClaudeSessionsFunc, removeClaudeSessionFunc = prevList, prevRemove })
}

func (f *listingFake) listing() []claudeSessionRow {
	var out []claudeSessionRow
	for _, r := range f.rows {
		gone := false
		for _, id := range f.removed {
			gone = gone || id == r.ID
		}
		if !gone {
			out = append(out, r)
		}
	}
	return out
}

// swapPassUnit is the pass fixture with a niwa worktree at <instance>/wt
// holding a pushed deep entry, its session listed and its job directory in
// place.
func swapPassUnit(t *testing.T) (passFixture, *listingFake) {
	t.Helper()
	f := newPassFixture(t)
	lockTestHome(t)
	deepWorktree(t, worktreeUnit(t, f))
	passJobDir(t, f)
	fake := &listingFake{rows: []claudeSessionRow{passRow()}}
	installListingFake(t, fake)
	return f, fake
}

func TestWorktreeSwap_TheReapKeepsTheUnitAndDeletesNothingOutside(t *testing.T) {
	for i, tg := range swapTargets("", "") {
		t.Run(tg.name, func(t *testing.T) {
			f, fake := swapPassUnit(t)
			target := swapTargets(f.inst, "tools/app")[i]
			log := gitCallLog(t)
			out, swap := installSwap(t, target.path)
			removed := realRemoval(t)
			res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			swap.AssertFired(t)
			out.AssertUnchanged(t)
			if res.Reaped != 0 || len(*removed) != 0 {
				t.Fatalf("reaped %d, removed %v; want the unit kept\nstderr:\n%s", res.Reaped, *removed, f.stderr.String())
			}
			want := "changed during the pass: " + target.rel
			if len(res.Kept) != 1 || !strings.Contains(res.Kept[0].Reason, want) {
				t.Fatalf("kept %+v; want the unit kept with %q\nstderr:\n%s", res.Kept, want, f.stderr.String())
			}
			if len(fake.removed) != 0 {
				t.Errorf("claude rm ran for a kept unit: %q", fake.removed)
			}
			assertNoTombstone(t, f.root)
			assertNoWorktreeRemoval(t, log, false)
		})
	}
}

// swapDestroyInstance is destroyWorktreeInstance with a pushed deep entry in
// its worktree, run from the workspace root.
func swapDestroyInstance(t *testing.T, force bool) (string, string) {
	t.Helper()
	root, _, wt := destroyWorktreeInstance(t)
	deepWorktree(t, wt)
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, force, false, "")
	return root, filepath.Join(root, "alpha")
}

func TestWorktreeSwap_AnUnforcedDestroyRefusesAndDeletesNothingOutside(t *testing.T) {
	for i, tg := range swapTargets("", "") {
		t.Run(tg.name, func(t *testing.T) {
			root, inst := swapDestroyInstance(t, false)
			target := swapTargets(inst, "app")[i]
			log := gitCallLog(t)
			out, swap := installSwap(t, target.path)
			err := runDestroy(destroyCmd, []string{"alpha"})
			swap.AssertFired(t)
			out.AssertUnchanged(t)
			want := "changed during the pass: " + target.rel
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("destroy = %v; want it refused with %q", err, want)
			}
			assertInstancesExist(t, root, "alpha")
			assertNoWorktreeRemoval(t, log, false)
		})
	}
}

func TestWorktreeSwap_AForcedDestroyRemovesTheInstanceAndDeletesNothingOutside(t *testing.T) {
	for i, tg := range swapTargets("", "") {
		t.Run(tg.name, func(t *testing.T) {
			_, inst := swapDestroyInstance(t, true)
			target := swapTargets(inst, "app")[i]
			log := gitCallLog(t)
			out, swap := installSwap(t, target.path)
			if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
				t.Fatalf("destroy --force: %v", err)
			}
			swap.AssertFired(t)
			out.AssertUnchanged(t)
			if _, err := os.Lstat(inst); !os.IsNotExist(err) {
				t.Fatalf("the instance is still there: %v", err)
			}
			if b, err := os.ReadFile(log); err == nil {
				for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
					if strings.Contains(line, "worktree remove") || strings.Contains(line, "worktree prune") {
						t.Errorf("git call removes or prunes a worktree: %s", line)
					}
				}
			}
		})
	}
}

// A clean unit holding a niwa worktree leaves no session behind: after the
// reap the listing shows none for it, and the worktree went with the
// instance by the no-follow walk, with no git call removing it.
func TestWorktreeSwap_ACleanUnitWithAWorktreeLeavesNoSessionAfterAReap(t *testing.T) {
	f, fake := swapPassUnit(t)
	log := gitCallLog(t)
	removed := realRemoval(t)
	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reaped != 1 || len(*removed) != 1 {
		t.Fatalf("reaped %d, removed %v; want the unit removed\nstderr:\n%s", res.Reaped, *removed, f.stderr.String())
	}
	for _, r := range fake.listing() {
		if r.SessionID == passSessionID {
			t.Fatalf("the listing still shows the unit's session %s", r.ID)
		}
	}
	if exists(f.inst) {
		t.Fatal("the instance is still there")
	}
	assertNoWorktreeRemoval(t, log, false)
}

// The same through niwa destroy: the worktree goes first by the walk, then
// the session, then the instance.
func TestWorktreeSwap_ACleanInstanceWithAWorktreeLeavesNoSessionAfterADestroy(t *testing.T) {
	vetHome(t, ownerSID[:8])
	root, _, wt := destroyWorktreeInstance(t)
	inst := filepath.Join(root, "alpha")
	mapNamedSession(t, root, ownerSID, inst, "fix-thing")
	fake := &listingFake{rows: []claudeSessionRow{{ID: ownerSID[:8], SessionID: ownerSID}}}
	installListingFake(t, fake)
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, false, false, "")
	log := gitCallLog(t)
	var order []string
	prev := worktreeRemover
	worktreeRemover = orderedRemover{order: &order, fake: fake}
	t.Cleanup(func() { worktreeRemover = prev })
	if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if len(order) != 1 || order[0] != wt+" before 0 sessions removed" {
		t.Fatalf("worktree removals %q; want %s removed before any session", order, wt)
	}
	for _, r := range fake.listing() {
		if r.SessionID == ownerSID {
			t.Fatalf("the listing still shows the instance's session %s", r.ID)
		}
	}
	if _, err := os.Lstat(inst); !os.IsNotExist(err) {
		t.Fatalf("the instance is still there: %v", err)
	}
	assertNoWorktreeRemoval(t, log, false)
}

// A change made in the worktree after the re-read, planted at the pass's
// one claude listing (after the re-read, before the worktree step), keeps
// the unit before the worktree goes: a new file through the status, a new
// commit through the fresh refs read.
func TestPassWorktree_AChangeAfterTheReReadKeepsTheUnitBeforeItsWorktreeGoes(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, wt string)
		want  string
	}{
		{"new file", func(t *testing.T, wt string) {
			writeTestFile(t, filepath.Join(wt, "late.txt"), "late\n")
		}, "changed during the pass: wt/late.txt"},
		{"new commit", func(t *testing.T, wt string) {
			passGit(t, wt, "commit", "-q", "--allow-empty", "-m", "late")
		}, "ref refs/heads/wt-branch in wt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, fake := swapPassUnit(t)
			wt := filepath.Join(f.inst, "wt")
			list := listClaudeSessionsFunc
			listClaudeSessionsFunc = func(ctx context.Context) ([]claudeSessionRow, error) {
				c.plant(t, wt)
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
				t.Fatalf("kept %+v; want the unit kept with %q", res.Kept, c.want)
			}
			if len(fake.removed) != 0 {
				t.Errorf("claude rm ran for a kept unit: %q", fake.removed)
			}
			if _, err := os.Stat(filepath.Join(wt, "deep", "a", "b", "file.txt")); err != nil {
				t.Errorf("the worktree lost a file: %v", err)
			}
		})
	}
}

// An unforced destroy lists a worktree whose status reports a change, and a
// locked one the walk won't remove, with the scan's findings; without a
// terminal it aborts with nothing removed.
func TestDestroyWorktree_AChangedOrLockedWorktreeJoinsTheScansFindings(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, clone, wt string)
		want  string
	}{
		{"changed", func(t *testing.T, _, wt string) {
			writeTestFile(t, filepath.Join(wt, "late.txt"), "late\n")
		}, "wt: the worktree has changes: untracked late.txt"},
		{"locked", func(t *testing.T, clone, wt string) {
			passGit(t, clone, "worktree", "lock", wt)
		}, "wt: a worktree niwa won't remove on its own (the worktree is locked: app/.git/worktrees/wt/locked)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, clone, wt := destroyWorktreeInstance(t)
			c.plant(t, clone, wt)
			chdirTo(t, root)
			stderr := quietDestroyCmd()
			setDestroyFlags(t, false, false, "")
			err := runDestroy(destroyCmd, []string{"alpha"})
			if err == nil || !strings.Contains(err.Error(), "stdin is not a terminal") {
				t.Fatalf("destroy = %v; want it aborted without a terminal", err)
			}
			if !strings.Contains(stderr.String(), c.want) {
				t.Fatalf("listing %q; want it to hold %q", stderr.String(), c.want)
			}
			if _, err := os.Stat(filepath.Join(wt, "README")); err != nil {
				t.Errorf("the worktree lost a file: %v", err)
			}
			assertInstancesExist(t, root, "alpha")
		})
	}
}

// Under --force a locked worktree, which the walk won't remove, goes with
// the instance directory.
func TestDestroyWorktree_ForceLeavesALockedWorktreeToTheInstanceDirectory(t *testing.T) {
	root, clone, wt := destroyWorktreeInstance(t)
	passGit(t, clone, "worktree", "lock", wt)
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, true, false, "")
	if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
		t.Fatalf("destroy --force: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "alpha")); !os.IsNotExist(err) {
		t.Fatalf("the instance is still there: %v", err)
	}
}

// orderedRemover records each worktree removal with how many sessions had
// been removed by then, and removes through the no-follow walk.
type orderedRemover struct {
	order *[]string
	fake  *listingFake
}

func (r orderedRemover) RemoveWorktree(instance, workTree, adminDir string) error {
	*r.order = append(*r.order, fmt.Sprintf("%s before %d sessions removed", workTree, len(r.fake.removed)))
	return removal.RemoveWorktree(instance, workTree, adminDir)
}
