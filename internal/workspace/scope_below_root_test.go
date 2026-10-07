package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// strayInstanceDir builds what an interrupted create leaves behind: a
// directory under the root that looks like an instance (the instance
// .gitignore create writes first, group directories inside it) but has no
// .niwa/instance.json.
func strayInstanceDir(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "ws+-0000beef")
	for _, sub := range []string{"public", "private"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureInstanceGitignore(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestResolveApplyScope_HintOnlyForInterruptedCreate: the hint that the
// directory may be an interrupted create appears only for a directory carrying
// the instance .gitignore create writes first. A plain directory under the
// root that was never an instance is refused without it.
func TestResolveApplyScope_HintOnlyForInterruptedCreate(t *testing.T) {
	root := setupWorkspace(t, []string{"ws-1"})
	scratch := filepath.Join(root, "scratch", "deeper")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveApplyScope(scratch, "")
	if err == nil {
		t.Fatal("expected refusal")
	}
	if strings.Contains(err.Error(), "interrupted") {
		t.Errorf("%s was never an instance; the hint does not apply: %v", scratch, err)
	}

	// A .gitignore that isn't the instance one doesn't count either.
	if err := os.WriteFile(filepath.Join(root, "scratch", ".gitignore"), []byte("*.o\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveApplyScope(scratch, ""); err == nil || strings.Contains(err.Error(), "interrupted") {
		t.Errorf("unrelated .gitignore: want a refusal without the hint, got %v", err)
	}

	// From deep inside a real interrupted create, the hint names its top.
	stray := strayInstanceDir(t, root)
	_, err = ResolveApplyScope(filepath.Join(stray, "public"), "")
	if err == nil || !strings.Contains(err.Error(), stray+" looks like an instance whose creation was interrupted") {
		t.Errorf("want the hint naming %s, got %v", stray, err)
	}
}

// TestClassifyCwd_BelowRootIsItsOwnClass: a stray directory gets a class of
// its own, not the root's, so a command that switches on CwdAtWorkspaceRoot
// to take the whole workspace's scope can't match it without naming it.
func TestClassifyCwd_BelowRootIsItsOwnClass(t *testing.T) {
	root := setupWorkspace(t, []string{"ws-1"})
	stray := strayInstanceDir(t, root)

	got, err := ClassifyCwd(stray)
	if err != nil {
		t.Fatal(err)
	}
	if got.Class != CwdBelowWorkspaceRoot || got.WorkspaceRoot != root || got.InstanceDir != "" {
		t.Errorf("stray dir: %+v, want below-workspace-root under %s", got, root)
	}

	got, err = ClassifyCwd(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Class != CwdAtWorkspaceRoot {
		t.Errorf("root: Class=%s, want at-workspace-root", got.Class)
	}
}

// TestResolveApplyScope_RefusesStrayDirectory is issue #344: apply run from a
// half-provisioned instance directory resolved to ApplyAll and converged every
// instance in the workspace. It must refuse, from the directory and anything
// below it, and --instance must not get around that.
func TestResolveApplyScope_RefusesStrayDirectory(t *testing.T) {
	root := setupWorkspace(t, []string{"ws-1", "ws-2"})
	stray := strayInstanceDir(t, root)

	for _, cwd := range []string{stray, filepath.Join(stray, "public")} {
		for _, flag := range []string{"", "ws-1"} {
			scope, err := ResolveApplyScope(cwd, flag)
			if err == nil {
				t.Fatalf("cwd=%s instance=%q: expected refusal, got scope mode %d over %d instance(s)", cwd, flag, scope.Mode, len(scope.Instances))
			}
			for _, want := range []string{cwd, "not an instance, a worktree, or the workspace root", "interrupted"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("cwd=%s instance=%q: error %q should contain %q", cwd, flag, err, want)
				}
			}
		}
	}
}

// The root's own .niwa/ counts as the root, at any depth: editing the config
// there and applying is ordinary use, and it's the root's own scope, not a
// wider one. Another dot-directory under the root does not, and gets the
// refusal without the interrupted-create hint, which would be wrong there.
func TestResolveApplyScope_RootConfigDirIsTheRoot(t *testing.T) {
	root := setupWorkspace(t, []string{"ws-1"})
	tools := filepath.Join(root, StateDir, "coordinator-tools")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{filepath.Join(root, StateDir), tools} {
		scope, err := ResolveApplyScope(cwd, "")
		if err != nil {
			t.Fatalf("cwd=%s: %v", cwd, err)
		}
		if scope.Mode != ApplyAll || scope.WorkspaceRoot != root {
			t.Errorf("cwd=%s: mode=%d root=%q, want ApplyAll at %q", cwd, scope.Mode, scope.WorkspaceRoot, root)
		}
	}

	other := filepath.Join(root, ".claude", "skills")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveApplyScope(other, "")
	if err == nil {
		t.Fatal("expected refusal from a dot-directory under the root other than .niwa/")
	}
	if strings.Contains(err.Error(), "interrupted") {
		t.Errorf("hint about interrupted creation does not apply to %s: %v", other, err)
	}
}
