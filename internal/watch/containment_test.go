package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// readSettings reads and parses an instance's .claude/settings.json.
func readSettings(t *testing.T, inst string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(inst, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parsing settings: %v", err)
	}
	return got
}

// countPreToolUseMatcher counts PreToolUse entries with the given matcher.
func countPreToolUseMatcher(t *testing.T, settings map[string]any, matcher string) int {
	t.Helper()
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return 0
	}
	preToolUse, ok := hooks["PreToolUse"].([]any)
	if !ok {
		return 0
	}
	n := 0
	for _, entry := range preToolUse {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if s, _ := m["matcher"].(string); s == matcher {
			n++
		}
	}
	return n
}

// TestApplyReviewSettings_NoSandbox writes only the Bash post-guard hook and
// leaves no sandbox stanza and no egress-deny hook.
func TestApplyReviewSettings_NoSandbox(t *testing.T) {
	inst := t.TempDir()
	if err := ApplyReviewSettings(inst, false); err != nil {
		t.Fatalf("ApplyReviewSettings(false): %v", err)
	}
	got := readSettings(t, inst)
	if err := VerifyReviewSettings(got, false); err != nil {
		t.Errorf("written no-sandbox settings do not verify: %v", err)
	}
	if _, present := got["sandbox"]; present {
		t.Error("no-sandbox apply must not write a sandbox stanza")
	}
	if n := countPreToolUseMatcher(t, got, postGuardMatcher); n != 1 {
		t.Errorf("no-sandbox apply must add the Bash post-guard hook exactly once, got %d", n)
	}
	if n := countPreToolUseMatcher(t, got, egressDenyMatcher); n != 0 {
		t.Errorf("no-sandbox apply must not add the egress-deny hook, got %d", n)
	}
	if n := countPreToolUseMatcher(t, got, fsGuardMatcher); n != 0 {
		t.Errorf("no-sandbox apply must not add the filesystem-guard hook, got %d", n)
	}
	// No fail-closed permission mode is imposed.
	if perms, ok := got["permissions"].(map[string]any); ok {
		if _, present := perms["defaultMode"]; present {
			t.Error("ApplyReviewSettings must not set permissions.defaultMode")
		}
		if _, present := perms["ask"]; present {
			t.Error("ApplyReviewSettings must not set permissions.ask")
		}
	}
}

