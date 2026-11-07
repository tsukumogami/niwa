package watch

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
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
	if err := ApplyReviewSettings(inst, false, false); err != nil {
		t.Fatalf("ApplyReviewSettings(false): %v", err)
	}
	got := readSettings(t, inst)
	if err := VerifyReviewSettings(got, inst, false, false); err != nil {
		t.Errorf("written no-sandbox settings do not verify: %v", err)
	}
	if _, present := got["sandbox"]; present {
		t.Error("no-sandbox apply must not write a sandbox stanza")
	}
	if n := countPreToolUseHook(got, postGuardMatcher, hookCommand(postGuardHook())); n != 1 {
		t.Errorf("no-sandbox apply must add the Bash post-guard hook exactly once, got %d", n)
	}
	if n := countPreToolUseMatcher(t, got, sessionReachDenyMatcher); n != 1 {
		t.Errorf("no-sandbox apply must add the session-reach deny hook exactly once, got %d", n)
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

	if err := ApplyReviewSettings(inst, true, false); err != nil {
		t.Fatalf("ApplyReviewSettings: %v", err)
	}

	got := readSettings(t, inst)
	// The permissive sandbox was overwritten to the no-egress profile and both
	// required hooks are present.
	if err := VerifyReviewSettings(got, inst, true, false); err != nil {
		t.Errorf("post-merge settings do not verify: %v", err)
	}
	if n := countPreToolUseHook(got, postGuardMatcher, hookCommand(postGuardHook())); n != 1 {
		t.Errorf("Bash post-guard hook must be present exactly once, got %d", n)
	}
	if n := countPreToolUseMatcher(t, got, egressDenyMatcher); n != 1 {
		t.Errorf("egress-deny hook must be present exactly once, got %d", n)
	}
	if n := countPreToolUseMatcher(t, got, fsGuardMatcher); n != 1 {
		t.Errorf("filesystem-guard hook must be present exactly once, got %d", n)
	}
	if n := countPreToolUseMatcher(t, got, sessionReachDenyMatcher); n != 1 {
		t.Errorf("session-reach deny hook must be present exactly once, got %d", n)
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

// preToolUseCommand returns the command string of the first PreToolUse hook with the
// given matcher, or "" if absent.
func preToolUseCommand(t *testing.T, settings map[string]any, matcher string) string {
	t.Helper()
	hooks, _ := settings["hooks"].(map[string]any)
	preToolUse, _ := hooks["PreToolUse"].([]any)
	for _, entry := range preToolUse {
		m, _ := entry.(map[string]any)
		if s, _ := m["matcher"].(string); s != matcher {
			continue
		}
		inner, _ := m["hooks"].([]any)
		if len(inner) == 0 {
			return ""
		}
		h, _ := inner[0].(map[string]any)
		cmd, _ := h["command"].(string)
		return cmd
	}
	return ""
}

// TestApplyReviewSettings_AskPosture writes the operator-approval posture: the
// non-bypass permission mode, the auto-allow hook, and the --ask-outside filesystem
// guard, on top of the sandbox baseline.
func TestApplyReviewSettings_AskPosture(t *testing.T) {
	inst := t.TempDir()
	if err := ApplyReviewSettings(inst, true, true); err != nil {
		t.Fatalf("ApplyReviewSettings(sandbox, ask): %v", err)
	}
	got := readSettings(t, inst)
	if err := VerifyReviewSettings(got, inst, true, true); err != nil {
		t.Errorf("ask-posture settings do not verify: %v", err)
	}
	// permissions.defaultMode owned as "default" so a hook ask is honored.
	perms, ok := got["permissions"].(map[string]any)
	if !ok || perms["defaultMode"] != "default" {
		t.Errorf("ask posture must set permissions.defaultMode=default, got %v", got["permissions"])
	}
	// The auto-allow hook is present exactly once.
	if n := countPreToolUseMatcher(t, got, autoAllowMatcher); n != 1 {
		t.Errorf("ask posture must add the auto-allow hook exactly once, got %d", n)
	}
	// The egress-deny, fs-guard, and post-guard remain.
	if n := countPreToolUseMatcher(t, got, egressDenyMatcher); n != 1 {
		t.Errorf("egress-deny hook must remain, got %d", n)
	}
	if n := countPreToolUseHook(got, postGuardMatcher, hookCommand(postGuardHook())); n != 1 {
		t.Errorf("post-guard hook must remain, got %d", n)
	}
	if n := countPreToolUseMatcher(t, got, sessionReachDenyMatcher); n != 1 {
		t.Errorf("session-reach deny hook must be present exactly once, got %d", n)
	}
	// The fs-guard hook runs with --ask-outside (operator-approval form).
	if cmd := preToolUseCommand(t, got, fsGuardMatcher); !strings.Contains(cmd, "--ask-outside") {
		t.Errorf("ask-posture fs-guard hook must pass --ask-outside, got %q", cmd)
	}
}

// TestApplyReviewSettings_HardDenyPostureUnchanged asserts the ask=false posture is
// byte-for-byte the shipped floor: no defaultMode, no auto-allow hook, and the fs-guard
// in its exit-code wrapper form (no --ask-outside).
func TestApplyReviewSettings_HardDenyPostureUnchanged(t *testing.T) {
	inst := t.TempDir()
	if err := ApplyReviewSettings(inst, true, false); err != nil {
		t.Fatalf("ApplyReviewSettings(sandbox, hard-deny): %v", err)
	}
	got := readSettings(t, inst)
	if perms, ok := got["permissions"].(map[string]any); ok {
		if _, present := perms["defaultMode"]; present {
			t.Error("hard-deny posture must not set permissions.defaultMode")
		}
	}
	if n := countPreToolUseMatcher(t, got, autoAllowMatcher); n != 0 {
		t.Errorf("hard-deny posture must not add the auto-allow hook, got %d", n)
	}
	if n := countPreToolUseMatcher(t, got, sessionReachDenyMatcher); n != 1 {
		t.Errorf("hard-deny posture must add the session-reach deny hook exactly once, got %d", n)
	}
	cmd := preToolUseCommand(t, got, fsGuardMatcher)
	if strings.Contains(cmd, "--ask-outside") {
		t.Errorf("hard-deny fs-guard hook must not pass --ask-outside, got %q", cmd)
	}
	if !strings.Contains(cmd, "exit 2") {
		t.Errorf("hard-deny fs-guard hook must keep its exit-code wrapper, got %q", cmd)
	}
}

// TestVerifyReviewSettings_RequiresAskPostureBits rejects an ask-posture doc that is
// missing the non-bypass mode or the auto-allow hook.
func TestVerifyReviewSettings_RequiresAskPostureBits(t *testing.T) {
	// A sandbox doc with all hard-deny hooks but no defaultMode / auto-allow: valid as
	// hard-deny, rejected as ask.
	base := map[string]any{
		"sandbox": noEgressSandboxStanza(),
		"hooks": map[string]any{
			"PreToolUse": []any{postGuardHook(), sessionReachDenyHook(), sessionCommandDenyHook(), egressDenyHook(), fsGuardHook("/inst", false)},
		},
	}
	if err := VerifyReviewSettings(base, "/inst", true, false); err != nil {
		t.Fatalf("base doc must verify as hard-deny: %v", err)
	}
	if err := VerifyReviewSettings(base, "/inst", true, true); err == nil || !strings.Contains(err.Error(), "permissions block missing") {
		t.Errorf("ask posture must reject a doc missing defaultMode + auto-allow for the missing permissions block, got %v", err)
	}

	// Add defaultMode but still no auto-allow -> still rejected as ask.
	withMode := map[string]any{
		"sandbox":     noEgressSandboxStanza(),
		"permissions": map[string]any{"defaultMode": "default"},
		"hooks": map[string]any{
			"PreToolUse": []any{postGuardHook(), sessionReachDenyHook(), sessionCommandDenyHook(), egressDenyHook(), fsGuardHook("/inst", true)},
		},
	}
	if err := VerifyReviewSettings(withMode, "/inst", true, true); err == nil || !strings.Contains(err.Error(), "auto-allow") {
		t.Errorf("ask posture must reject a doc missing the auto-allow hook for that reason, got %v", err)
	}

	// Full ask-posture doc verifies.
	full := map[string]any{
		"sandbox":     noEgressSandboxStanza(),
		"permissions": map[string]any{"defaultMode": "default"},
		"hooks": map[string]any{
			"PreToolUse": []any{postGuardHook(), sessionReachDenyHook(), sessionCommandDenyHook(), egressDenyHook(), autoAllowHook(), fsGuardHook("/inst", true)},
		},
	}
	if err := VerifyReviewSettings(full, "/inst", true, true); err != nil {
		t.Errorf("full ask-posture doc must verify: %v", err)
	}
}

// TestAskPosture_PostGuardStillBlocksPosting proves that under the ask posture the
// Bash post-guard still refuses a `gh pr review`/`gh pr comment` even though the
// auto-allow hook (which shares the Bash channel) emits an allow. The two hooks are
// distinct matcher entries; the post-guard's own exit-2 is what blocks posting, and
// the harness's deny-over-allow precedence is what makes it win at runtime. This test
// executes the post-guard command the ask-posture settings actually emit and asserts it
// exits 2 for a posting command and 0 for a benign one -- so the guard itself is wired
// and fires regardless of posture.
func TestAskPosture_PostGuardStillBlocksPosting(t *testing.T) {
	inst := t.TempDir()
	if err := ApplyReviewSettings(inst, true, true); err != nil {
		t.Fatalf("ApplyReviewSettings(sandbox, ask): %v", err)
	}
	got := readSettings(t, inst)

	// The ask posture wires BOTH the post-guard (matcher "Bash") and the auto-allow
	// (matcher "Bash|Read|Glob|Grep") as distinct entries; neither displaces the other.
	if n := countPreToolUseHook(got, postGuardMatcher, hookCommand(postGuardHook())); n != 1 {
		t.Fatalf("post-guard hook must be present under the ask posture, got %d", n)
	}
	if n := countPreToolUseMatcher(t, got, autoAllowMatcher); n != 1 {
		t.Fatalf("auto-allow hook must be present under the ask posture, got %d", n)
	}

	postGuard := preToolUseCommand(t, got, postGuardMatcher)
	if postGuard == "" {
		t.Fatal("post-guard command missing")
	}

	runHook := func(payload string) int {
		c := exec.Command("sh", "-c", postGuard)
		c.Stdin = strings.NewReader(payload)
		if err := c.Run(); err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return ee.ExitCode()
			}
			t.Fatalf("running post-guard: %v", err)
		}
		return 0
	}

	// A posting command is blocked (exit 2) even though the auto-allow hook would allow
	// the same Bash tool.
	if code := runHook(`{"tool_name":"Bash","tool_input":{"command":"gh pr review 5 --approve"}}`); code != 2 {
		t.Errorf("post-guard must block `gh pr review` under the ask posture (exit 2), got %d", code)
	}
	if code := runHook(`{"tool_name":"Bash","tool_input":{"command":"gh pr comment 5 --body hi"}}`); code != 2 {
		t.Errorf("post-guard must block `gh pr comment` under the ask posture (exit 2), got %d", code)
	}
	// A benign command is not blocked by the post-guard (exit 0), leaving the auto-allow
	// hook free to approve it.
	if code := runHook(`{"tool_name":"Bash","tool_input":{"command":"git status"}}`); code != 0 {
		t.Errorf("post-guard must not block a benign Bash command (exit 0), got %d", code)
	}
}

