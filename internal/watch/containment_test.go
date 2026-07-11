package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func askContains(ask []any, rule string) int {
	n := 0
	for _, v := range ask {
		if s, ok := v.(string); ok && s == rule {
			n++
		}
	}
	return n
}

// TestSandboxProfile_ShapeAndVerify: the sandbox profile carries the no-egress
// stanza and the post-guard ask rules, sets no defaultMode, and verifies.
func TestSandboxProfile_ShapeAndVerify(t *testing.T) {
	profile := SandboxProfile()
	if err := VerifyReviewSettings(profile, true); err != nil {
		t.Fatalf("freshly-built profile should verify: %v", err)
	}
	perms, ok := profile["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("profile missing permissions stanza: %v", profile)
	}
	if _, present := perms["defaultMode"]; present {
		t.Error("sandbox profile must not set permissions.defaultMode")
	}
	ask, _ := perms["ask"].([]any)
	if askContains(ask, "Bash(gh pr review:*)") == 0 || askContains(ask, "Bash(gh pr comment:*)") == 0 {
		t.Errorf("profile missing post-guard ask rules, got %v", ask)
	}
}

// TestApplyReviewSettings_Sandbox writes and verifies the sandbox stanza,
// overwrites a permissive pre-existing sandbox, preserves unrelated keys, and
// adds the post-guard without setting a fail-closed permission mode.
func TestApplyReviewSettings_Sandbox(t *testing.T) {
	inst := t.TempDir()
	claudeDir := filepath.Join(inst, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{
	  "hooks": {"x": 1},
	  "permissions": {"deny": ["Bash(rm:*)"], "ask": ["Bash(rm:*)"]},
	  "sandbox": {"enabled": false, "network": {"allowedDomains": ["evil.example.com"]}}
	}`
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ApplyReviewSettings(inst, true); err != nil {
		t.Fatalf("ApplyReviewSettings: %v", err)
	}

	data, _ := os.ReadFile(settingsPath)
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	// The permissive sandbox was overwritten to the no-egress profile.
	if err := VerifyReviewSettings(got, true); err != nil {
		t.Errorf("post-merge settings do not verify: %v", err)
	}
	// Unrelated keys preserved.
	if _, ok := got["hooks"]; !ok {
		t.Error("unrelated 'hooks' key was dropped")
	}
	perms := got["permissions"].(map[string]any)
	// No fail-closed permission mode is imposed.
	if _, present := perms["defaultMode"]; present {
		t.Error("ApplyReviewSettings must not set permissions.defaultMode")
	}
	// Pre-existing deny list preserved.
	if _, ok := perms["deny"]; !ok {
		t.Error("pre-existing permissions.deny was dropped")
	}
	// Pre-existing ask rule preserved alongside the post-guard, no duplicates.
	ask, _ := perms["ask"].([]any)
	if askContains(ask, "Bash(rm:*)") != 1 {
		t.Error("pre-existing ask rule must be preserved exactly once")
	}
	if askContains(ask, "Bash(gh pr review:*)") != 1 || askContains(ask, "Bash(gh pr comment:*)") != 1 {
		t.Errorf("post-guard rules must be present exactly once, got %v", ask)
	}
}

// TestApplyReviewSettings_NoSandbox writes only the post-guard and leaves no
// sandbox stanza.
func TestApplyReviewSettings_NoSandbox(t *testing.T) {
	inst := t.TempDir()
	if err := ApplyReviewSettings(inst, false); err != nil {
		t.Fatalf("ApplyReviewSettings(false): %v", err)
	}
	data, err := os.ReadFile(filepath.Join(inst, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(data, &got)
	if err := VerifyReviewSettings(got, false); err != nil {
		t.Errorf("written no-sandbox settings do not verify: %v", err)
	}
	if _, present := got["sandbox"]; present {
		t.Error("no-sandbox apply must not write a sandbox stanza")
	}
	perms := got["permissions"].(map[string]any)
	ask, _ := perms["ask"].([]any)
	if askContains(ask, "Bash(gh pr review:*)") == 0 || askContains(ask, "Bash(gh pr comment:*)") == 0 {
		t.Errorf("no-sandbox apply must add the post-guard ask rules, got %v", ask)
	}
}

func TestVerifyReviewSettings_RejectsRelaxations(t *testing.T) {
	// allowedDomains non-empty -> reject (egress would be permitted).
	relaxed := SandboxProfile()
	relaxed["sandbox"].(map[string]any)["network"].(map[string]any)["allowedDomains"] = []any{"evil.example.com"}
	if err := VerifyReviewSettings(relaxed, true); err == nil {
		t.Error("non-empty allowedDomains must be rejected")
	}

	// sandbox disabled -> reject.
	off := SandboxProfile()
	off["sandbox"].(map[string]any)["enabled"] = false
	if err := VerifyReviewSettings(off, true); err == nil {
		t.Error("sandbox.enabled=false must be rejected")
	}

	// failIfUnavailable dropped -> reject (would allow silent fail-open).
	noFail := SandboxProfile()
	noFail["sandbox"].(map[string]any)["failIfUnavailable"] = false
	if err := VerifyReviewSettings(noFail, true); err == nil {
		t.Error("sandbox.failIfUnavailable=false must be rejected")
	}

	// allowUnsandboxedCommands relaxed -> reject (unsandboxed escape hatch).
	escape := SandboxProfile()
	escape["sandbox"].(map[string]any)["allowUnsandboxedCommands"] = true
	if err := VerifyReviewSettings(escape, true); err == nil {
		t.Error("sandbox.allowUnsandboxedCommands=true must be rejected")
	}

	// Missing sandbox stanza (a merge dropped it) -> reject.
	if err := VerifyReviewSettings(map[string]any{"permissions": SandboxProfile()["permissions"]}, true); err == nil {
		t.Error("missing sandbox stanza must be rejected")
	}

	// Missing post-guard ask rules -> reject in both modes.
	if err := VerifyReviewSettings(map[string]any{"permissions": map[string]any{}}, false); err == nil {
		t.Error("missing post-guard ask rules must be rejected")
	}
	noGuard := SandboxProfile()
	noGuard["permissions"].(map[string]any)["ask"] = []any{"Bash(rm:*)"}
	if err := VerifyReviewSettings(noGuard, true); err == nil {
		t.Error("missing post-guard ask rules must be rejected even with a valid sandbox stanza")
	}
}