// TestApplyReviewSettings_Sandbox writes the no-egress sandbox stanza, the
// egress-deny hook, and the Bash post-guard hook; overwrites a permissive
// pre-existing sandbox; and preserves unrelated keys and pre-existing hooks.
func TestApplyReviewSettings_Sandbox(t *testing.T) {
	inst := t.TempDir()
	claudeDir := filepath.Join(inst, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{
	  "hooks": {
	    "PostToolUse": [{"matcher": "Edit", "hooks": []}],
	    "PreToolUse": [{"matcher": "Read", "hooks": []}]
	  },
	  "permissions": {"deny": ["Bash(rm:*)"]},
	  "sandbox": {"enabled": false, "network": {"allowedDomains": ["evil.example.com"]}}
	}`
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ApplyReviewSettings(inst, true); err != nil {
		t.Fatalf("ApplyReviewSettings: %v", err)
	}

	got := readSettings(t, inst)
	// The permissive sandbox was overwritten to the no-egress profile and both
	// required hooks are present.
	if err := VerifyReviewSettings(got, true); err != nil {
		t.Errorf("post-merge settings do not verify: %v", err)
	}
	if n := countPreToolUseMatcher(t, got, postGuardMatcher); n != 1 {
		t.Errorf("Bash post-guard hook must be present exactly once, got %d", n)
	}
	if n := countPreToolUseMatcher(t, got, egressDenyMatcher); n != 1 {
		t.Errorf("egress-deny hook must be present exactly once, got %d", n)
	}
	if n := countPreToolUseMatcher(t, got, fsGuardMatcher); n != 1 {
		t.Errorf("filesystem-guard hook must be present exactly once, got %d", n)
	}
	// Pre-existing PreToolUse entry preserved alongside the appended hooks.
	if n := countPreToolUseMatcher(t, got, "Read"); n != 1 {
		t.Errorf("pre-existing PreToolUse 'Read' hook must be preserved, got %d", n)
	}
	// Unrelated hook event preserved.
	hooks := got["hooks"].(map[string]any)
	if _, ok := hooks["PostToolUse"]; !ok {
		t.Error("unrelated 'PostToolUse' hook event was dropped")
	}
	// Pre-existing deny list preserved.
	perms := got["permissions"].(map[string]any)
	if _, ok := perms["deny"]; !ok {
		t.Error("pre-existing permissions.deny was dropped")
	}
	// No fail-closed permission mode is imposed.
	if _, present := perms["defaultMode"]; present {
		t.Error("ApplyReviewSettings must not set permissions.defaultMode")
	}
}

// TestApplyReviewSettings_DedupesHooks re-applying does not append duplicate
// hooks (dedupe by matcher).
func TestApplyReviewSettings_DedupesHooks(t *testing.T) {
	inst := t.TempDir()
	if err := ApplyReviewSettings(inst, true); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := ApplyReviewSettings(inst, true); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	got := readSettings(t, inst)
	if n := countPreToolUseMatcher(t, got, postGuardMatcher); n != 1 {
		t.Errorf("re-apply must not duplicate the post-guard hook, got %d", n)
	}
	if n := countPreToolUseMatcher(t, got, egressDenyMatcher); n != 1 {
		t.Errorf("re-apply must not duplicate the egress-deny hook, got %d", n)
	}
	if n := countPreToolUseMatcher(t, got, fsGuardMatcher); n != 1 {
		t.Errorf("re-apply must not duplicate the filesystem-guard hook, got %d", n)
	}
}

// TestVerifyReviewSettings_RequiresPostGuard rejects a doc without the Bash
// post-guard hook, in both modes.
func TestVerifyReviewSettings_RequiresPostGuard(t *testing.T) {
	// No hooks at all, non-sandbox -> reject.
	if err := VerifyReviewSettings(map[string]any{}, false); err == nil {
		t.Error("missing post-guard hook must be rejected (non-sandbox)")
	}
	// A sandbox doc that has the egress-deny hook but not the post-guard -> reject.
	noGuard := map[string]any{
		"sandbox": noEgressSandboxStanza(),
		"hooks": map[string]any{
			"PreToolUse": []any{egressDenyHook()},
		},
	}
	if err := VerifyReviewSettings(noGuard, true); err == nil {
		t.Error("missing post-guard hook must be rejected even with a valid sandbox + egress-deny")
	}
}

// TestVerifyReviewSettings_RequiresEgressDeny rejects a sandbox doc that is
// missing the egress-deny hook (WebFetch/WebSearch/MCP would be reachable).
func TestVerifyReviewSettings_RequiresEgressDeny(t *testing.T) {
	noEgress := map[string]any{
		"sandbox": noEgressSandboxStanza(),
		"hooks": map[string]any{
			"PreToolUse": []any{postGuardHook()},
		},
	}
	if err := VerifyReviewSettings(noEgress, true); err == nil {
		t.Error("missing egress-deny hook must be rejected under sandbox mode")
	}
	// The same doc is fine in non-sandbox mode (egress-deny not required there).
	if err := VerifyReviewSettings(noEgress, false); err != nil {
		t.Errorf("non-sandbox mode must not require the egress-deny hook: %v", err)
	}
}

// TestVerifyReviewSettings_RequiresFSGuard rejects a sandbox doc that is missing
// the filesystem-guard hook (Write/Edit/NotebookEdit could escape the instance).
func TestVerifyReviewSettings_RequiresFSGuard(t *testing.T) {
	noFSGuard := map[string]any{
		"sandbox": noEgressSandboxStanza(),
		"hooks": map[string]any{
			// Has the post-guard and egress-deny, but not the filesystem guard.
			"PreToolUse": []any{postGuardHook(), egressDenyHook()},
		},
	}
	if err := VerifyReviewSettings(noFSGuard, true); err == nil {
		t.Error("missing filesystem-guard hook must be rejected under sandbox mode")
	}
	// The same doc is fine in non-sandbox mode (filesystem guard not required there).
	if err := VerifyReviewSettings(noFSGuard, false); err != nil {
		t.Errorf("non-sandbox mode must not require the filesystem-guard hook: %v", err)
	}
}

// TestVerifyReviewSettings_RejectsRelaxations rejects any weakening of the
// no-egress sandbox stanza.
func TestVerifyReviewSettings_RejectsRelaxations(t *testing.T) {
	// Build a valid sandbox doc, then relax one field at a time.
	base := func() map[string]any {
		return map[string]any{
			"sandbox": noEgressSandboxStanza(),
			"hooks": map[string]any{
				"PreToolUse": []any{postGuardHook(), egressDenyHook(), fsGuardHook("/review-instance")},
			},
		}
	}
	// Sanity: the base doc verifies.
	if err := VerifyReviewSettings(base(), true); err != nil {
		t.Fatalf("base sandbox doc should verify: %v", err)
	}

	// allowedDomains non-empty -> reject (egress would be permitted).
	relaxed := base()
	relaxed["sandbox"].(map[string]any)["network"].(map[string]any)["allowedDomains"] = []any{"evil.example.com"}
	if err := VerifyReviewSettings(relaxed, true); err == nil {
		t.Error("non-empty allowedDomains must be rejected")
	}

	// sandbox disabled -> reject.
	off := base()
	off["sandbox"].(map[string]any)["enabled"] = false
	if err := VerifyReviewSettings(off, true); err == nil {
		t.Error("sandbox.enabled=false must be rejected")
	}

	// failIfUnavailable dropped -> reject (would allow silent fail-open).
	noFail := base()
	noFail["sandbox"].(map[string]any)["failIfUnavailable"] = false
	if err := VerifyReviewSettings(noFail, true); err == nil {
		t.Error("sandbox.failIfUnavailable=false must be rejected")
	}

	// allowUnsandboxedCommands relaxed -> reject (unsandboxed escape hatch).
	escape := base()
	escape["sandbox"].(map[string]any)["allowUnsandboxedCommands"] = true
	if err := VerifyReviewSettings(escape, true); err == nil {
		t.Error("sandbox.allowUnsandboxedCommands=true must be rejected")
	}

	// Missing sandbox stanza (a merge dropped it) -> reject.
	noSandbox := map[string]any{
		"hooks": map[string]any{"PreToolUse": []any{postGuardHook(), egressDenyHook()}},
	}
	if err := VerifyReviewSettings(noSandbox, true); err == nil {
		t.Error("missing sandbox stanza must be rejected")
	}
}

// TestHasPreToolUseMatcher covers the walk over the PreToolUse array.
func TestHasPreToolUseMatcher(t *testing.T) {
	doc := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{postGuardHook(), egressDenyHook()},
		},
	}
	if !hasPreToolUseMatcher(doc, postGuardMatcher) {
		t.Error("expected to find the Bash post-guard matcher")
	}
	if !hasPreToolUseMatcher(doc, egressDenyMatcher) {
		t.Error("expected to find the egress-deny matcher")
	}
	if hasPreToolUseMatcher(doc, "Nope") {
		t.Error("did not expect to find an absent matcher")
	}
	// Missing hooks / wrong shapes return false, not panic.
	if hasPreToolUseMatcher(map[string]any{}, postGuardMatcher) {
		t.Error("empty doc must not report a matcher present")
	}
	if hasPreToolUseMatcher(map[string]any{"hooks": map[string]any{"PreToolUse": "nope"}}, postGuardMatcher) {
		t.Error("non-array PreToolUse must not report a matcher present")
	}
}