// TestApplyReviewSettings_DedupesHooks re-applying does not append duplicate
// hooks. Hooks are counted by matcher and command, since the post-guard and the
// session-command deny share the matcher Bash.
func TestApplyReviewSettings_DedupesHooks(t *testing.T) {
	inst := t.TempDir()
	if err := ApplyReviewSettings(inst, true, false); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := ApplyReviewSettings(inst, true, false); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	got := readSettings(t, inst)
	for _, h := range reviewHooks(inst, true, false) {
		if n := countPreToolUseHook(got, h.matcher, h.command); n != 1 {
			t.Errorf("re-apply must leave the %s hook exactly once, got %d", h.name, n)
		}
	}
	if n := countPreToolUseMatcher(t, got, postGuardMatcher); n != 2 {
		t.Errorf("the Bash matcher must carry the post-guard and the session-command deny, got %d entries", n)
	}
}

// TestVerifyReviewSettings_RequiresPostGuard rejects a doc without the Bash
// post-guard hook, in both modes.
func TestVerifyReviewSettings_RequiresPostGuard(t *testing.T) {
	// No hooks at all, non-sandbox -> reject.
	if err := VerifyReviewSettings(map[string]any{}, "/inst", false, false); err == nil {
		t.Error("missing post-guard hook must be rejected (non-sandbox)")
	}
	// A sandbox doc that has every other hook but not the post-guard -> reject.
	noGuard := map[string]any{
		"sandbox": noEgressSandboxStanza(),
		"hooks": map[string]any{
			"PreToolUse": []any{sessionReachDenyHook(), sessionCommandDenyHook(), egressDenyHook(), fsGuardHook("/inst", false)},
		},
	}
	if err := VerifyReviewSettings(noGuard, "/inst", true, false); err == nil || !strings.Contains(err.Error(), "post-guard") {
		t.Errorf("missing post-guard hook must be rejected even with a valid sandbox and the other hooks, got %v", err)
	}
}

