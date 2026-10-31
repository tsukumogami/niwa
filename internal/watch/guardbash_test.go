package watch

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	testNiwaOnce sync.Once
	testNiwaPath string
	testNiwaErr  error
)

// buildTestNiwa builds the niwa binary once per test run, so a test can run an
// installed hook command exactly as Claude Code would.
func buildTestNiwa(t *testing.T) string {
	t.Helper()
	testNiwaOnce.Do(func() {
		dir, err := os.MkdirTemp("", "niwa-watch-test-bin-")
		if err != nil {
			testNiwaErr = err
			return
		}
		testNiwaPath = filepath.Join(dir, "niwa")
		out, err := exec.Command("go", "build", "-o", testNiwaPath, "github.com/tsukumogami/niwa/cmd/niwa").CombinedOutput()
		if err != nil {
			testNiwaErr = err
			t.Logf("go build: %s", out)
		}
	})
	if testNiwaErr != nil {
		t.Fatalf("building niwa: %v", testNiwaErr)
	}
	return testNiwaPath
}

// useGuardBin points the hook commands at bin for the rest of the test.
func useGuardBin(t *testing.T, bin string) {
	t.Helper()
	orig := guardBinPath
	guardBinPath = func() string { return bin }
	t.Cleanup(func() { guardBinPath = orig })
}

// bashPayload builds a PreToolUse payload for a Bash command.
func bashPayload(t *testing.T, command string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": command, "description": "run it"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// runHookCommand runs a hook command under sh -c with payload on stdin.
func runHookCommand(t *testing.T, command, payload string) (int, string, string) {
	t.Helper()
	c := exec.Command("sh", "-c", command)
	c.Stdin = strings.NewReader(payload)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	code := 0
	if err := c.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running hook: %v", err)
		}
		code = ee.ExitCode()
	}
	return code, stdout.String(), stderr.String()
}

// refusedSessionCommands are review Bash commands the session-command deny refuses.
var refusedSessionCommands = []string{
	"niwa retask w1 \"hi\"",
	"niwa resume w1",
	"niwa dispatch \"task\" --name x",
	"claude --resume 5f0c2d1e-7a3b-4c9d-8e2f-1a2b3c4d5e6f",
	"claude --resume=5f0c2d1e-7a3b-4c9d-8e2f-1a2b3c4d5e6f",
	"claude -r 5f0c2d1e",
	"claude attach abc123",
	"claude stop abc123",
	"claude rm abc123",
	"/usr/local/bin/claude stop abc123",
	"~/.local/bin/niwa retask w1 \"hi\"",
	"FOO=1 claude --resume 5f0c2d1e-7a3b-4c9d-8e2f-1a2b3c4d5e6f",
	"cd x && niwa dispatch \"t\"",
	"true; claude rm abc",
	"echo | claude attach abc",
	"$(claude stop abc)",
	"claude  --resume",
	"claude\tstop abc",
}

// TestSessionCommandDenyHook_Refuses runs the installed hook command, and the
// decision function behind it, against payloads naming each session command in
// bare form, behind a path or an environment prefix, after shell separators, and
// with odd spacing. Each must exit 2 with the refusal line on stderr.
func TestSessionCommandDenyHook_Refuses(t *testing.T) {
	useGuardBin(t, buildTestNiwa(t))
	hook := sessionCommandDenyCommand()
	for _, command := range refusedSessionCommands {
		payload := bashPayload(t, command)
		var stderr strings.Builder
		if code := GuardBashDecision(strings.NewReader(payload), &stderr); code != 2 {
			t.Errorf("decision %q: exit %d, want 2", command, code)
		}
		if stderr.String() != sessionCommandDenyMessage+"\n" {
			t.Errorf("decision %q: stderr = %q, want the refusal line", command, stderr.String())
		}
		code, stdout, hookStderr := runHookCommand(t, hook, payload)
		if code != 2 {
			t.Errorf("hook %q: exit %d, want 2", command, code)
		}
		if hookStderr != sessionCommandDenyMessage+"\n" {
			t.Errorf("hook %q: stderr = %q, want the refusal line", command, hookStderr)
		}
		if stdout != "" {
			t.Errorf("hook %q: stdout = %q, want empty", command, stdout)
		}
	}
	if sessionCommandDenyMessage != "niwa watch: review sessions don't run session commands" {
		t.Errorf("refusal message changed: %q", sessionCommandDenyMessage)
	}
}

