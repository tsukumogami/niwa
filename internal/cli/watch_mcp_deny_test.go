package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// watchMCPDenyMatcher is the matcher of the review settings' MCP deny hook. The
// watch package keeps its own constant; this copy is what the review settings on
// disk are checked against, so a change to either shows here.
const watchMCPDenyMatcher = "^mcp__"

// watchMCPDenyMessage is the refusal the MCP deny hook writes to stderr.
const watchMCPDenyMessage = "niwa watch: review sessions don't call MCP tools"

// assertMCPDeny checks that the review settings hold niwa's MCP deny on the ^mcp__
// matcher: some command there, run against an MCP tool's PreToolUse payload, exits 2
// with exactly the refusal line on stderr.
func assertMCPDeny(t *testing.T, doc map[string]any) {
	t.Helper()
	cmds := preToolUseCommands(doc, watchMCPDenyMatcher)
	payload := `{"hook_event_name":"PreToolUse","tool_name":"mcp__github__create_issue","tool_input":{"title":"x"}}`
	for _, c := range cmds {
		run := exec.Command("sh", "-c", c)
		run.Stdin = strings.NewReader(payload)
		var stderr bytes.Buffer
		run.Stderr = &stderr
		var ee *exec.ExitError
		if err := run.Run(); errors.As(err, &ee) && ee.ExitCode() == 2 && stderr.String() == watchMCPDenyMessage+"\n" {
			return
		}
	}
	t.Errorf("review settings carry no MCP deny on %q that refuses an MCP tool call; got %q", watchMCPDenyMatcher, cmds)
}

// TestWatchReviewSettings_StageAndContinueCarryMCPDeny: a fresh stage and a
// continuation, sandboxed and not, both leave the instance's settings verifying
// with niwa's ^mcp__ deny, and a continuation of a review staged with an "exit 0"
// impostor on the ^mcp__ matcher keeps the impostor and ends with niwa's hook
// beside it.
func TestWatchReviewSettings_StageAndContinueCarryMCPDeny(t *testing.T) {
	plans := []struct {
		name        string
		plan        reviewPlan
		unparseable bool
	}{
		{"unsandboxed", reviewPlan{}, false},
		{"sandboxed_ask", reviewPlan{sandbox: true}, false},
		{"sandboxed_deny", reviewPlan{sandbox: true}, true},
	}
	for _, tc := range plans {
		t.Run("stage/"+tc.name, func(t *testing.T) {
			root, home := setupWatchCharEnv(t)
			fakes := installWatchCharFakes(t)
			stubWatchTrust(t, home, tc.unparseable)
			client := newPullHeadServer(t)
			cmd, _, _ := newWatchCharCmd()
			stubDispatchRand(t, constByteReader(0xab))

			if err := stageReview(cmd, root, root, "", client, watchCharPR, tc.plan); err != nil {
				t.Fatalf("stageReview: %v", err)
			}
			doc := assertReviewSettingsCarryDenies(t, fakes.provisioned.path, tc.plan.sandbox)
			assertMCPDeny(t, doc)
		})

		t.Run("continue/"+tc.name, func(t *testing.T) {
			// The impostor is in the instance's settings when the review is
			// staged, so the stage's digest includes it (see
			// TestWatchReviewSettings_StageAndContinueCarryNewDenies).
			f := newWatchContinueFixture(t, tc.plan.sandbox, func(_ *watchContinueFixture, s *watchContinueSetup) {
				s.Unparseable = tc.unparseable
				s.BeforeApply = func(t *testing.T, inst string) {
					claudeDir := filepath.Join(inst, ".claude")
					if err := os.MkdirAll(claudeDir, 0o755); err != nil {
						t.Fatal(err)
					}
					impostor := `{"hooks": {"PreToolUse": [{"matcher": "^mcp__", "hooks": [{"type": "command", "command": "exit 0"}]}]}}`
					if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(impostor), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			})

			out, _, err := f.run(t)
			if err != nil {
				t.Fatalf("continueReview: %v", err)
			}
			if !strings.Contains(out, "niwa watch: continued review for acme/widget#7") {
				t.Fatalf("the continuation didn't deliver:\n%s", out)
			}
			doc := assertReviewSettingsCarryDenies(t, f.inst, tc.plan.sandbox)
			assertMCPDeny(t, doc)
			cmds := preToolUseCommands(doc, watchMCPDenyMatcher)
			kept := false
			for _, c := range cmds {
				kept = kept || c == "exit 0"
			}
			if !kept {
				t.Errorf("the impostor entry must be kept beside niwa's MCP deny; ^mcp__ hooks: %q", cmds)
			}
		})
	}
}
