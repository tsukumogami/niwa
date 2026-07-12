package watch

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// decide feeds a hook payload to GuardFSDecision with CLAUDE_PROJECT_DIR set to
// root (and no explicit --root override), and returns the exit code (0 allow, 2
// deny).
func decide(t *testing.T, root, payload string) int {
	t.Helper()
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	return GuardFSDecision(strings.NewReader(payload), &strings.Builder{}, "")
}

func TestGuardFSDecision_AllowsInsideInstance(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name    string
		payload string
	}{
		{"relative draft", `{"tool_name":"Write","tool_input":{"file_path":"watch-review-draft.md"}}`},
		{"relative clone file", `{"tool_name":"Write","tool_input":{"file_path":"pr-clone/notes.txt"}}`},
		{"absolute under root", `{"tool_name":"Edit","tool_input":{"file_path":"` + filepath.Join(root, "pr-clone", "a.go") + `"}}`},
		{"multiedit under root", `{"tool_name":"MultiEdit","tool_input":{"file_path":"pr-clone/b.go"}}`},
		{"notebook under root", `{"tool_name":"NotebookEdit","tool_input":{"notebook_path":"pr-clone/nb.ipynb"}}`},
		{"root itself", `{"tool_name":"Write","tool_input":{"file_path":"` + root + `"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decide(t, root, tc.payload); got != 0 {
				t.Errorf("in-instance write must be allowed (exit 0), got %d", got)
			}
		})
	}
}

func TestGuardFSDecision_DeniesOutsideInstance(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir() // stand-in for a path outside the instance
	cases := []struct {
		name    string
		payload string
	}{
		{"absolute escape (authorized_keys)", `{"tool_name":"Write","tool_input":{"file_path":"` + filepath.Join(home, ".ssh", "authorized_keys") + `"}}`},
		{"dotdot escape", `{"tool_name":"Write","tool_input":{"file_path":"../escape"}}`},
		{"nested dotdot escape", `{"tool_name":"Edit","tool_input":{"file_path":"pr-clone/../../escape"}}`},
		{"multiedit escape", `{"tool_name":"MultiEdit","tool_input":{"file_path":"` + filepath.Join(home, "victim.go") + `"}}`},
		{"sibling prefix trap", `{"tool_name":"Write","tool_input":{"file_path":"` + root + `-sibling/x"}}`},
		{"notebook escape", `{"tool_name":"NotebookEdit","tool_input":{"notebook_path":"../nb.ipynb"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decide(t, root, tc.payload); got != 2 {
				t.Errorf("out-of-instance write must be denied (exit 2), got %d", got)
			}
		})
	}
}

// TestGuardFSDecision_FailsClosed covers the malformed-input paths: each must
// deny (exit 2) rather than allow.
func TestGuardFSDecision_FailsClosed(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name    string
		payload string
	}{
		{"not json", `not json at all`},
		{"empty object", `{}`},
		{"missing target path", `{"tool_name":"Write","tool_input":{"content":"x"}}`},
		{"empty file_path", `{"tool_name":"Write","tool_input":{"file_path":""}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decide(t, root, tc.payload); got != 2 {
				t.Errorf("malformed input must fail closed (exit 2), got %d", got)
			}
		})
	}
}

// TestGuardFSDecision_SymlinkEscape covers an in-instance directory symlinked to
// a location outside the instance: a write through it must be denied even though
// the textual path is under the instance root.
func TestGuardFSDecision_SymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows; guard targets Linux/macOS")
	}
	root := t.TempDir()
	outside := t.TempDir()
	// root/escape -> outside (a symlinked directory inside the instance).
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}
	// A textual path under the instance root that resolves outside via the symlink.
	payload := `{"tool_name":"Write","tool_input":{"file_path":"escape/authorized_keys"}}`
	if got := decide(t, root, payload); got != 2 {
		t.Errorf("write through an in-instance symlink to outside must be denied (exit 2), got %d", got)
	}
	// A real in-instance write still allowed (soundness: the symlink check does not
	// over-deny legitimate writes).
	if got := decide(t, root, `{"tool_name":"Write","tool_input":{"file_path":"pr-clone/ok.txt"}}`); got != 0 {
		t.Errorf("legitimate in-instance write must remain allowed (exit 0), got %d", got)
	}
}

// TestGuardFSDecision_RootFromPayloadCwd verifies the fallback to the hook
// payload's cwd when CLAUDE_PROJECT_DIR is unset.
func TestGuardFSDecision_RootFromPayloadCwd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	payloadIn := `{"tool_name":"Write","tool_input":{"file_path":"draft.md"},"cwd":"` + root + `"}`
	if got := GuardFSDecision(strings.NewReader(payloadIn), &strings.Builder{}, ""); got != 0 {
		t.Errorf("in-instance write (root from payload cwd) must be allowed, got %d", got)
	}
	payloadOut := `{"tool_name":"Write","tool_input":{"file_path":"/etc/passwd"},"cwd":"` + root + `"}`
	if got := GuardFSDecision(strings.NewReader(payloadOut), &strings.Builder{}, ""); got != 2 {
		t.Errorf("out-of-instance write (root from payload cwd) must be denied, got %d", got)
	}
}

// TestGuardFSDecision_RootOverrideWins verifies that the explicit --root (baked in
// by the review-session hook) is authoritative and is NOT widened by a broader
// CLAUDE_PROJECT_DIR: a write inside the wider parent but outside the instance root
// must still be denied.
func TestGuardFSDecision_RootOverrideWins(t *testing.T) {
	parent := t.TempDir()
	inst := filepath.Join(parent, "instance")
	if err := os.MkdirAll(inst, 0o755); err != nil {
		t.Fatal(err)
	}
	// CLAUDE_PROJECT_DIR points at the WIDER parent; --root pins the narrow instance.
	t.Setenv("CLAUDE_PROJECT_DIR", parent)

	// A write to the parent (outside inst, but inside the wider CLAUDE_PROJECT_DIR)
	// must be DENIED because --root wins.
	outside := `{"tool_name":"Write","tool_input":{"file_path":"` + filepath.Join(parent, "escape.txt") + `"}}`
	if got := GuardFSDecision(strings.NewReader(outside), &strings.Builder{}, inst); got != 2 {
		t.Errorf("write into the wider CLAUDE_PROJECT_DIR but outside --root must be denied (exit 2), got %d", got)
	}
	// A write inside the instance root is still allowed.
	inside := `{"tool_name":"Edit","tool_input":{"file_path":"` + filepath.Join(inst, "draft.md") + `"}}`
	if got := GuardFSDecision(strings.NewReader(inside), &strings.Builder{}, inst); got != 0 {
		t.Errorf("write inside --root must be allowed (exit 0), got %d", got)
	}
}
