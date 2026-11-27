package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// checkResetUncommitted's own results, by repository. The first five tests
// are the ones the workspace package's uncommitted-changes check had, on the
// same layouts.

// resetCheckState makes a workspace with one instance named name, records
// repos in its state file, and returns the root and the instance.
func resetCheckState(t *testing.T, name string, repos map[string]bool) (root, inst string) {
	t.Helper()
	root = destroyTestSetup(t, []string{name})
	inst = filepath.Join(root, name)
	setResetRepos(t, inst, repos)
	return root, inst
}

func checkReset(t *testing.T, root, inst string) (dirty, unchecked []string) {
	t.Helper()
	dirty, unchecked, err := checkResetUncommitted(context.Background(), root, inst)
	if err != nil {
		t.Fatalf("checkResetUncommitted: %v", err)
	}
	return dirty, unchecked
}

func TestCheckResetUncommitted_CleanRepos(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root, inst := resetCheckState(t, "clean-instance", map[string]bool{"myrepo": true})
	resetCheckRepo(t, filepath.Join(inst, "myrepo"))

	dirty, unchecked := checkReset(t, root, inst)
	if len(dirty) != 0 {
		t.Errorf("expected no dirty repos, got %v", dirty)
	}
	if len(unchecked) != 0 {
		t.Errorf("expected no unchecked repos, got %v", unchecked)
	}
}

func TestCheckResetUncommitted_DirtyRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root, inst := resetCheckState(t, "dirty-instance", map[string]bool{"myrepo": true})
	repoDir := filepath.Join(inst, "myrepo")
	resetCheckRepo(t, repoDir)

	// Create an uncommitted file.
	if err := os.WriteFile(filepath.Join(repoDir, "uncommitted.txt"), []byte("changes"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirty, unchecked := checkReset(t, root, inst)
	if len(dirty) != 1 || dirty[0] != "myrepo" {
		t.Errorf("expected [myrepo], got %v", dirty)
	}
	if len(unchecked) != 0 {
		t.Errorf("expected no unchecked repos, got %v", unchecked)
	}
}

func TestCheckResetUncommitted_SkipsNotCloned(t *testing.T) {
	root, inst := resetCheckState(t, "skip-instance", map[string]bool{"not-cloned": false})

	dirty, unchecked := checkReset(t, root, inst)
	if len(dirty) != 0 {
		t.Errorf("expected no dirty repos for uncloned, got %v", dirty)
	}
	if len(unchecked) != 0 {
		t.Errorf("expected no unchecked repos for uncloned, got %v", unchecked)
	}
}

func TestCheckResetUncommitted_SkipsMissingDir(t *testing.T) {
	root, inst := resetCheckState(t, "missing-dir-instance", map[string]bool{"gone": true})

	dirty, unchecked := checkReset(t, root, inst)
	if len(dirty) != 0 {
		t.Errorf("expected no dirty repos for missing dir, got %v", dirty)
	}
	if len(unchecked) != 0 {
		t.Errorf("expected no unchecked repos for missing dir, got %v", unchecked)
	}
}

func TestCheckResetUncommitted_MultipleRepos(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root, inst := resetCheckState(t, "multi-instance", map[string]bool{"clean": true, "dirty": true})

	// Create two repos: one clean, one dirty.
	resetCheckRepo(t, filepath.Join(inst, "clean"))
	dirtyDir := filepath.Join(inst, "dirty")
	resetCheckRepo(t, dirtyDir)
	if err := os.WriteFile(filepath.Join(dirtyDir, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirtyRepos, unchecked := checkReset(t, root, inst)
	sort.Strings(dirtyRepos)
	if len(dirtyRepos) != 1 || dirtyRepos[0] != "dirty" {
		t.Errorf("expected [dirty], got %v", dirtyRepos)
	}
	if len(unchecked) != 0 {
		t.Errorf("expected no unchecked repos, got %v", unchecked)
	}
}

// submoduleState is a clean instance whose one repository, app, has a
// submodule at app/sub. It returns the root, the instance, the clone and the
// submodule.
func submoduleState(t *testing.T) (root, inst, clone, sub string) {
	t.Helper()
	root, inst = resetCheckState(t, "alpha", map[string]bool{"app": true})
	clone = filepath.Join(inst, "app")
	resetCheckRepo(t, clone)
	sub = addResetSubmodule(t, clone)
	if dirty, unchecked := checkReset(t, root, inst); len(dirty)+len(unchecked) != 0 {
		t.Fatalf("the fixture is not clean: dirty %v, unchecked %v", dirty, unchecked)
	}
	return root, inst, clone, sub
}

func assertResetLists(t *testing.T, root, inst string, wantDirty []string) {
	t.Helper()
	dirty, unchecked := checkReset(t, root, inst)
	if !reflect.DeepEqual(dirty, wantDirty) {
		t.Errorf("dirty = %v, want %v", dirty, wantDirty)
	}
	if len(unchecked) != 0 {
		t.Errorf("unchecked = %v, want none", unchecked)
	}
}

func TestCheckResetUncommitted_AModifiedFileInASubmoduleListsTheSubmodule(t *testing.T) {
	root, inst, _, sub := submoduleState(t)
	writeTestFile(t, filepath.Join(sub, "README"), "changed, never committed\n")

	assertResetLists(t, root, inst, []string{"app/sub"})
}

func TestCheckResetUncommitted_ASubmoduleMovedToAnotherCommitListsTheSuperproject(t *testing.T) {
	root, inst, _, sub := submoduleState(t)
	writeTestFile(t, filepath.Join(sub, "more.txt"), "committed in the submodule only\n")
	passGit(t, sub, "add", "more.txt")
	passGit(t, sub, "commit", "-q", "-m", "more")

	assertResetLists(t, root, inst, []string{"app"})
}

func TestCheckResetUncommitted_AStagedSubmoduleCommitListsTheSuperproject(t *testing.T) {
	root, inst, clone, sub := submoduleState(t)
	writeTestFile(t, filepath.Join(sub, "more.txt"), "committed in the submodule only\n")
	passGit(t, sub, "add", "more.txt")
	passGit(t, sub, "commit", "-q", "-m", "more")
	passGit(t, clone, "add", "sub")

	assertResetLists(t, root, inst, []string{"app"})
}

// The submodule's store stays under .git/modules with no work tree, which
// the check can't read, so it is listed as unchecked as well.
func TestCheckResetUncommitted_ARemovedSubmoduleWorkTreeListsTheSuperproject(t *testing.T) {
	root, inst, _, sub := submoduleState(t)
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}

	dirty, unchecked := checkReset(t, root, inst)
	if !reflect.DeepEqual(dirty, []string{"app"}) {
		t.Errorf("dirty = %v, want [app]", dirty)
	}
	if len(unchecked) != 1 || !strings.Contains(unchecked[0], "app/.git/modules/sub") {
		t.Errorf("unchecked = %v, want the submodule's store", unchecked)
	}
}

// An uninitialized submodule is an empty directory at the gitlink's path,
// with no store under the superproject's .git/modules.
func TestCheckResetUncommitted_AnUninitializedSubmoduleIsClean(t *testing.T) {
	root, inst, clone, sub := submoduleState(t)
	for _, p := range []string{sub, filepath.Join(clone, ".git", "modules", "sub")} {
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	assertResetLists(t, root, inst, nil)
}