// TestVerifyReviewSettings_RequiresEgressDeny rejects a sandbox doc that is
// missing the egress-deny hook (WebFetch/WebSearch/MCP would be reachable).
func TestVerifyReviewSettings_RequiresEgressDeny(t *testing.T) {
	noEgress := map[string]any{
		"sandbox": noEgressSandboxStanza(),
		"hooks": map[string]any{
			"PreToolUse": []any{postGuardHook(), sessionReachDenyHook(), sessionCommandDenyHook(), fsGuardHook("/inst", false)},
		},
	}
	if err := VerifyReviewSettings(noEgress, "/inst", true, false); err == nil || !strings.Contains(err.Error(), "egress-deny") {
		t.Errorf("missing egress-deny hook must be rejected under sandbox mode for that reason, got %v", err)
	}
	// The same doc is fine in non-sandbox mode (egress-deny not required there).
	if err := VerifyReviewSettings(noEgress, "/inst", false, false); err != nil {
		t.Errorf("non-sandbox mode must not require the egress-deny hook: %v", err)
	}
}

// TestVerifyReviewSettings_RequiresFSGuard rejects a sandbox doc that is missing
// the filesystem-guard hook (Write/Edit/NotebookEdit could escape the instance).
func TestVerifyReviewSettings_RequiresFSGuard(t *testing.T) {
	noFSGuard := map[string]any{
		"sandbox": noEgressSandboxStanza(),
		"hooks": map[string]any{
			// Has the post-guard, session-reach deny, and egress-deny hooks, but not
			// the filesystem guard.
			"PreToolUse": []any{postGuardHook(), sessionReachDenyHook(), sessionCommandDenyHook(), egressDenyHook()},
		},
	}
	if err := VerifyReviewSettings(noFSGuard, "/inst", true, false); err == nil || !strings.Contains(err.Error(), "filesystem-guard") {
		t.Errorf("missing filesystem-guard hook must be rejected under sandbox mode for that reason, got %v", err)
	}
	// The same doc is fine in non-sandbox mode (filesystem guard not required there).
	if err := VerifyReviewSettings(noFSGuard, "/inst", false, false); err != nil {
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
				"PreToolUse": []any{postGuardHook(), sessionReachDenyHook(), sessionCommandDenyHook(), egressDenyHook(), fsGuardHook("/inst", false)},
			},
		}
	}
	// Sanity: the base doc verifies.
	if err := VerifyReviewSettings(base(), "/inst", true, false); err != nil {
		t.Fatalf("base sandbox doc should verify: %v", err)
	}

	// allowedDomains non-empty -> reject (egress would be permitted).
	relaxed := base()
	relaxed["sandbox"].(map[string]any)["network"].(map[string]any)["allowedDomains"] = []any{"evil.example.com"}
	if err := VerifyReviewSettings(relaxed, "/inst", true, false); err == nil {
		t.Error("non-empty allowedDomains must be rejected")
	}

	// sandbox disabled -> reject.
	off := base()
	off["sandbox"].(map[string]any)["enabled"] = false
	if err := VerifyReviewSettings(off, "/inst", true, false); err == nil {
		t.Error("sandbox.enabled=false must be rejected")
	}

	// failIfUnavailable dropped -> reject (would allow silent fail-open).
	noFail := base()
	noFail["sandbox"].(map[string]any)["failIfUnavailable"] = false
	if err := VerifyReviewSettings(noFail, "/inst", true, false); err == nil {
		t.Error("sandbox.failIfUnavailable=false must be rejected")
	}

	// allowUnsandboxedCommands relaxed -> reject (unsandboxed escape hatch).
	escape := base()
	escape["sandbox"].(map[string]any)["allowUnsandboxedCommands"] = true
	if err := VerifyReviewSettings(escape, "/inst", true, false); err == nil {
		t.Error("sandbox.allowUnsandboxedCommands=true must be rejected")
	}

	// Missing sandbox stanza (a merge dropped it) -> reject.
	noSandbox := map[string]any{
		"hooks": map[string]any{"PreToolUse": []any{postGuardHook(), sessionReachDenyHook(), sessionCommandDenyHook(), egressDenyHook(), fsGuardHook("/inst", false)}},
	}
	if err := VerifyReviewSettings(noSandbox, "/inst", true, false); err == nil || !strings.Contains(err.Error(), "sandbox stanza missing") {
		t.Errorf("missing sandbox stanza must be rejected for that reason, got %v", err)
	}
}

