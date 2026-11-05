package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/watch"
)

// readReviewSettingsDoc parses an instance's .claude/settings.json.
func readReviewSettingsDoc(t *testing.T, inst string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(inst, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("reading review settings: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing review settings: %v", err)
	}
	return doc
}

// preToolUseCommands returns the commands of the PreToolUse entries with matcher.
func preToolUseCommands(doc map[string]any, matcher string) []string {
	hooks, _ := doc["hooks"].(map[string]any)
	entries, _ := hooks["PreToolUse"].([]any)
	var cmds []string
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if s, _ := m["matcher"].(string); s != matcher {
			continue
		}
		inner, _ := m["hooks"].([]any)
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			if c, _ := hm["command"].(string); c != "" {
				cmds = append(cmds, c)
			}
		}
	}
	return cmds
}

// containsWith reports whether some command contains every one of parts.
func containsWith(cmds []string, parts ...string) bool {
	for _, c := range cmds {
		ok := true
		for _, p := range parts {
			if !strings.Contains(c, p) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// assertReviewSettingsCarryDenies checks an instance's settings after a stage or a
// continuation: they pass VerifyReviewSettings for the posture they were written in,
// hold the session-command deny, and, when sandboxed, the filesystem guard with this
// instance's --root.
func assertReviewSettingsCarryDenies(t *testing.T, inst string, sandbox bool) map[string]any {
	t.Helper()
	doc := readReviewSettingsDoc(t, inst)
	ask := false
	if perms, ok := doc["permissions"].(map[string]any); ok && perms["defaultMode"] == "default" {
		ask = true
	}
	if err := watch.VerifyReviewSettings(doc, inst, sandbox, ask); err != nil {
		t.Errorf("review settings do not verify: %v", err)
	}
	bash := preToolUseCommands(doc, "Bash")
	if !containsWith(bash, "watch guard-bash") {
		t.Errorf("review settings carry no session-command deny; Bash hooks: %q", bash)
	}
	if !containsWith(bash, "gh[[:space:]]+pr") {
		t.Errorf("review settings carry no post-guard; Bash hooks: %q", bash)
	}
	fs := preToolUseCommands(doc, "Write|Edit|MultiEdit|NotebookEdit")
	if sandbox {
		if !containsWith(fs, "watch guard-fs", "--root '"+inst+"'") {
			t.Errorf("sandboxed review settings carry no filesystem guard for %s; got %q", inst, fs)
		}
	} else if len(fs) != 0 {
		t.Errorf("unsandboxed review settings carry a filesystem guard: %q", fs)
	}
	return doc
}

// TestWatchReviewSettings_StageAndContinueCarryNewDenies: a fresh stage and a
// continuation, sandboxed and not, both leave the instance's settings verifying
// with the session-command deny (and, sandboxed, this instance's filesystem guard),
// and a continuation whose settings hold an "exit 0" impostor on the Bash matcher
// ends with niwa's real post-guard and session-command deny beside it.
func TestWatchReviewSettings_StageAndContinueCarryNewDenies(t *testing.T) {
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
			assertReviewSettingsCarryDenies(t, fakes.provisioned.path, tc.plan.sandbox)
		})

		t.Run("continue/"+tc.name, func(t *testing.T) {
			root, home := setupWatchCharEnv(t)
			installWatchCharFakes(t)
			stubWatchTrust(t, home, tc.unparseable)
			client := newPullHeadServer(t)
			cmd, stdout, _ := newWatchCharCmd()

			inst := filepath.Join(root, "test-ws+watch_acme_widget_7-4e33acfa")
			if err := writeMinimalInstanceState(inst); err != nil {
				t.Fatal(err)
			}
			// The instance's settings hold an impostor on the Bash matcher.
			claudeDir := filepath.Join(inst, ".claude")
			if err := os.MkdirAll(claudeDir, 0o755); err != nil {
				t.Fatal(err)
			}
			impostor := `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "exit 0"}]}]}}`
			if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(impostor), 0o644); err != nil {
				t.Fatal(err)
			}
			jobsDir := filepath.Join(home, ".claude", "jobs")
			writeJobStateFile(t, jobsDir, watchCharShortID, watchCharSessionID, inst, nil)
			rec := watch.StagedRecord{
				Handle:        "watch_acme_widget_7",
				Owner:         "acme",
				Repo:          "widget",
				Number:        7,
				URL:           watchCharPRURL,
				InstancePath:  inst,
				DispatchedSHA: watchCharOldSHA,
				SessionID:     watchCharSessionID,
				ShortID:       watchCharShortID,
			}
			if err := watch.SaveStagedRecord(root, rec); err != nil {
				t.Fatal(err)
			}
			if !sessionLive(jobsDir, watchCharSessionID, time.Now()) || !instanceHasLiveJob(jobsDir, inst) {
				t.Fatal("precondition: the review session must read as live")
			}

			if err := continueReview(cmd, root, root, "", client, watchCharPR, rec, tc.plan); err != nil {
				t.Fatalf("continueReview: %v", err)
			}
			if strings.Contains(stdout.String(), "skipping continuation") {
				t.Fatalf("continuation was skipped:\n%s", stdout.String())
			}
			doc := assertReviewSettingsCarryDenies(t, inst, tc.plan.sandbox)
			kept := false
			for _, c := range preToolUseCommands(doc, "Bash") {
				kept = kept || c == "exit 0"
			}
			if !kept {
				t.Errorf("the impostor entry must be kept beside niwa's hooks; Bash hooks: %q", preToolUseCommands(doc, "Bash"))
			}
		})
	}
}
