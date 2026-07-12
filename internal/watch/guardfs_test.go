package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// decide feeds a hook payload to GuardFSDecision in the hard-deny posture
// (askOutside=false) with CLAUDE_PROJECT_DIR set to root (and no explicit --root
// override), and returns the exit code (0 allow, 2 deny).
func decide(t *testing.T, root, payload string) int {
	t.Helper()
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	return GuardFSDecision(strings.NewReader(payload), &strings.Builder{}, &strings.Builder{}, "", false)
}

// decideAsk feeds a hook payload to GuardFSDecision in the operator-approval posture
// (askOutside=true) with CLAUDE_PROJECT_DIR set to root, and returns the exit code
// plus whatever decision object was printed to stdout.
func decideAsk(t *testing.T, root, payload string) (int, string) {
	t.Helper()
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	var stdout strings.Builder
	code := GuardFSDecision(strings.NewReader(payload), &stdout, &strings.Builder{}, "", true)
	return code, stdout.String()
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

// TestGuardFSDecision_AskOutside_AllowsInstanceWithDecision verifies that in the
// operator-approval posture an in-instance write is emitted as an explicit "allow"
// decision on stdout (so a non-bypass mode auto-approves it) and exits 0.
func TestGuardFSDecision_AskOutside_AllowsInstanceWithDecision(t *testing.T) {
	root := t.TempDir()
	code, out := decideAsk(t, root, `{"tool_name":"Write","tool_input":{"file_path":"pr-clone/draft.md"}}`)
	if code != 0 {
		t.Fatalf("in-instance write in ask posture must exit 0, got %d", code)
	}
	if got := decisionOf(t, out); got != "allow" {
		t.Errorf("in-instance write must emit permissionDecision \"allow\", got %q (stdout=%q)", got, out)
	}
}

// TestGuardFSDecision_AskOutside_AsksOutsideInstance verifies that in the
// operator-approval posture an out-of-instance write is surfaced as an "ask"
// decision on stdout (blocks pending operator approval) and exits 0 -- the upgrade
// from the hard-deny posture's exit 2.
func TestGuardFSDecision_AskOutside_AsksOutsideInstance(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	cases := []struct {
		name    string
		payload string
	}{
		{"absolute escape", `{"tool_name":"Write","tool_input":{"file_path":"` + filepath.Join(home, ".ssh", "authorized_keys") + `"}}`},
		{"dotdot escape", `{"tool_name":"Edit","tool_input":{"file_path":"../escape"}}`},
		{"notebook escape", `{"tool_name":"NotebookEdit","tool_input":{"notebook_path":"../nb.ipynb"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := decideAsk(t, root, tc.payload)
			if code != 0 {
				t.Fatalf("out-of-instance write in ask posture must exit 0 (decision on stdout), got %d", code)
			}
			if got := decisionOf(t, out); got != "ask" {
				t.Errorf("out-of-instance write must emit permissionDecision \"ask\", got %q (stdout=%q)", got, out)
			}
		})
	}
}

// TestGuardFSDecision_AskOutside_FailsClosed verifies that even in the
// operator-approval posture a malformed or undeterminable payload is a hard deny
// (exit 2) with NO ask/allow emitted -- the escape can only become an ask for a
// cleanly-resolved target.
func TestGuardFSDecision_AskOutside_FailsClosed(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name    string
		payload string
	}{
		{"not json", `not json at all`},
		{"empty object", `{}`},
		{"missing target path", `{"tool_name":"Write","tool_input":{"content":"x"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := decideAsk(t, root, tc.payload)
			if code != 2 {
				t.Errorf("malformed input must fail closed (exit 2) even in ask posture, got %d", code)
			}
			if strings.TrimSpace(out) != "" {
				t.Errorf("fail-closed path must emit no decision object, got stdout %q", out)
			}
		})
	}
}

// decisionOf parses a PreToolUse decision object and returns its permissionDecision.
func decisionOf(t *testing.T, out string) string {
	t.Helper()
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	var d struct {
		HookSpecificOutput struct {
			HookEventName      string `json:"hookEventName"`
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("stdout is not a valid decision object: %v (%q)", err, out)
	}
	if d.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("decision hookEventName must be PreToolUse, got %q", d.HookSpecificOutput.HookEventName)
	}
	return d.HookSpecificOutput.PermissionDecision
}

// TestGuardFSDecision_RootFromPayloadCwd verifies the fallback to the hook
// payload's cwd when CLAUDE_PROJECT_DIR is unset.
func TestGuardFSDecision_RootFromPayloadCwd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	payloadIn := `{"tool_name":"Write","tool_input":{"file_path":"draft.md"},"cwd":"` + root + `"}`
	if got := GuardFSDecision(strings.NewReader(payloadIn), &strings.Builder{}, &strings.Builder{}, "", false); got != 0 {
		t.Errorf("in-instance write (root from payload cwd) must be allowed, got %d", got)
	}
	payloadOut := `{"tool_name":"Write","tool_input":{"file_path":"/etc/passwd"},"cwd":"` + root + `"}`
	if got := GuardFSDecision(strings.NewReader(payloadOut), &strings.Builder{}, &strings.Builder{}, "", false); got != 2 {
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
	if got := GuardFSDecision(strings.NewReader(outside), &strings.Builder{}, &strings.Builder{}, inst, false); got != 2 {
		t.Errorf("write into the wider CLAUDE_PROJECT_DIR but outside --root must be denied (exit 2), got %d", got)
	}
	// A write inside the instance root is still allowed.
	inside := `{"tool_name":"Edit","tool_input":{"file_path":"` + filepath.Join(inst, "draft.md") + `"}}`
	if got := GuardFSDecision(strings.NewReader(inside), &strings.Builder{}, &strings.Builder{}, inst, false); got != 0 {
		t.Errorf("write inside --root must be allowed (exit 0), got %d", got)
	}
}
