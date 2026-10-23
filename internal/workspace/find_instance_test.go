package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// findInstanceFixture builds a workspace root holding one instance, "inst",
// with a cloned repo and a niwa worktree, and returns the root. Both marker
// files are mode 0: FindInstance must find the instance by stat alone, and a
// read of either would fail.
func findInstanceFixture(t *testing.T) string {
	t.Helper()
	root := canonicalTempDir(t)
	mustWriteMode(t, filepath.Join(root, StateDir, WorkspaceConfigFile), "not toml [[[", 0o000)
	mustWriteMode(t, filepath.Join(root, StateDir, StateFile), "{", 0o000)
	inst := filepath.Join(root, "inst")
	mustWriteMode(t, statePath(inst), "not json", 0o000)
	mustMkdir(t, filepath.Join(inst, "group", "repo", "sub"))
	mustMkdir(t, filepath.Join(inst, StateDir, worktreesDirName, "repo-1234", "pkg"))
	return root
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func mustWriteMode(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
}

// statOnly replaces FindInstance's stat seam with one that counts calls, for
// the duration of a test.
func statOnly(t *testing.T) *int {
	t.Helper()
	n := 0
	prev := findInstanceStat
	findInstanceStat = func(p string) (os.FileInfo, error) {
		n++
		return prev(p)
	}
	t.Cleanup(func() { findInstanceStat = prev })
	return &n
}

func assertFound(t *testing.T, dir, wantInstance, wantRoot string) {
	t.Helper()
	got, ok, err := FindInstance(dir)
	if err != nil {
		t.Fatalf("FindInstance(%s): %v", dir, err)
	}
	if !ok || got.Instance != wantInstance || got.WorkspaceRoot != wantRoot {
		t.Fatalf("FindInstance(%s) = %+v, %v; want %s under %s", dir, got, ok, wantInstance, wantRoot)
	}
}

func assertNone(t *testing.T, dir string) {
	t.Helper()
	got, ok, err := FindInstance(dir)
	if err != nil {
		t.Fatalf("FindInstance(%s): %v", dir, err)
	}
	if ok {
		t.Fatalf("FindInstance(%s) = %+v; want none", dir, got)
	}
}

func TestFindInstance_InstanceRepoAndWorktree(t *testing.T) {
	root := findInstanceFixture(t)
	inst := filepath.Join(root, "inst")
	calls := statOnly(t)
	for _, dir := range []string{
		inst,
		filepath.Join(inst, "group", "repo"),
		filepath.Join(inst, "group", "repo", "sub"),
		filepath.Join(inst, StateDir, worktreesDirName, "repo-1234"),
		filepath.Join(inst, StateDir, worktreesDirName, "repo-1234", "pkg"),
	} {
		assertFound(t, dir, inst, root)
	}
	if *calls == 0 {
		t.Error("FindInstance made no stat calls through its seam")
	}
}

func TestFindInstance_RootAndOutside(t *testing.T) {
	root := findInstanceFixture(t)
	assertNone(t, root)
	assertNone(t, filepath.Join(root, StateDir))
	mustMkdir(t, filepath.Join(root, "not-an-instance", "x"))
	assertNone(t, filepath.Join(root, "not-an-instance", "x"))
	assertNone(t, canonicalTempDir(t))
}

func TestFindInstance_PlantedMarkers(t *testing.T) {
	root := findInstanceFixture(t)
	inst := filepath.Join(root, "inst")
	repo := filepath.Join(inst, "group", "repo")

	// A repo that plants an instance marker, and even a whole fake workspace
	// root with an instance under it, does not shadow the real instance.
	mustWriteMode(t, statePath(repo), "{}", 0o644)
	mustWriteMode(t, filepath.Join(repo, StateDir, WorkspaceConfigFile), "", 0o644)
	mustWriteMode(t, statePath(filepath.Join(repo, "sub")), "{}", 0o644)
	assertFound(t, repo, inst, root)
	assertFound(t, filepath.Join(repo, "sub"), inst, root)

	// A marker under the root but outside every enumerated instance is none.
	stray := filepath.Join(root, "stray", "repo")
	mustWriteMode(t, statePath(stray), "{}", 0o644)
	assertNone(t, stray)

	// So is a marker with no workspace root above it at all.
	lone := filepath.Join(canonicalTempDir(t), "lone")
	mustWriteMode(t, statePath(lone), "{}", 0o644)
	assertNone(t, lone)
}

func TestFindInstance_SymlinkedStart(t *testing.T) {
	root := findInstanceFixture(t)
	inst := filepath.Join(root, "inst")
	links := canonicalTempDir(t)

	into := filepath.Join(links, "into")
	if err := os.Symlink(filepath.Join(inst, "group", "repo"), into); err != nil {
		t.Fatal(err)
	}
	assertFound(t, into, inst, root)

	// A link inside the instance that leads out of it finds nothing.
	outside := canonicalTempDir(t)
	out := filepath.Join(inst, "group", "escape")
	if err := os.Symlink(outside, out); err != nil {
		t.Fatal(err)
	}
	assertNone(t, out)

	// A workspace reached through a symlinked parent resolves to the real
	// paths.
	viaParent := filepath.Join(links, "ws")
	if err := os.Symlink(root, viaParent); err != nil {
		t.Fatal(err)
	}
	assertFound(t, filepath.Join(viaParent, "inst", "group"), inst, root)
}

func TestFindInstance_MissingDirIsAnError(t *testing.T) {
	if _, _, err := FindInstance(filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Fatal("FindInstance of a missing directory returned no error")
	}
}
