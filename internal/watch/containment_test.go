package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyContainment_MergesAndVerifies(t *testing.T) {
	inst := t.TempDir()
	claudeDir := filepath.Join(inst, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing instance settings with unrelated keys and a permissions.deny
	// list that must be preserved, plus a permissive sandbox that must be
	// overwritten.
	existing := `{
	  "hooks": {"x": 1},
	  "permissions": {"deny": ["Bash(rm:*)"]},
	  "sandbox": {"enabled": false, "network": {"allowedDomains": ["evil.example.com"]}}
	}`
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ApplyContainment(inst, true); err != nil {
		t.Fatalf("ApplyContainment: %v", err)
	}

	data, _ := os.ReadFile(settingsPath)
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	// The permissive sandbox was overwritten to the no-egress profile.
	if err := VerifyContainmentApplied(got, true); err != nil {
		t.Errorf("post-merge settings do not verify: %v", err)
	}
	// Unrelated keys preserved.
	if _, ok := got["hooks"]; !ok {
		t.Error("unrelated 'hooks' key was dropped")
	}
	// The pre-existing deny list is preserved alongside the new defaultMode.
	perms := got["permissions"].(map[string]any)
	if perms["defaultMode"] != "default" {
		t.Errorf("defaultMode = %v, want default", perms["defaultMode"])
	}
	if _, ok := perms["deny"]; !ok {
		t.Error("pre-existing permissions.deny was dropped")
	}
}

func TestApplyContainment_NoExistingSettings(t *testing.T) {
	inst := t.TempDir()
	if err := ApplyContainment(inst, true); err != nil {
		t.Fatalf("ApplyContainment with no existing settings: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(inst, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(data, &got)
	if err := VerifyContainmentApplied(got, true); err != nil {
		t.Errorf("written settings do not verify: %v", err)
	}
}

// TestBuildContainedEnv_CanaryAbsentAndAllowlistSubset is the AC12 canary test:
// a planted secret and credential-bearing variables in the parent env must be
// absent from the contained session env, and the session env must be a subset
// of the allowlist (plus the synthetic HOME).
func TestBuildContainedEnv_CanaryAbsentAndAllowlistSubset(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"HOME=/home/dev", // must be replaced by synthetic HOME
		"ANTHROPIC_API_KEY=sk-model",
		"NIWA_CANARY_SECRET=canary", // planted secret -> must NOT survive
		"GITHUB_TOKEN=ghp_secret",
		"GH_TOKEN=gh_secret",
		"GH_ENTERPRISE_TOKEN=ghe",
		"GITHUB_ACTIONS=true",
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"AWS_SECRET_ACCESS_KEY=aws",
		"LANG=en_US.UTF-8",
	}
	syntheticHome := "/instance/.watch-home"

	env := BuildContainedEnv(parent, syntheticHome)

	got := map[string]string{}
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		got[kv[:eq]] = kv[eq+1:]
	}

	// Allowed survivors.
	if got["PATH"] != "/usr/bin" {
		t.Errorf("PATH not carried: %q", got["PATH"])
	}
	if got["ANTHROPIC_API_KEY"] != "sk-model" {
		t.Errorf("model auth not carried: %q", got["ANTHROPIC_API_KEY"])
	}
	if got["LANG"] != "en_US.UTF-8" {
		t.Errorf("LANG not carried")
	}
	// Synthetic HOME, not the developer's.
	if got["HOME"] != syntheticHome {
		t.Errorf("HOME = %q, want synthetic %q", got["HOME"], syntheticHome)
	}

	// Every denied credential-bearing var and the canary must be absent.
	mustBeAbsent := []string{
		"NIWA_CANARY_SECRET", "GITHUB_TOKEN", "GH_TOKEN", "GH_ENTERPRISE_TOKEN",
		"GITHUB_ACTIONS", "SSH_AUTH_SOCK", "AWS_SECRET_ACCESS_KEY",
	}
	for _, name := range mustBeAbsent {
		if _, present := got[name]; present {
			t.Errorf("contained env must not contain %q", name)
		}
	}
	for _, name := range deniedEnvNames {
		if _, present := got[name]; present {
			t.Errorf("denied env var %q leaked into contained session", name)
		}
	}

	// The env is a subset of the allowlist (+ HOME).
	for name := range got {
		if name == "HOME" {
			continue
		}
		if !envAllowlist[name] {
			t.Errorf("contained env contains non-allowlisted var %q", name)
		}
	}
}

func TestBuildContainedEnv_DropsHomeWhenNoSynthetic(t *testing.T) {
	env := BuildContainedEnv([]string{"HOME=/home/dev", "PATH=/bin"}, "")
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			t.Errorf("HOME must not be carried when no synthetic home is given: %q", kv)
		}
	}
}

