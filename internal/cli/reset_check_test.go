package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// An unforced niwa reset checks every cloned repository for uncommitted
// changes before it destroys anything. Each repository name in the state
// file is resolved inside the instance before anything is read or run at it,
// the removal walk and the configuration check run over the instance before
// any other git call, and every status goes through the removal pass's
// command builder. A repository the check can't read refuses the reset just
// as a dirty one does.
//
// The workspaces here have a local-only config, so a reset whose check
// passes stops at the local-only refusal and never destroys the instance.
// Every repository, driver and marker file is under t.TempDir().

// setResetRepos records repos, by name, in the instance's state file.
func setResetRepos(t *testing.T, inst string, repos map[string]bool) {
	t.Helper()
	state, err := workspace.LoadState(inst)
	if err != nil {
		t.Fatal(err)
	}
	state.Repos = map[string]workspace.RepoState{}
	for name, cloned := range repos {
		state.Repos[name] = workspace.RepoState{URL: "git@github.com:org/" + filepath.Base(name) + ".git", Cloned: cloned}
	}
	if err := workspace.SaveState(inst, state); err != nil {
		t.Fatal(err)
	}
}

// resetCheckRepo makes a repository at dir with one commit.
func resetCheckRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	passGit(t, dir, "init", "-q", "-b", "main")
	writeTestFile(t, filepath.Join(dir, "README"), "hi\n")
	passGit(t, dir, "add", "README")
	passGit(t, dir, "commit", "-q", "-m", "init")
}

// resetCheckInstance is destroyScanInstance with the clone recorded as the
// instance's one cloned repository, app, and reset's --force off. It returns
// the root, the instance and the clone.
func resetCheckInstance(t *testing.T, seed func(repo string)) (root, inst, clone string) {
	t.Helper()
	root, clone = destroyScanInstance(t, seed)
	inst = filepath.Join(root, "alpha")
	setResetRepos(t, inst, map[string]bool{"app": true})
	prevForce := resetForce
	resetForce = false
	t.Cleanup(func() { resetForce = prevForce })
	return root, inst, clone
}

// addResetSubmodule adds a submodule at clone/sub from a repository outside
// the instance and commits it, leaving the clone clean.
func addResetSubmodule(t *testing.T, clone string) string {
	t.Helper()
	src := filepath.Join(canonicalTempDir(t), "sub-src")
	resetCheckRepo(t, src)
	passGit(t, clone, "-c", "protocol.file.allow=always", "submodule", "add", "-q", src, "sub")
	passGit(t, clone, "commit", "-q", "-m", "add sub")
	return filepath.Join(clone, "sub")
}

// runResetCheck runs an unforced niwa reset of alpha and returns its error
// and stderr.
func runResetCheck(t *testing.T) (error, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetContext(context.Background())
	err := runReset(cmd, []string{"alpha"})
	return err, stderr.String()
}

// assertResetRefused checks that the reset stopped at its uncommitted-changes
// check, before the local-only refusal, and that its output names each of
// want.
func assertResetRefused(t *testing.T, root string, err error, stderr string, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("an unforced reset went ahead; want it refused\nstderr:\n%s", stderr)
	}
	if strings.Contains(err.Error(), "local-only") {
		t.Fatalf("the reset passed its uncommitted-changes check: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("the refusal does not point at --force: %v", err)
	}
	out := err.Error() + "\n" + stderr
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("reset output does not name %q:\n%s", w, out)
		}
	}
	assertInstancesExist(t, root, "alpha")
}

// assertResetChecked checks that the reset passed its uncommitted-changes
// check and stopped at the local-only refusal.
func assertResetChecked(t *testing.T, err error, stderr string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "local-only") {
		t.Fatalf("want the check to pass and the local-only refusal to stop the reset; got %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("a passing check printed:\n%s", stderr)
	}
}

// assertNoGitCallNaming fails when any call the git wrapper logged names a
// path containing part.
func assertNoGitCallNaming(t *testing.T, log, part string) {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, part) {
			t.Errorf("git ran naming %q: %s", part, line)
		}
	}
}

