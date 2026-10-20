package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestApplyReviewSettings_KeepsNiwaWorktreeGuard checks that niwa's root-scope
// worktree guard (matcher "EnterWorktree|Bash") survives ApplyReviewSettings
// and does not stand in for watch's own Bash post-guard, which watch dedupes
// by matcher.
func TestApplyReviewSettings_KeepsNiwaWorktreeGuard(t *testing.T) {
	instance := t.TempDir()
	claudeDir := filepath.Join(instance, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"hooks":{"PreToolUse":[{"matcher":"EnterWorktree|Bash","hooks":[{"type":"command","command":"niwa worktree guard","timeout":5}]}]}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ApplyReviewSettings(instance, false, false); err != nil {
		t.Fatalf("ApplyReviewSettings: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var guard, bash bool
	for _, e := range doc.Hooks.PreToolUse {
		switch e.Matcher {
		case "EnterWorktree|Bash":
			guard = true
		case postGuardMatcher:
			bash = true
		}
	}
	if !guard {
		t.Error("niwa's worktree guard entry was dropped")
	}
	if !bash {
		t.Error("watch's own Bash post-guard was not added; the worktree guard must not stand in for it")
	}
}