func TestContainmentProfile_ShapeAndVerify(t *testing.T) {
	profile := ContainmentProfile(true)
	// A correct profile verifies.
	if err := VerifyContainmentApplied(profile, true); err != nil {
		t.Fatalf("freshly-built profile should verify: %v", err)
	}
}

// TestContainmentProfile_NoSandbox covers the watch_sandbox =
// optional-but-unavailable cell: the profile still enforces the fail-closed
// permission mode but carries no sandbox stanza, and it verifies with
// withSandbox=false.
func TestContainmentProfile_NoSandbox(t *testing.T) {
	profile := ContainmentProfile(false)
	if _, present := profile["sandbox"]; present {
		t.Error("no-sandbox profile must not carry a sandbox stanza")
	}
	perms, ok := profile["permissions"].(map[string]any)
	if !ok || perms["defaultMode"] != "default" {
		t.Errorf("no-sandbox profile must still set the fail-closed permission mode, got %v", profile["permissions"])
	}
	if err := VerifyContainmentApplied(profile, false); err != nil {
		t.Fatalf("no-sandbox profile should verify with withSandbox=false: %v", err)
	}
	// A non-fail-closed permission mode is still rejected without the sandbox.
	profile["permissions"].(map[string]any)["defaultMode"] = "bypassPermissions"
	if err := VerifyContainmentApplied(profile, false); err == nil {
		t.Error("bypassPermissions must be rejected even without the sandbox")
	}
}

