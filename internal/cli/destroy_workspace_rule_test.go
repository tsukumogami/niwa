package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// niwa destroy --workspace goes through the same scan and order as an
// unforced single-instance destroy: the walk and the configuration check on
// every instance before the scan's first git call, a flagged key listed with
// the rest of the findings, and, after the confirmation and the job
// directory vet, every worktree's configuration re-check and fresh status
// before the first removal, then each worktree removed through the
// no-follow walk before any claude rm or instance directory removal.
//
// Every repository, driver and marker file here is under t.TempDir().

func TestDestroyWorkspaceRule_AFlaggedRepositoryIsListedAndNeverRuns(t *testing.T) {
	t.Run("--confirm goes ahead with no prompt", func(t *testing.T) {
		root, clone := destroyScanInstance(t, nil)
		marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
		passGit(t, clone, "config", "core.fsmonitor", markerScript(t, marker))
		setDestroyFlags(t, false, true, "testws")
		stderr := quietDestroyCmd()

		err := runDestroy(destroyCmd, nil)
		assertNoMarker(t, marker)
		if err != nil {
			t.Fatalf("destroy --workspace --confirm testws: %v\n%s", err, stderr.String())
		}
		if !strings.Contains(stderr.String(), "core.fsmonitor") {
			t.Errorf("the workspace scan's listing does not name core.fsmonitor:\n%s", stderr.String())
		}
		if _, err := os.Lstat(filepath.Join(root, "alpha")); !os.IsNotExist(err) {
			t.Errorf("the instance is still there after a confirmed workspace destroy: %v", err)
		}
	})
	t.Run("without a terminal it aborts with the key listed", func(t *testing.T) {
		root, clone := destroyScanInstance(t, nil)
		marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
		passGit(t, clone, "config", "core.fsmonitor", markerScript(t, marker))
		log := destroyGitWrapper(t, "")
		setDestroyFlags(t, false, true, "")
		stderr := quietDestroyCmd()

		err := runDestroy(destroyCmd, nil)
		assertNoMarker(t, marker)
		assertOnlyConfigReads(t, log, clone)
		if err == nil || !strings.Contains(err.Error(), "not a terminal") {
			t.Fatalf("destroy --workspace = %v; want it aborted because stdin isn't a terminal", err)
		}
		if !strings.Contains(stderr.String(), "core.fsmonitor") {
			t.Errorf("the workspace scan's listing does not name core.fsmonitor:\n%s", stderr.String())
		}
		assertInstancesExist(t, root, "alpha")
	})
}

func TestDestroyWorkspaceRule_AChangeAtTheFreshStatusStopsBeforeAnyWorktreeGoes(t *testing.T) {
	root, _, wt := destroyWorktreeInstance(t)
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, false, true, "testws")
	log := destroyGitWrapper(t, "alpha/wt")
	var removed []string
	var calls []int
	prev := worktreeRemover
	worktreeRemover = countingRemover{log: log, removed: &removed, calls: &calls}
	t.Cleanup(func() { worktreeRemover = prev })

	err := runDestroy(destroyCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "destroy stopped before worktree wt was removed") || !strings.Contains(err.Error(), "wt/late.txt") {
		t.Fatalf("destroy --workspace = %v; want it stopped before wt with wt/late.txt named", err)
	}
	if len(removed) != 0 {
		t.Errorf("worktrees removed %q; want none once the fresh status showed a change", removed)
	}
	if _, err := os.Stat(filepath.Join(wt, "late.txt")); err != nil {
		t.Errorf("the worktree lost the file its fresh status found: %v", err)
	}
	assertInstancesExist(t, root, "alpha")
}

// The workspace scenario: an instance holding a clean niwa worktree and a
// stopped session. The worktree goes by the walk before the session, the
// claude rm finds the worktree path gone, and no session is left listed.
func TestDestroyWorkspaceRule_TheWorktreeGoesByTheWalkBeforeClaudeRm(t *testing.T) {
	vetHome(t, ownerSID[:8])
	root, _, wt := destroyWorktreeInstance(t)
	inst := filepath.Join(root, "alpha")
	mapNamedSession(t, root, ownerSID, inst, "fix-thing")
	fake := &listingFake{rows: []claudeSessionRow{{ID: ownerSID[:8], SessionID: ownerSID}}}
	installListingFake(t, fake)
	var atRm []string
	remove := removeClaudeSessionFunc
	removeClaudeSessionFunc = func(ctx context.Context, id string) (string, error) {
		if _, err := os.Lstat(wt); err == nil {
			atRm = append(atRm, id+": worktree present")
		} else {
			atRm = append(atRm, id+": worktree gone")
		}
		return remove(ctx, id)
	}
	t.Cleanup(func() { removeClaudeSessionFunc = remove })
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, false, true, "testws")
	log := gitCallLog(t)
	var order []string
	prev := worktreeRemover
	worktreeRemover = orderedRemover{order: &order, fake: fake}
	t.Cleanup(func() { worktreeRemover = prev })

	if err := runDestroy(destroyCmd, nil); err != nil {
		t.Fatalf("destroy --workspace --confirm testws: %v", err)
	}
	if len(order) != 1 || order[0] != wt+" before 0 sessions removed" {
		t.Errorf("worktree removals %q; want %s removed by the walk before any session", order, wt)
	}
	if len(atRm) != 1 || atRm[0] != ownerSID[:8]+": worktree gone" {
		t.Errorf("claude rm calls %q; want one, with the worktree path already gone", atRm)
	}
	for _, r := range fake.listing() {
		if r.SessionID == ownerSID {
			t.Errorf("the listing still shows the instance's session %s", r.ID)
		}
	}
	if _, err := os.Lstat(inst); !os.IsNotExist(err) {
		t.Errorf("the instance is still there: %v", err)
	}
	assertNoWorktreeRemoval(t, log, false)
}
