package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// strayInstanceDir builds what an interrupted create leaves behind: a
// directory under the root that looks like an instance (group directories
// inside it) but has no .niwa/instance.json.
func strayInstanceDir(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "ws+-0000beef")
	for _, sub := range []string{"public", "private"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestClassifyCwd_BelowRootIsFlagged(t *testing.T) {
	root := setupWorkspace(t, []string{"ws-1"})
	stray := strayInstanceDir(t, root)

	got, err := ClassifyCwd(stray)
	if err != nil {
		t.Fatal(err)
	}
	if got.Class != CwdAtWorkspaceRoot || !got.BelowRoot {
		t.Errorf("stray dir: Class=%s BelowRoot=%v, want at-workspace-root with BelowRoot", got.Class, got.BelowRoot)
	}

	got, err = ClassifyCwd(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Class != CwdAtWorkspaceRoot || got.BelowRoot {
		t.Errorf("root: Class=%s BelowRoot=%v, want at-workspace-root without BelowRoot", got.Class, got.BelowRoot)
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
