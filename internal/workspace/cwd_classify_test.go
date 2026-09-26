package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyCwd_InsideInstance(t *testing.T) {
	root := destroySetupWorkspace(t)
	instanceDir := destroySetupInstance(t, root, "alpha")

	got, err := ClassifyCwd(instanceDir)
	if err != nil {
		t.Fatalf("ClassifyCwd: %v", err)
	}
	if got.Class != CwdInsideInstance {
		t.Errorf("Class = %s, want %s", got.Class, CwdInsideInstance)
	}
	if got.InstanceDir != instanceDir {
		t.Errorf("InstanceDir = %q, want %q", got.InstanceDir, instanceDir)
	}
	if got.WorkspaceRoot != root {
		t.Errorf("WorkspaceRoot = %q, want %q", got.WorkspaceRoot, root)
	}
}

func TestClassifyCwd_NestedInsideInstance(t *testing.T) {
	root := destroySetupWorkspace(t)
	instanceDir := destroySetupInstance(t, root, "alpha")

	nested := filepath.Join(instanceDir, "subdir", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ClassifyCwd(nested)
	if err != nil {
		t.Fatalf("ClassifyCwd: %v", err)
	}
	if got.Class != CwdInsideInstance {
		t.Errorf("Class = %s, want %s", got.Class, CwdInsideInstance)
	}
	if got.InstanceDir != instanceDir {
		t.Errorf("InstanceDir = %q, want %q (should resolve to enclosing instance, not nested dir)", got.InstanceDir, instanceDir)
	}
}

func TestClassifyCwd_AtWorkspaceRoot(t *testing.T) {
	root := destroySetupWorkspace(t)

	got, err := ClassifyCwd(root)
	if err != nil {
		t.Fatalf("ClassifyCwd: %v", err)
	}
	if got.Class != CwdAtWorkspaceRoot {
		t.Errorf("Class = %s, want %s", got.Class, CwdAtWorkspaceRoot)
	}
	if got.WorkspaceRoot != root {
		t.Errorf("WorkspaceRoot = %q, want %q", got.WorkspaceRoot, root)
	}
	if got.InstanceDir != "" {
		t.Errorf("InstanceDir = %q, want empty for AtWorkspaceRoot", got.InstanceDir)
	}
}

func TestClassifyCwd_AtWorkspaceRootSiblingOfInstance(t *testing.T) {
	// At the workspace root with one or more instances present, ClassifyCwd
	// should still return AtWorkspaceRoot (not InsideInstance) because the
	// workspace root has workspace.toml but no instance.json.
	root := destroySetupWorkspace(t)
	destroySetupInstance(t, root, "alpha")
	destroySetupInstance(t, root, "beta")

	got, err := ClassifyCwd(root)
	if err != nil {
		t.Fatalf("ClassifyCwd: %v", err)
	}
	if got.Class != CwdAtWorkspaceRoot {
		t.Errorf("Class = %s, want %s (workspace root with instances should still classify as root)", got.Class, CwdAtWorkspaceRoot)
	}
}

func TestClassifyCwd_InsideWorktree(t *testing.T) {
	root := destroySetupWorkspace(t)
	instanceDir := destroySetupInstance(t, root, "alpha")

	// Worktree at <instance>/.niwa/worktrees/<repo>-<sid>/ (the layout
	// CreateSession writes; its own .niwa has no instance.json).
	wtPath := filepath.Join(instanceDir, StateDir, "worktrees", "repo-abcd1234")
	if err := os.MkdirAll(filepath.Join(wtPath, StateDir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ClassifyCwd(wtPath)
	if err != nil {
		t.Fatalf("ClassifyCwd: %v", err)
	}
	if got.Class != CwdInsideWorktree {
		t.Errorf("Class = %s, want %s", got.Class, CwdInsideWorktree)
	}
	if got.WorktreeDir != wtPath {
		t.Errorf("WorktreeDir = %q, want %q", got.WorktreeDir, wtPath)
	}
	if got.InstanceDir != instanceDir {
		t.Errorf("InstanceDir = %q, want %q", got.InstanceDir, instanceDir)
	}
	if got.WorkspaceRoot != root {
		t.Errorf("WorkspaceRoot = %q, want %q", got.WorkspaceRoot, root)
	}
}

func TestClassifyCwd_NestedInsideWorktree(t *testing.T) {
	root := destroySetupWorkspace(t)
	instanceDir := destroySetupInstance(t, root, "alpha")
	wtPath := filepath.Join(instanceDir, StateDir, "worktrees", "repo-abcd1234")
	nested := filepath.Join(wtPath, "src", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ClassifyCwd(nested)
	if err != nil {
		t.Fatalf("ClassifyCwd: %v", err)
	}
	if got.Class != CwdInsideWorktree {
		t.Errorf("Class = %s, want %s (a dir below a worktree is still inside-worktree)", got.Class, CwdInsideWorktree)
	}
	if got.WorktreeDir != wtPath {
		t.Errorf("WorktreeDir = %q, want %q (should resolve to the worktree root)", got.WorktreeDir, wtPath)
	}
}

func TestClassifyCwd_WorktreesDirItselfIsInstance(t *testing.T) {
	// A cwd at <instance>/.niwa/worktrees (the parent of worktree roots, not a
	// worktree itself) must NOT classify as inside-worktree: there is no
	// worktree root at or above it. It walks up to the enclosing instance.
	root := destroySetupWorkspace(t)
	instanceDir := destroySetupInstance(t, root, "alpha")
	worktreesDir := filepath.Join(instanceDir, StateDir, "worktrees")
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ClassifyCwd(worktreesDir)
	if err != nil {
		t.Fatalf("ClassifyCwd: %v", err)
	}
	if got.Class != CwdInsideInstance {
		t.Errorf("Class = %s, want %s", got.Class, CwdInsideInstance)
	}
}

func TestClassifyCwd_Outside(t *testing.T) {
	dir := t.TempDir()

	got, err := ClassifyCwd(dir)
	if err != nil {
		t.Fatalf("ClassifyCwd: %v", err)
	}
	if got.Class != CwdOutside {
		t.Errorf("Class = %s, want %s", got.Class, CwdOutside)
	}
	if got.WorkspaceRoot != "" || got.InstanceDir != "" {
		t.Errorf("Outside should have empty paths; got WorkspaceRoot=%q InstanceDir=%q", got.WorkspaceRoot, got.InstanceDir)
	}
}

func TestClassifyCwd_RelativePath(t *testing.T) {
	root := destroySetupWorkspace(t)
	instanceDir := destroySetupInstance(t, root, "alpha")

	// Pass cwd as a relative path; ClassifyCwd should resolve to absolute
	// before walking up.
	relRoot := filepath.Dir(instanceDir)
	pwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(pwd) })
	if err := os.Chdir(relRoot); err != nil {
		t.Fatal(err)
	}

	got, err := ClassifyCwd("alpha")
	if err != nil {
		t.Fatalf("ClassifyCwd: %v", err)
	}
	if got.Class != CwdInsideInstance {
		t.Errorf("Class = %s, want %s", got.Class, CwdInsideInstance)
	}
}

func TestCwdClass_String(t *testing.T) {
	cases := []struct {
		c    CwdClass
		want string
	}{
		{CwdInsideInstance, "inside-instance"},
		{CwdAtWorkspaceRoot, "at-workspace-root"},
		{CwdInsideWorktree, "inside-worktree"},
		{CwdOutside, "outside"},
	}
	for _, tc := range cases {
		if got := tc.c.String(); got != tc.want {
			t.Errorf("CwdClass(%d).String() = %q, want %q", int(tc.c), got, tc.want)
		}
	}
}
