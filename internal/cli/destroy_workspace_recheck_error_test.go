package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// niwa destroy --workspace runs every worktree's configuration re-check and
// fresh status, in every instance, before the first worktree removal
// anywhere. A re-check that fails in one instance, after another instance's
// worktree passed its own, keeps everything: no worktree in any instance
// goes, no session is removed and every instance stays.
//
// Every repository and wrapper here is under t.TempDir().

// workspaceWorktreeInstance adds to the workspace at root a clone of a
// pushed repository at <name>/app and a linked worktree of it at
// <name>/<wt> on a pushed branch, and returns the clone and the worktree.
func workspaceWorktreeInstance(t *testing.T, root, name, wt string) (string, string) {
	t.Helper()
	remote := filepath.Join(canonicalTempDir(t), name+".git")
	passGit(t, root, "init", "-q", "--bare", "-b", "main", remote)
	seed := filepath.Join(canonicalTempDir(t), "seed")
	passGit(t, root, "init", "-q", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	passGit(t, seed, "add", "README")
	passGit(t, seed, "commit", "-q", "-m", "init")
	passGit(t, seed, "push", "-q", remote, "main")
	clone := filepath.Join(root, name, "app")
	passGit(t, root, "clone", "-q", remote, clone)
	wtDir := filepath.Join(root, name, wt)
	passGit(t, clone, "worktree", "add", "-q", wtDir, "-b", wt+"-branch", "main")
	passGit(t, clone, "push", "-q", "-u", "origin", wt+"-branch")
	return clone, wtDir
}

// breakConfigGitWrapper puts a git on PATH that logs each call and runs the
// real git. The second porcelain v2 status in the worktree whose work tree
// ends in /<at> (the fresh status after the scan's) first appends a line
// git can't parse to the configuration file config, so the next read of it
// fails. It returns the log's path.
func breakConfigGitWrapper(t *testing.T, at, config string) string {
	t.Helper()
	real := realGit(t)
	dir := canonicalTempDir(t)
	scratch := canonicalTempDir(t)
	log := filepath.Join(scratch, "git-calls.log")
	counter := filepath.Join(scratch, "status-count")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> '%s'
wt=""
st=""
for a in "$@"; do
	case "$a" in
	--work-tree=*) wt="${a#--work-tree=}" ;;
	--porcelain=v2) st=1 ;;
	esac
done
case "$wt" in
*"/%s")
	if [ -n "$st" ]; then
		n=$(cat '%s' 2>/dev/null || echo 0)
		n=$((n + 1))
		echo "$n" > '%s'
		if [ "$n" = 2 ]; then
			printf '[[[ not a config line\n' >> '%s'
		fi
	fi
	;;
esac
exec '%s' "$@"
`, log, at, counter, counter, config, real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestDestroyWorkspaceRecheck_AFailedReadInOneInstanceKeepsEveryWorktreeAndSession(t *testing.T) {
	vetHome(t, ownerSID[:8])
	sessionJobDir(t, otherSID[:8])
	root := destroyTestSetup(t, []string{"alpha", "beta"})
	_, alphaWT := workspaceWorktreeInstance(t, root, "alpha", "wt")
	betaClone, betaWT := workspaceWorktreeInstance(t, root, "beta", "wtb")
	mapNamedSession(t, root, ownerSID, filepath.Join(root, "alpha"), "alpha-work")
	mapNamedSession(t, root, otherSID, filepath.Join(root, "beta"), "beta-work")
	fake := &listingFake{rows: []claudeSessionRow{
		{ID: ownerSID[:8], SessionID: ownerSID},
		{ID: otherSID[:8], SessionID: otherSID},
	}}
	installListingFake(t, fake)
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, false, true, "testws")
	// alpha's worktree is re-checked first and passes; its fresh status
	// breaks beta's configuration, so beta's re-check, which follows, fails.
	log := breakConfigGitWrapper(t, "alpha/wt", filepath.Join(betaClone, ".git", "config"))
	var removed []string
	var calls []int
	prev := worktreeRemover
	worktreeRemover = countingRemover{log: log, removed: &removed, calls: &calls}
	t.Cleanup(func() { worktreeRemover = prev })

	err := runDestroy(destroyCmd, nil)
	t.Logf("destroy --workspace: %v", err)
	if err == nil || !strings.Contains(err.Error(), "destroy stopped before worktree wtb was removed") {
		t.Fatalf("destroy --workspace = %v; want it stopped before beta's worktree wtb", err)
	}
	if !strings.Contains(err.Error(), filepath.Join("beta", "app", ".git", "config")) {
		t.Errorf("destroy --workspace = %v; want the configuration file that couldn't be read named", err)
	}
	if len(removed) != 0 {
		t.Errorf("worktrees removed %q; want none in any instance once one re-check failed", removed)
	}
	for _, wt := range []string{alphaWT, betaWT} {
		if _, err := os.Lstat(filepath.Join(wt, "README")); err != nil {
			t.Errorf("worktree %s lost its files: %v", wt, err)
		}
	}
	if len(fake.removed) != 0 {
		t.Errorf("claude rm ran for %q; want every session kept", fake.removed)
	}
	if len(fake.listing()) != 2 {
		t.Errorf("the listing shows %d sessions; want both still listed", len(fake.listing()))
	}
	assertInstancesExist(t, root, "alpha", "beta")
}
