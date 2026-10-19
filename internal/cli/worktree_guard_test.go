package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// guardInstance builds an instance root with repos niwa and koto and returns
// its path.
func guardInstance(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, repo := range []string{"niwa", "koto"} {
		if err := os.MkdirAll(filepath.Join(root, "public", repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := workspace.SaveState(root, &workspace.InstanceState{SchemaVersion: workspace.SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	return root
}

func runGuard(t *testing.T, payload map[string]any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := runWorktreeGuard(bytes.NewReader(raw), &stderr)
	return code, stderr.String()
}

func TestRunWorktreeGuard(t *testing.T) {
	root := guardInstance(t)
	t.Setenv("CLAUDE_PROJECT_DIR", root)

	code, stderr := runGuard(t, map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]string{"command": "git worktree add ../x"},
		"cwd":        root,
	})
	if code != guardBlockExit {
		t.Fatalf("git worktree add: exit %d, want %d", code, guardBlockExit)
	}
	for _, want := range []string{"use EnterWorktree with name", "koto", "niwa", "koto/<slug>"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q lacks %q", stderr, want)
		}
	}

	code, _ = runGuard(t, map[string]any{
		"tool_name":  "EnterWorktree",
		"tool_input": map[string]string{"path": filepath.Join(root, "public", "niwa", ".claude", "worktrees", "x")},
		"cwd":        root,
	})
	if code != guardBlockExit {
		t.Errorf("path-form EnterWorktree: exit %d, want %d", code, guardBlockExit)
	}

	for _, p := range []map[string]any{
		{"tool_name": "Bash", "tool_input": map[string]string{"command": "git worktree list"}, "cwd": root},
		{"tool_name": "Bash", "tool_input": map[string]string{"command": "ls"}, "cwd": root},
		{"tool_name": "EnterWorktree", "tool_input": map[string]string{"name": "niwa/x"}, "cwd": root},
		{"tool_name": "Read", "tool_input": map[string]string{"file_path": "x"}, "cwd": root},
		{"tool_name": "Bash", "tool_input": map[string]string{"command": "git worktree add x"}},
	} {
		if code, stderr := runGuard(t, p); code != 0 {
			t.Errorf("%v: exit %d (%s), want 0", p, code, stderr)
		}
	}
}

func TestRunWorktreeGuard_FailsOpen(t *testing.T) {
	var stderr bytes.Buffer
	if code := runWorktreeGuard(strings.NewReader("{not json"), &stderr); code != 0 {
		t.Errorf("malformed payload: exit %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "allowing") {
		t.Errorf("malformed payload should explain on stderr, got %q", stderr.String())
	}

	// Outside any niwa instance or workspace: allowed.
	outside := t.TempDir()
	t.Setenv("CLAUDE_PROJECT_DIR", outside)
	code, _ := runGuard(t, map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]string{"command": "git worktree add x"},
		"cwd":        outside,
	})
	if code != 0 {
		t.Errorf("outside any instance: exit %d, want 0", code)
	}
}

func TestRunWorktreeGuard_OptedOutInstance(t *testing.T) {
	root := guardInstance(t)
	if err := workspace.SaveState(root, &workspace.InstanceState{SchemaVersion: workspace.SchemaVersion, NoWorktreeDelegation: true}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	code, _ := runGuard(t, map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]string{"command": "git worktree add x"},
		"cwd":        root,
	})
	if code != 0 {
		t.Errorf("opted-out instance: exit %d, want 0", code)
	}
}
