package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func symlink(t *testing.T, target, link string) string {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return link
}

// TestResolveApplyScope_SymlinkedCwd covers a cwd reached through a symlink.
// ClassifyCwd works on the path as given, without resolving links, and takes
// the root from the same path, so the root comparison and the below-root check
// agree with each other. What must never happen is a whole-workspace scope
// from anywhere but the root itself.
func TestResolveApplyScope_SymlinkedCwd(t *testing.T) {
	root := setupWorkspace(t, []string{"ws-1", "ws-2"})
	stray := strayInstanceDir(t, root)
	if err := os.MkdirAll(filepath.Join(root, "ws-1", "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	linkedRoot := symlink(t, root, filepath.Join(outside, "linked-root"))

	t.Run("the root reached through a symlink is the root", func(t *testing.T) {
		scope, err := ResolveApplyScope(linkedRoot, "")
		if err != nil {
			t.Fatal(err)
		}
		if scope.Mode != ApplyAll || scope.WorkspaceRoot != linkedRoot || len(scope.Instances) != 2 {
			t.Errorf("got mode=%d root=%q instances=%v, want ApplyAll over 2 at %q", scope.Mode, scope.WorkspaceRoot, scope.Instances, linkedRoot)
		}
	})

	t.Run("a stray directory under a symlinked root still refuses", func(t *testing.T) {
		cwd := filepath.Join(linkedRoot, filepath.Base(stray), "public")
		if scope, err := ResolveApplyScope(cwd, ""); err == nil {
			t.Errorf("want a refusal, got mode=%d over %v", scope.Mode, scope.Instances)
		}
	})

	t.Run("an instance under a symlinked root is that instance", func(t *testing.T) {
		cwd := filepath.Join(linkedRoot, "ws-1", "public")
		scope, err := ResolveApplyScope(cwd, "")
		if err != nil {
			t.Fatal(err)
		}
		if scope.Mode != ApplySingle || len(scope.Instances) != 1 || scope.Instances[0] != filepath.Join(linkedRoot, "ws-1") {
			t.Errorf("got mode=%d instances=%v, want ApplySingle on ws-1", scope.Mode, scope.Instances)
		}
	})

	t.Run("a symlink under the root into an instance subdirectory refuses", func(t *testing.T) {
		// Lexically the cwd is a directory under the root that is not an
		// instance, so it is refused rather than given either scope.
		cwd := symlink(t, filepath.Join(root, "ws-1", "public"), filepath.Join(root, "into-ws-1"))
		class, err := ClassifyCwd(cwd)
		if err != nil {
			t.Fatal(err)
		}
		if class.Class != CwdBelowWorkspaceRoot {
			t.Errorf("class = %s, want below-workspace-root", class.Class)
		}
		if scope, err := ResolveApplyScope(cwd, ""); err == nil {
			t.Errorf("want a refusal, got mode=%d over %v", scope.Mode, scope.Instances)
		}
	})

	t.Run("a symlink under the root to an instance is that instance", func(t *testing.T) {
		cwd := symlink(t, filepath.Join(root, "ws-2"), filepath.Join(root, "alias-ws-2"))
		scope, err := ResolveApplyScope(cwd, "")
		if err != nil {
			t.Fatal(err)
		}
		if scope.Mode != ApplySingle || len(scope.Instances) != 1 || scope.Instances[0] != cwd {
			t.Errorf("got mode=%d instances=%v, want ApplySingle on %s", scope.Mode, scope.Instances, cwd)
		}
	})

	t.Run("a symlink outside the workspace into a stray directory gets no scope", func(t *testing.T) {
		// Lexically the cwd is outside every workspace.
		cwd := symlink(t, stray, filepath.Join(outside, "to-stray"))
		class, err := ClassifyCwd(cwd)
		if err != nil {
			t.Fatal(err)
		}
		if class.Class != CwdOutside {
			t.Errorf("class = %s, want outside", class.Class)
		}
		if scope, err := ResolveApplyScope(cwd, ""); err == nil {
			t.Errorf("want an error, got mode=%d over %v", scope.Mode, scope.Instances)
		}
	})
}