// outsideFsmonitorRepo makes a repository at root/outside, beside the
// instance, whose core.fsmonitor names a script that writes marker.
func outsideFsmonitorRepo(t *testing.T, root, marker string) string {
	t.Helper()
	outside := filepath.Join(root, "outside")
	resetCheckRepo(t, outside)
	passGit(t, outside, "config", "core.fsmonitor", markerScript(t, marker))
	return outside
}

func TestResetCheck_AStateNameLeavingTheInstanceIsRefusedAndNeverRead(t *testing.T) {
	root, inst, _ := resetCheckInstance(t, nil)
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	outsideFsmonitorRepo(t, root, marker)
	setResetRepos(t, inst, map[string]bool{"app": true, "../outside": true})
	log := destroyGitWrapper(t, "")

	err, stderr := runResetCheck(t)
	assertNoMarker(t, marker)
	assertNoGitCallNaming(t, log, "/outside")
	assertResetRefused(t, root, err, stderr, "../outside")
}

func TestResetCheck_ARepoDirThatIsALinkOutOfTheInstanceIsRefused(t *testing.T) {
	root, inst, _ := resetCheckInstance(t, nil)
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	outside := outsideFsmonitorRepo(t, root, marker)
	if err := os.Symlink(outside, filepath.Join(inst, "linked")); err != nil {
		t.Fatal(err)
	}
	setResetRepos(t, inst, map[string]bool{"app": true, "linked": true})
	log := destroyGitWrapper(t, "")

	err, stderr := runResetCheck(t)
	assertNoMarker(t, marker)
	assertNoGitCallNaming(t, log, "/outside")
	assertNoGitCallNaming(t, log, "/alpha/linked")
	assertResetRefused(t, root, err, stderr, "linked")
}

func TestResetCheck_AProgramKeyInAnInstanceRepoIsRefusedNamingTheKey(t *testing.T) {
	root, _, clone := resetCheckInstance(t, nil)
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	passGit(t, clone, "config", "core.fsmonitor", markerScript(t, marker))

	err, stderr := runResetCheck(t)
	assertNoMarker(t, marker)
	assertResetRefused(t, root, err, stderr, "core.fsmonitor", "app")
}

func TestResetCheck_ACleanInstancePassesTheCheck(t *testing.T) {
	resetCheckInstance(t, nil)

	err, stderr := runResetCheck(t)
	assertResetChecked(t, err, stderr)
}

func TestResetCheck_AnUntrackedFileIsListedAsUncommitted(t *testing.T) {
	root, _, clone := resetCheckInstance(t, nil)
	writeTestFile(t, filepath.Join(clone, "new.txt"), "never committed\n")

	err, stderr := runResetCheck(t)
	assertResetRefused(t, root, err, stderr, "uncommitted changes", "\n  app\n")
}

func TestResetCheck_AModifiedFileInASubmoduleIsListed(t *testing.T) {
	root, _, clone := resetCheckInstance(t, nil)
	sub := addResetSubmodule(t, clone)
	writeTestFile(t, filepath.Join(sub, "README"), "changed, never committed\n")

	err, stderr := runResetCheck(t)
	assertResetRefused(t, root, err, stderr, "uncommitted changes", "\n  app")
}

func TestResetCheck_ASubmoduleMovedToAnotherCommitIsListed(t *testing.T) {
	root, _, clone := resetCheckInstance(t, nil)
	sub := addResetSubmodule(t, clone)
	writeTestFile(t, filepath.Join(sub, "more.txt"), "committed in the submodule only\n")
	passGit(t, sub, "add", "more.txt")
	passGit(t, sub, "commit", "-q", "-m", "more")
	if out := passGitOut(t, sub, "status", "--porcelain"); out != "" {
		t.Fatalf("the submodule should hold nothing uncommitted: %q", out)
	}

	err, stderr := runResetCheck(t)
	assertResetRefused(t, root, err, stderr, "uncommitted changes", "\n  app\n")
}

func TestResetCheck_ANotClonedOrMissingRepoIsSkipped(t *testing.T) {
	_, inst, _ := resetCheckInstance(t, nil)
	setResetRepos(t, inst, map[string]bool{"app": true, "gone": true, "later": false})

	err, stderr := runResetCheck(t)
	assertResetChecked(t, err, stderr)
}
