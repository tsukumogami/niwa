package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeRootInstanceJSON writes an .niwa/instance.json at the workspace root,
// reproducing the state `niwa init` persists there (config-name override,
// ephemeral-session flags) for `niwa create` to read. A root carrying this
// file must still classify as a workspace root, never as instance-0.
func writeRootInstanceJSON(t *testing.T, root string) {
	t.Helper()
	state := &InstanceState{
		SchemaVersion: SchemaVersion,
		InstanceName:  "test-ws",
		Root:          root,
		Created:       time.Now(),
		LastApplied:   time.Now(),
		Repos:         map[string]RepoState{},
	}
	if err := SaveState(root, state); err != nil {
		t.Fatalf("writing root instance.json: %v", err)
	}
}

// TestClassifyCwd_RootWithInstanceJSON is the regression guard for the bug where
// a workspace root that also carries .niwa/instance.json (written by init) was
// misclassified as inside-instance. That misclassification made `niwa apply` at
// the root treat the root as instance-0 and clone repos directly under it.
func TestClassifyCwd_RootWithInstanceJSON(t *testing.T) {
	root := setupWorkspace(t, nil)
	writeRootInstanceJSON(t, root)

	got, err := ClassifyCwd(root)
	if err != nil {
		t.Fatalf("ClassifyCwd: %v", err)
	}
	if got.Class != CwdAtWorkspaceRoot {
		t.Errorf("Class = %s, want %s (a root carrying instance.json is still the root, never instance-0)", got.Class, CwdAtWorkspaceRoot)
	}
	if got.WorkspaceRoot != root {
		t.Errorf("WorkspaceRoot = %q, want %q", got.WorkspaceRoot, root)
	}
	if got.InstanceDir != "" {
		t.Errorf("InstanceDir = %q, want empty (the root is not an instance)", got.InstanceDir)
	}
}

// TestClassifyCwd_SubdirOfRootWithInstanceJSON verifies that a non-instance
// subdirectory of such a root (e.g. a group dir like public/) still resolves to
// the workspace root rather than the root-as-instance.
func TestClassifyCwd_SubdirOfRootWithInstanceJSON(t *testing.T) {
	root := setupWorkspace(t, nil)
	writeRootInstanceJSON(t, root)

	sub := filepath.Join(root, "public")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ClassifyCwd(sub)
	if err != nil {
		t.Fatalf("ClassifyCwd: %v", err)
	}
	if got.Class != CwdAtWorkspaceRoot {
		t.Errorf("Class = %s, want %s", got.Class, CwdAtWorkspaceRoot)
	}
}

// TestClassifyCwd_ChildInstanceStillClassifies confirms the fix does not break
// real child instances: an instance directory under the root (instance.json,
// no workspace.toml) still classifies as inside-instance.
func TestClassifyCwd_ChildInstanceStillClassifies(t *testing.T) {
	root := setupWorkspace(t, nil)
	writeRootInstanceJSON(t, root)
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
}

// TestResolveApplyScope_RootWithInstanceJSON is the end-to-end regression guard:
// `niwa apply` at a root carrying instance.json must resolve to ApplyAll (root
// config + cascade into child instances), NOT ApplySingle on the root (which
// cloned repos under the root). The root must not appear in the cascade list.
func TestResolveApplyScope_RootWithInstanceJSON(t *testing.T) {
	root := setupWorkspace(t, []string{"alpha", "beta"})
	writeRootInstanceJSON(t, root)

	scope, err := ResolveApplyScope(root, "")
	if err != nil {
		t.Fatalf("ResolveApplyScope: %v", err)
	}
	if scope.Mode != ApplyAll {
		t.Fatalf("Mode = %v, want ApplyAll (root scope, not ApplySingle on the root)", scope.Mode)
	}
	if scope.WorkspaceRoot != root {
		t.Errorf("WorkspaceRoot = %q, want %q", scope.WorkspaceRoot, root)
	}
	for _, inst := range scope.Instances {
		if inst == root {
			t.Errorf("Instances contains the workspace root %q; the root must never be applied as an instance", root)
		}
	}
	if len(scope.Instances) != 2 {
		t.Errorf("len(Instances) = %d, want 2 (the two child instances)", len(scope.Instances))
	}
}