// TestApplyContainment_NoSandbox proves the on+optional-but-unavailable path
// writes the fail-closed permission mode without enabling the sandbox stanza.
func TestApplyContainment_NoSandbox(t *testing.T) {
	inst := t.TempDir()
	if err := ApplyContainment(inst, false); err != nil {
		t.Fatalf("ApplyContainment(withSandbox=false): %v", err)
	}
	data, err := os.ReadFile(filepath.Join(inst, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(data, &got)
	if err := VerifyContainmentApplied(got, false); err != nil {
		t.Errorf("written no-sandbox settings do not verify: %v", err)
	}
	if sb, ok := got["sandbox"].(map[string]any); ok {
		if enabled, _ := sb["enabled"].(bool); enabled {
			t.Error("no-sandbox apply must not enable a sandbox")
		}
	}
}

func askContains(ask []any, rule string) int {
	n := 0
	for _, v := range ask {
		if s, ok := v.(string); ok && s == rule {
			n++
		}
	}
	return n
}

// TestContainmentProfile_IncludesPostGuard: the post-guard ask rules ride in the
// profile in both sandbox modes, so a contained run always carries the accident
// guard.
func TestContainmentProfile_IncludesPostGuard(t *testing.T) {
	for _, ws := range []bool{true, false} {
		perms := ContainmentProfile(ws)["permissions"].(map[string]any)
		ask, _ := perms["ask"].([]any)
		if askContains(ask, "Bash(gh pr review:*)") == 0 || askContains(ask, "Bash(gh pr comment:*)") == 0 {
			t.Errorf("withSandbox=%v: profile missing post-guard ask rules, got %v", ws, ask)
		}
	}
}

// TestApplyPostGuard_MergesAndDedups proves the uncontained path adds the guard
// rules without duplicating them, preserves unrelated rules, and does NOT change
// the ordinary (non-fail-closed) permission mode.
func TestApplyPostGuard_MergesAndDedups(t *testing.T) {
	inst := t.TempDir()
	claudeDir := filepath.Join(inst, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"permissions":{"defaultMode":"acceptEdits","ask":["Bash(gh pr review:*)","Bash(rm:*)"]}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ApplyPostGuard(inst); err != nil {
		t.Fatalf("ApplyPostGuard: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	perms := got["permissions"].(map[string]any)
	if perms["defaultMode"] != "acceptEdits" {
		t.Errorf("ApplyPostGuard must not change defaultMode, got %v", perms["defaultMode"])
	}
	ask, _ := perms["ask"].([]any)
	if n := askContains(ask, "Bash(gh pr review:*)"); n != 1 {
		t.Errorf("review rule count = %d, want 1 (no duplicate)", n)
	}
	if askContains(ask, "Bash(gh pr comment:*)") == 0 {
		t.Error("comment guard rule should have been added")
	}
	if askContains(ask, "Bash(rm:*)") == 0 {
		t.Error("unrelated ask rule must be preserved")
	}
}

func TestVerifyContainmentApplied_RejectsRelaxations(t *testing.T) {
	base := ContainmentProfile(true)

	// allowedDomains non-empty -> reject (egress would be permitted).
	relaxed := ContainmentProfile(true)
	relaxed["sandbox"].(map[string]any)["network"].(map[string]any)["allowedDomains"] = []any{"evil.example.com"}
	if err := VerifyContainmentApplied(relaxed, true); err == nil {
		t.Error("non-empty allowedDomains must be rejected")
	}

	// sandbox disabled -> reject.
	off := ContainmentProfile(true)
	off["sandbox"].(map[string]any)["enabled"] = false
	if err := VerifyContainmentApplied(off, true); err == nil {
		t.Error("sandbox.enabled=false must be rejected")
	}

	// failIfUnavailable dropped -> reject (would allow silent fail-open).
	noFail := ContainmentProfile(true)
	noFail["sandbox"].(map[string]any)["failIfUnavailable"] = false
	if err := VerifyContainmentApplied(noFail, true); err == nil {
		t.Error("sandbox.failIfUnavailable=false must be rejected")
	}

	// allowUnsandboxedCommands relaxed -> reject (unsandboxed escape hatch).
	escape := ContainmentProfile(true)
	escape["sandbox"].(map[string]any)["allowUnsandboxedCommands"] = true
	if err := VerifyContainmentApplied(escape, true); err == nil {
		t.Error("sandbox.allowUnsandboxedCommands=true must be rejected")
	}

	// bypassPermissions -> reject (not fail-closed).
	bypass := ContainmentProfile(true)
	bypass["permissions"].(map[string]any)["defaultMode"] = "bypassPermissions"
	if err := VerifyContainmentApplied(bypass, true); err == nil {
		t.Error("bypassPermissions must be rejected")
	}

	// A non-bypass mode that is still not fail-closed -> reject. This is the
	// allowlist case a denylist would wrongly accept: acceptEdits (and any other
	// mode ContainmentProfile does not produce) must be rejected.
	for _, badMode := range []string{"acceptEdits", "dontAsk", "auto", "plan", "typo"} {
		m := ContainmentProfile(true)
		m["permissions"].(map[string]any)["defaultMode"] = badMode
		if err := VerifyContainmentApplied(m, true); err == nil {
			t.Errorf("permission mode %q must be rejected (only the fail-closed allowlist passes)", badMode)
		}
	}

	// Missing sandbox stanza (a merge dropped it) -> reject.
	if err := VerifyContainmentApplied(map[string]any{"permissions": base["permissions"]}, true); err == nil {
		t.Error("missing sandbox stanza must be rejected")
	}
}