// TestSessionCommandDenyHook_AllowsOrdinaryCommands: ordinary review commands,
// including ones that mention the verbs or programs in other positions, run.
func TestSessionCommandDenyHook_AllowsOrdinaryCommands(t *testing.T) {
	useGuardBin(t, buildTestNiwa(t))
	hook := sessionCommandDenyCommand()
	for _, command := range []string{
		"go test ./...",
		"git log --oneline",
		"niwa status",
		"claude --version",
		"cat resume.txt",
		"claudette stop",
		"grep -n stopSession internal/cli/watch.go",
	} {
		payload := bashPayload(t, command)
		var stderr strings.Builder
		if code := GuardBashDecision(strings.NewReader(payload), &stderr); code != 0 {
			t.Errorf("decision %q: exit %d, want 0 (stderr %q)", command, code, stderr.String())
		}
		if code, _, hookStderr := runHookCommand(t, hook, payload); code != 0 {
			t.Errorf("hook %q: exit %d, want 0 (stderr %q)", command, code, hookStderr)
		}
	}
}

// TestSessionCommandDenyHook_FailsClosed: unreadable input, a payload without a
// command, and a missing niwa binary all block.
func TestSessionCommandDenyHook_FailsClosed(t *testing.T) {
	for name, payload := range map[string]string{
		"not json":   "not json",
		"empty":      "",
		"no command": `{"tool_name":"Bash","tool_input":{}}`,
	} {
		if code := GuardBashDecision(strings.NewReader(payload), io.Discard); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
	useGuardBin(t, filepath.Join(t.TempDir(), "no-such-niwa"))
	if code, _, _ := runHookCommand(t, sessionCommandDenyCommand(), bashPayload(t, "git status")); code != 2 {
		t.Errorf("missing niwa binary: exit %d, want 2", code)
	}
}

// TestSessionCommandDenyHook_DrainsStdin proves the installed hook reads its whole
// payload before exiting, as TestSessionReachDenyHook_DrainsStdin does for the
// session-reach deny: a payload larger than a pipe buffer is written in full and
// the hook still exits 2. It covers the guard refusing and the guard binary missing.
func TestSessionCommandDenyHook_DrainsStdin(t *testing.T) {
	payload := bashPayload(t, "claude stop abc # "+strings.Repeat("x", 300*1024))

	run := func(command string) (writeErr error, exitCode int) {
		t.Helper()
		c := exec.Command("sh", "-c", command)
		stdin, err := c.StdinPipe()
		if err != nil {
			t.Fatalf("stdin pipe: %v", err)
		}
		if err := c.Start(); err != nil {
			t.Fatalf("starting hook: %v", err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := io.WriteString(stdin, payload)
			if cerr := stdin.Close(); err == nil {
				err = cerr
			}
			done <- err
		}()
		writeErr = <-done
		if err := c.Wait(); err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("waiting for hook: %v", err)
			}
			exitCode = ee.ExitCode()
		}
		return writeErr, exitCode
	}

	useGuardBin(t, buildTestNiwa(t))
	if werr, code := run(sessionCommandDenyCommand()); werr != nil || code != 2 {
		t.Errorf("hook: write error = %v, exit = %d; want the whole payload read and exit 2", werr, code)
	}

	useGuardBin(t, filepath.Join(t.TempDir(), "no-such-niwa"))
	if werr, code := run(sessionCommandDenyCommand()); werr != nil || code != 2 {
		t.Errorf("hook with no niwa binary: write error = %v, exit = %d; want the whole payload read and exit 2", werr, code)
	}
}

// TestApplyReviewSettings_SessionCommandDenyEveryMode applies and verifies the
// session-command deny in every mode, shows that dropping it fails verification,
// and that a settings document from before the hook existed (the post-guard alone
// on Bash) gets it added.
func TestApplyReviewSettings_SessionCommandDenyEveryMode(t *testing.T) {
	for _, mode := range reviewModes {
		t.Run(mode.name, func(t *testing.T) {
			inst := t.TempDir()
			if err := ApplyReviewSettings(inst, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("ApplyReviewSettings: %v", err)
			}
			got := readSettings(t, inst)
			if n := countPreToolUseHook(got, sessionCommandDenyMatcher, sessionCommandDenyCommand()); n != 1 {
				t.Errorf("session-command deny must be present exactly once, got %d", n)
			}
			if err := VerifyReviewSettings(got, inst, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("applied settings must verify: %v", err)
			}
			without := fullReviewDoc(inst, mode.sandbox, mode.ask, "session-command deny")
			err := VerifyReviewSettings(without, inst, mode.sandbox, mode.ask)
			if err == nil || !strings.Contains(err.Error(), `session-command deny PreToolUse hook (matcher "Bash") missing`) {
				t.Errorf("settings without the deny: error = %v", err)
			}

			pre := t.TempDir()
			writeSettingsDoc(t, pre, fullReviewDoc(pre, mode.sandbox, mode.ask, "session-command deny"))
			if err := ApplyReviewSettings(pre, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("ApplyReviewSettings on pre-hook settings: %v", err)
			}
			if n := countPreToolUseHook(readSettings(t, pre), sessionCommandDenyMatcher, sessionCommandDenyCommand()); n != 1 {
				t.Errorf("re-apply must add the session-command deny exactly once, got %d", n)
			}
		})
	}
}
