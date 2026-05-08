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
		{CwdOutside, "outside"},
	}
	for _, tc := range cases {
		if got := tc.c.String(); got != tc.want {
			t.Errorf("CwdClass(%d).String() = %q, want %q", int(tc.c), got, tc.want)
		}
	}
}