// hasPreToolUseMatcher reports whether any PreToolUse entry in a settings document
// has the given matcher, whatever its command. Tests use it to inspect fixtures;
// the code under test identifies hooks by matcher and command.
func hasPreToolUseMatcher(doc map[string]any, matcher string) bool {
	for _, entry := range preToolUseEntries(doc) {
		m, _ := entry.(map[string]any)
		if s, _ := m["matcher"].(string); s == matcher {
			return true
		}
	}
	return false
}

// countPreToolUseHook counts PreToolUse entries with the given matcher whose only
// handler is a plain command hook running exactly command.
func countPreToolUseHook(settings map[string]any, matcher, command string) int {
	n := 0
	for _, entry := range preToolUseEntries(settings) {
		if preToolUseHasHook([]any{entry}, matcher, command) {
			n++
		}
	}
	return n
}

// TestHasPreToolUseHook covers the walk over the PreToolUse array.
func TestHasPreToolUseHook(t *testing.T) {
	doc := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{postGuardHook(), egressDenyHook()},
		},
	}
	if !hasPreToolUseHook(doc, postGuardMatcher, hookCommand(postGuardHook())) {
		t.Error("expected to find the Bash post-guard")
	}
	if !hasPreToolUseHook(doc, egressDenyMatcher, hookCommand(egressDenyHook())) {
		t.Error("expected to find the egress deny")
	}
	if hasPreToolUseHook(doc, postGuardMatcher, hookCommand(egressDenyHook())) {
		t.Error("a matcher with another hook's command must not count")
	}
	if hasPreToolUseHook(doc, "Nope", hookCommand(postGuardHook())) {
		t.Error("did not expect to find an absent matcher")
	}
	// Missing hooks / wrong shapes return false, not panic.
	if hasPreToolUseHook(map[string]any{}, postGuardMatcher, hookCommand(postGuardHook())) {
		t.Error("empty doc must not report a hook present")
	}
	if hasPreToolUseHook(map[string]any{"hooks": map[string]any{"PreToolUse": "nope"}}, postGuardMatcher, hookCommand(postGuardHook())) {
		t.Error("non-array PreToolUse must not report a hook present")
	}
}

