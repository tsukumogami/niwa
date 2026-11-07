package watch

import (
	"bytes"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// runMCPDenyCommand runs command under sh with payload on stdin, written through
// the test's own pipe so a write error is seen (os/exec swallows one). It returns
// the write error, the exit code, stdout and stderr.
func runMCPDenyCommand(t *testing.T, command, payload string) (writeErr error, code int, stdout, stderr string) {
	t.Helper()
	c := exec.Command("sh", "-c", command)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
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
		code = ee.ExitCode()
	}
	return writeErr, code, out.String(), errb.String()
}

// TestMCPDenyMatcher_CoversEveryMCPTool pins the matcher to the regular-expression
// form Claude Code reads for a matcher starting with "^", and checks it matches MCP
// tool names and none of the tools a review relies on.
func TestMCPDenyMatcher_CoversEveryMCPTool(t *testing.T) {
	if mcpDenyMatcher != "^mcp__" {
		t.Fatalf("mcpDenyMatcher = %q, want ^mcp__", mcpDenyMatcher)
	}
	re := regexp.MustCompile(mcpDenyMatcher)
	for _, tool := range []string{"mcp__github__create_issue", "mcp__niwa-live-stub__record_call", "mcp__x__y"} {
		if !re.MatchString(tool) {
			t.Errorf("matcher must cover %q", tool)
		}
	}
	for _, tool := range append([]string{"SendMessage", "ListAgents", "Task"}, reviewToolNames...) {
		if re.MatchString(tool) {
			t.Errorf("matcher must not cover %q", tool)
		}
	}
	if mcpDenyMessage != "niwa watch: review sessions don't call MCP tools" {
		t.Errorf("refusal message changed: %q", mcpDenyMessage)
	}
}

// TestApplyReviewSettings_MCPDenyEveryMode applies the settings in every mode and
// checks the MCP deny is there exactly once by matcher and command, that the result
// verifies, and that dropping the hook fails verification naming it.
func TestApplyReviewSettings_MCPDenyEveryMode(t *testing.T) {
	for _, mode := range reviewModes {
		t.Run(mode.name, func(t *testing.T) {
			inst := t.TempDir()
			if err := ApplyReviewSettings(inst, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("ApplyReviewSettings: %v", err)
			}
			got := readSettings(t, inst)
			if n := countPreToolUseHook(got, mcpDenyMatcher, mcpDenyCommand()); n != 1 {
				t.Errorf("MCP deny must be present exactly once, got %d", n)
			}
			if err := VerifyReviewSettings(got, inst, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("applied settings must verify: %v", err)
			}
			err := VerifyReviewSettings(fullReviewDoc(inst, mode.sandbox, mode.ask, "MCP deny"), inst, mode.sandbox, mode.ask)
			if err == nil {
				t.Fatal("settings without the MCP deny must fail verification")
			}
			if want := `MCP deny PreToolUse hook (matcher "^mcp__") missing`; !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to contain %q", err, want)
			}
		})
	}
}

// TestMCPDeny_ImpostorCannotStandIn: in every mode, an entry with the ^mcp__ matcher
// running exit 0 fails verification, and re-applying keeps it and adds niwa's hook
// beside it, after which the settings verify.
func TestMCPDeny_ImpostorCannotStandIn(t *testing.T) {
	for _, mode := range reviewModes {
		t.Run(mode.name, func(t *testing.T) {
			inst := t.TempDir()
			doc := fullReviewDoc(inst, mode.sandbox, mode.ask, "MCP deny", commandHook(mcpDenyMatcher, "exit 0"))
			if err := VerifyReviewSettings(doc, inst, mode.sandbox, mode.ask); err == nil || !strings.Contains(err.Error(), "MCP deny") {
				t.Fatalf("an exit 0 entry on the MCP matcher must fail verification naming the MCP deny, got %v", err)
			}
			writeSettingsDoc(t, inst, doc)
			if err := ApplyReviewSettings(inst, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("ApplyReviewSettings with an impostor present: %v", err)
			}
			got := readSettings(t, inst)
			if !hasPreToolUseHook(got, mcpDenyMatcher, "exit 0") {
				t.Error("apply must keep the impostor entry")
			}
			if n := countPreToolUseHook(got, mcpDenyMatcher, mcpDenyCommand()); n != 1 {
				t.Errorf("niwa's MCP deny must be added once beside the impostor, got %d", n)
			}
			if err := VerifyReviewSettings(got, inst, mode.sandbox, mode.ask); err != nil {
				t.Errorf("settings must verify once niwa's hook is beside the impostor: %v", err)
			}
		})
	}
}

// TestMCPDenyHook_CommandRefuses runs the command the applied settings carry, in
// every mode, against an MCP tool's PreToolUse payload and against empty stdin: each
// exits 2 with exactly the refusal line on stderr and nothing on stdout.
func TestMCPDenyHook_CommandRefuses(t *testing.T) {
	for _, mode := range reviewModes {
		t.Run(mode.name, func(t *testing.T) {
			inst := t.TempDir()
			if err := ApplyReviewSettings(inst, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("ApplyReviewSettings: %v", err)
			}
			cmd := preToolUseCommand(t, readSettings(t, inst), mcpDenyMatcher)
			if cmd == "" {
				t.Fatal("applied settings carry no MCP deny command")
			}
			for name, payload := range map[string]string{
				"mcp tool":    `{"hook_event_name":"PreToolUse","tool_name":"mcp__github__create_issue","tool_input":{"title":"x"}}`,
				"empty stdin": "",
			} {
				_, code, stdout, stderr := runMCPDenyCommand(t, cmd, payload)
				if code != 2 {
					t.Errorf("%s: exit code = %d, want 2", name, code)
				}
				if stderr != mcpDenyMessage+"\n" {
					t.Errorf("%s: stderr = %q, want %q", name, stderr, mcpDenyMessage+"\n")
				}
				if stdout != "" {
					t.Errorf("%s: stdout must be empty, got %q", name, stdout)
				}
			}
		})
	}
}

// TestMCPDenyHook_DrainsStdin: a payload larger than a pipe buffer is read to the end
// and the hook still exits 2. A control run without the drain fails the write, which
// shows the check tells the two apart.
func TestMCPDenyHook_DrainsStdin(t *testing.T) {
	payload := `{"hook_event_name":"PreToolUse","tool_name":"mcp__github__create_issue","tool_input":{"body":"` +
		strings.Repeat("x", 300*1024) + `"}}`
	deny := mcpDenyCommand()
	if werr, code, _, _ := runMCPDenyCommand(t, deny, payload); werr != nil || code != 2 {
		t.Errorf("MCP deny: write error = %v, exit = %d; want the whole payload read and exit 2", werr, code)
	}
	control := strings.TrimPrefix(deny, "cat >/dev/null 2>&1; ")
	if control == deny {
		t.Fatalf("MCP deny command no longer starts with the stdin drain: %q", deny)
	}
	if werr, code, _, _ := runMCPDenyCommand(t, control, payload); werr == nil || code != 2 {
		t.Errorf("control without the drain: write error = %v, exit = %d; want a failed write and exit 2", werr, code)
	}
}