// putFile writes content at inst/rel, creating parent directories.
func putFile(t *testing.T, inst, rel, content string) {
	t.Helper()
	p := filepath.Join(inst, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newDigestInstance builds a review instance holding every kind of path the
// settings digest covers, a PR clone with its own Claude Code files, and two
// symlinks under .claude/skills: one to a file inside the instance and one to
// a file outside it.
func newDigestInstance(t *testing.T) (inst, outside string) {
	t.Helper()
	inst, outside = t.TempDir(), t.TempDir()
	putFile(t, inst, ".claude/settings.json", `{"hooks":{}}`)
	putFile(t, inst, ".claude/skills/review/SKILL.md", "skill")
	putFile(t, inst, ".claude/agents/a.md", "agent")
	putFile(t, inst, ".claude/commands/c.md", "command")
	putFile(t, inst, "CLAUDE.md", "instructions-plain-text")
	putFile(t, inst, "shared/linked-skill.md", "linked")
	putFile(t, inst, "pr-clone/.claude/settings.json", `{}`)
	putFile(t, inst, "pr-clone/.mcp.json", `{}`)
	putFile(t, inst, "pr-clone/CLAUDE.md", "clone")
	putFile(t, outside, "outside.md", "outside")
	if err := os.Symlink(filepath.Join(inst, "shared", "linked-skill.md"), filepath.Join(inst, ".claude", "skills", "linked.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "outside.md"), filepath.Join(inst, ".claude", "skills", "outside.md")); err != nil {
		t.Fatal(err)
	}
	return inst, outside
}

func mustDigest(t *testing.T, inst string) string {
	t.Helper()
	d, err := ReviewSettingsDigest(inst)
	if err != nil {
		t.Fatalf("ReviewSettingsDigest: %v", err)
	}
	return d
}

func TestReviewSettingsDigest(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(t *testing.T, inst, outside string)
		changes bool
	}{
		{"settings_json_edited", func(t *testing.T, inst, _ string) {
			putFile(t, inst, ".claude/settings.json", `{"hooks":{},"statusLine":{"type":"command","command":"x"}}`)
		}, true},
		{"skill_added", func(t *testing.T, inst, _ string) { putFile(t, inst, ".claude/skills/new/SKILL.md", "new") }, true},
		{"agent_added", func(t *testing.T, inst, _ string) { putFile(t, inst, ".claude/agents/new.md", "new") }, true},
		{"command_added", func(t *testing.T, inst, _ string) { putFile(t, inst, ".claude/commands/new.md", "new") }, true},
		{"claude_md_edited", func(t *testing.T, inst, _ string) { putFile(t, inst, "CLAUDE.md", "edited") }, true},
		{"claude_local_md_added", func(t *testing.T, inst, _ string) { putFile(t, inst, "CLAUDE.local.md", "new") }, true},
		{"linked_file_inside_instance_edited", func(t *testing.T, inst, _ string) { putFile(t, inst, "shared/linked-skill.md", "edited") }, true},
		{"file_removed", func(t *testing.T, inst, _ string) {
			if err := os.Remove(filepath.Join(inst, ".claude", "agents", "a.md")); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"outside_link_retargeted", func(t *testing.T, inst, outside string) {
			putFile(t, outside, "other.md", "outside")
			link := filepath.Join(inst, ".claude", "skills", "outside.md")
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "other.md"), link); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"pr_clone_settings_json", func(t *testing.T, inst, _ string) {
			putFile(t, inst, "pr-clone/.claude/settings.json", `{"hooks":{"SessionStart":[]}}`)
		}, false},
		{"pr_clone_mcp_json", func(t *testing.T, inst, _ string) {
			putFile(t, inst, "pr-clone/.mcp.json", `{"mcpServers":{"x":{"command":"x"}}}`)
		}, false},
		{"pr_clone_claude_md", func(t *testing.T, inst, _ string) { putFile(t, inst, "pr-clone/CLAUDE.md", "edited") }, false},
		{"pr_clone_new_file", func(t *testing.T, inst, _ string) { putFile(t, inst, "pr-clone/.claude/skills/x/SKILL.md", "x") }, false},
		{"outside_link_target_edited", func(t *testing.T, _, outside string) { putFile(t, outside, "outside.md", "edited") }, false},
		{"unlinked_instance_file_edited", func(t *testing.T, inst, _ string) { putFile(t, inst, "shared/unlinked.md", "x") }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inst, outside := newDigestInstance(t)
			before := mustDigest(t, inst)
			if again := mustDigest(t, inst); again != before {
				t.Fatalf("digest is not stable across two reads of an unchanged instance")
			}
			tc.mutate(t, inst, outside)
			after := mustDigest(t, inst)
			if tc.changes && after == before {
				t.Fatalf("digest did not change")
			}
			if !tc.changes && after != before {
				t.Fatalf("digest changed:\nbefore %s\nafter  %s", before, after)
			}
		})
	}

	t.Run("no_content_in_digest", func(t *testing.T) {
		inst, _ := newDigestInstance(t)
		if d := mustDigest(t, inst); strings.Contains(d, "instructions-plain-text") || strings.Contains(d, "pr-clone") {
			t.Fatalf("digest carries content or the PR clone: %s", d)
		}
	})

	t.Run("fifo_does_not_hang", func(t *testing.T) {
		inst, _ := newDigestInstance(t)
		before := mustDigest(t, inst)
		if err := syscall.Mkfifo(filepath.Join(inst, ".claude", "pipe"), 0o600); err != nil {
			t.Skipf("mkfifo: %v", err)
		}
		type result struct {
			d   string
			err error
		}
		done := make(chan result, 1)
		go func() {
			d, err := ReviewSettingsDigest(inst)
			done <- result{d, err}
		}()
		select {
		case r := <-done:
			if r.err != nil {
				t.Fatalf("ReviewSettingsDigest: %v", r.err)
			}
			if r.d == before {
				t.Fatalf("a FIFO under .claude did not change the digest")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("ReviewSettingsDigest hung on a FIFO")
		}
	})

	t.Run("claude_dir_symlink_not_descended", func(t *testing.T) {
		inst := t.TempDir()
		elsewhere := t.TempDir()
		putFile(t, elsewhere, "settings.json", "{}")
		if err := os.Symlink(elsewhere, filepath.Join(inst, ".claude")); err != nil {
			t.Fatal(err)
		}
		before := mustDigest(t, inst)
		putFile(t, elsewhere, "settings.json", `{"x":1}`)
		if mustDigest(t, inst) != before {
			t.Fatalf("a .claude symlink was descended")
		}
	})

	t.Run("bounds", func(t *testing.T) {
		inst, _ := newDigestInstance(t)
		origEntries, origBytes := digestMaxEntries, digestMaxBytes
		t.Cleanup(func() { digestMaxEntries, digestMaxBytes = origEntries, origBytes })
		digestMaxEntries = 3
		if _, err := ReviewSettingsDigest(inst); err == nil {
			t.Error("digest over the entry bound did not fail")
		}
		digestMaxEntries, digestMaxBytes = origEntries, 10
		if _, err := ReviewSettingsDigest(inst); err == nil {
			t.Error("digest over the byte bound did not fail")
		}
	})

	t.Run("missing_instance_errors", func(t *testing.T) {
		if _, err := ReviewSettingsDigest(filepath.Join(t.TempDir(), "gone")); err == nil {
			t.Fatal("digest of a missing instance did not fail")
		}
	})
}

func TestReviewSettingsDigestChanges(t *testing.T) {
	inst, _ := newDigestInstance(t)
	recorded := mustDigest(t, inst)

	if got, err := ReviewSettingsDigestChanges(recorded, recorded); err != nil || len(got) != 0 {
		t.Fatalf("unchanged: got %v, %v", got, err)
	}

	putFile(t, inst, ".claude/settings.json", `{"env":{"X":"y"}}`)
	putFile(t, inst, "CLAUDE.local.md", "new")
	putFile(t, inst, ".claude/commands/new.md", "new")
	if err := os.Remove(filepath.Join(inst, ".claude", "agents", "a.md")); err != nil {
		t.Fatal(err)
	}
	putFile(t, inst, "pr-clone/.mcp.json", `{"x":1}`)
	got, err := ReviewSettingsDigestChanges(recorded, mustDigest(t, inst))
	if err != nil {
		t.Fatalf("ReviewSettingsDigestChanges: %v", err)
	}
	want := []string{".claude/agents/a.md", ".claude/commands/new.md", ".claude/settings.json", "CLAUDE.local.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}

	for _, bad := range []string{"", "not json", `{"v":2,"f":{}}`, `{"v":1}`} {
		if _, err := ReviewSettingsDigestChanges(bad, recorded); err == nil {
			t.Errorf("recorded %q was accepted", bad)
		}
	}
	if _, err := ReviewSettingsDigestChanges(recorded, "not json"); err == nil {
		t.Error("an unreadable current digest was accepted")
	}
}

func TestCheckReviewSavedOptions(t *testing.T) {
	// The secret-looking marker stands in for an environment value: no
	// refusal may print it.
	const secret = "s3cr3t-marker"
	doc := `{"env":{"NIWA_REAP_KEY":"` + secret + `"}}`
	cases := []struct {
		name     string
		opts     []string
		sandbox  bool
		wantFlag string // "" with refused=false means accepted
		refused  bool
	}{
		// Accepted: what a review stage saves.
		{"sandboxed_stage", []string{"--name", "watch_acme_widget_7", "--strict-mcp-config", "--settings", doc}, true, "", false},
		{"unsandboxed_stage", []string{"--name", "watch_acme_widget_7", "--settings", doc}, false, "", false},
		{"unsandboxed_with_strict", []string{"--name", "watch_acme_widget_7", "--strict-mcp-config", "--settings", doc}, false, "", false},
		{"joined_values", []string{"--name=watch_acme_widget_7", "--strict-mcp-config", "--settings=" + doc}, true, "", false},
		{"no_settings_doc", []string{"--name", "watch_acme_widget_7"}, false, "", false},

		// Refused.
		{"strict_missing_sandboxed", []string{"--name", "watch_acme_widget_7", "--settings", doc}, true, "--strict-mcp-config", true},
		{"strict_with_value", []string{"--strict-mcp-config=" + secret}, false, "--strict-mcp-config", true},
		{"settings_permissions_key", []string{"--settings", `{"env":{"A":"` + secret + `"},"permissions":{"allow":["Bash"]}}`}, false, "--settings", true},
		{"settings_hooks_key", []string{"--settings", `{"env":{},"hooks":{"x":"` + secret + `"}}`}, false, "--settings", true},
		{"settings_not_json", []string{"--settings", "not json " + secret}, false, "--settings", true},
		{"settings_env_not_object", []string{"--settings", `{"env":"` + secret + `"}`}, false, "--settings", true},
		{"settings_null", []string{"--settings", "null"}, false, "--settings", true},
		{"settings_twice", []string{"--settings", doc, "--settings", doc}, false, "--settings", true},
		{"settings_missing_value", []string{"--settings"}, false, "--settings", true},
		{"permission_mode", []string{"--name", "x", "--permission-mode", "bypassPermissions"}, false, "--permission-mode", true},
		{"permission_mode_joined", []string{"--permission-mode=default"}, false, "--permission-mode", true},
		{"mcp_config", []string{"--mcp-config", "/tmp/mcp.json"}, false, "--mcp-config", true},
		{"setting_sources", []string{"--setting-sources", "user,project"}, false, "--setting-sources", true},
		{"add_dir", []string{"--add-dir", "/"}, false, "--add-dir", true},
		{"plugin_dir", []string{"--plugin-dir", "/tmp/p"}, false, "--plugin-dir", true},
		{"dangerously_skip_permissions", []string{"--dangerously-skip-permissions"}, false, "--dangerously-skip-permissions", true},
		{"dangerously_other", []string{"--dangerously-load-development-channels", "x"}, false, "--dangerously-load-development-channels", true},
		{"unknown_flag", []string{"--foo"}, false, "--foo", true},
		{"name_value_is_a_flag", []string{"--name", "--permission-mode", "bypassPermissions"}, false, "--name", true},
		{"name_missing_value", []string{"--name"}, false, "--name", true},
		{"positional", []string{"--name", "x", "stray-" + secret}, false, "", true},
		{"short_flag", []string{"-p", secret}, false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckReviewSavedOptions(tc.opts, tc.sandbox)
			if !tc.refused {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted")
			}
			var soe *SavedOptionsError
			if !errors.As(err, &soe) {
				t.Fatalf("error %T is not a *SavedOptionsError: %v", err, err)
			}
			if soe.Flag != tc.wantFlag {
				t.Errorf("Flag = %q, want %q", soe.Flag, tc.wantFlag)
			}
			if tc.wantFlag != "" && !strings.Contains(err.Error(), tc.wantFlag) {
				t.Errorf("error %q does not name %s", err, tc.wantFlag)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), `"env"`) {
				t.Errorf("error prints an option value: %q", err)
			}
		})
	}
}
