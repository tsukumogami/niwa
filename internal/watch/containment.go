package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Matcher strings for the PreToolUse hooks. They are also the identity used to
// dedupe (an entry is "already present" iff some hook shares its matcher).
const (
	// egressDenyMatcher matches the out-of-sandbox egress channels: WebFetch,
	// WebSearch, and every MCP tool (mcp__ prefix). These make network calls
	// OUTSIDE the OS sandbox (which cages only Bash subprocesses), so in sandbox
	// mode they must be denied by a hook that fires even under bypassPermissions.
	egressDenyMatcher = "WebFetch|WebSearch|mcp__"
	// fsGuardMatcher matches the built-in file tools. Like the egress channels
	// these run OUTSIDE the OS sandbox (through the permission system, which a
	// dispatched session's bypassPermissions skips), so in sandbox mode a write
	// that resolves outside the instance must be denied by a hook. This is the
	// filesystem-escape counterpart to egressDenyMatcher.
	fsGuardMatcher = "Write|Edit|NotebookEdit"
	// postGuardMatcher matches Bash so the post-guard can inspect gh commands and
	// refuse a review/comment post. Applied in every mode (accident prevention).
	postGuardMatcher = "Bash"
)

// guardBinPath returns the absolute path to the niwa binary the filesystem-guard
// hook invokes (as `<niwa> watch guard-fs`). In production it is the running
// executable; tests override it to point at a built binary. If the executable
// path cannot be determined it falls back to "niwa" (resolved on PATH) -- and the
// hook wrapper fails closed (deny) if even that is not found, so a bad path can
// never silently allow an out-of-instance write.
var guardBinPath = func() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "niwa"
}

// shellQuote single-quotes s for safe embedding in the hook command string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// noEgressSandboxStanza is the no-egress OS sandbox profile merged into a
// dispatched instance's .claude/settings.json when sandbox mode is on. An EMPTY
// allowedDomains is the deny-all posture (the live adversarial test proves the
// harness honors it as deny-all, not allow-all). failIfUnavailable makes the
// harness REFUSE to run rather than silently disabling the sandbox, and
// allowUnsandboxedCommands=false removes the unsandboxed escape hatch; together
// they close the harness fail-open so a silent degradation cannot quietly drop
// the sandbox once niwa has decided to enforce it.
func noEgressSandboxStanza() map[string]any {
	return map[string]any{
		"enabled": true,
		"network": map[string]any{
			"allowedDomains": []any{}, // deny-all
		},
		"failIfUnavailable":        true,  // refuse rather than run uncontained
		"allowUnsandboxedCommands": false, // no unsandboxed escape hatch
	}
}

// egressDenyHook returns the PreToolUse hook that denies the out-of-sandbox
// egress channels (WebFetch, WebSearch, and all MCP tools). It is applied in
// sandbox mode only. The hook fires even under bypassPermissions -- unlike
// permissions.ask/deny -- which is why it, not a permission rule, is the closure
// for these channels. Exit 2 blocks the tool call.
func egressDenyHook() map[string]any {
	return map[string]any{
		"matcher": egressDenyMatcher,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": "echo 'niwa watch: no-egress sandbox -- WebFetch/WebSearch/MCP are disabled' >&2; exit 2",
			},
		},
	}
}

// fsGuardHook returns the PreToolUse hook that denies a filesystem escape via the
// built-in Write/Edit/NotebookEdit tools. It is applied in sandbox mode only. The
// OS sandbox confines Bash writes to the instance, but the built-in file tools run
// through the permission system, which bypassPermissions skips -- so this hook is
// the closure for that channel, the filesystem counterpart to egressDenyHook. It
// delegates the decision to `niwa watch guard-fs`, which resolves the target path
// against the instance root and exits 0 (inside -> allow) or 2 (outside -> deny).
// The wrapper maps any non-zero guard exit -- 2, or a failure to run the guard at
// all -- to exit 2 (block), so it is fail-closed: a missing or erroring guard
// denies the write rather than letting it through. It is a hard deny, not an ask:
// a review-drafting agent's only legitimate writes (the draft and clone-local
// files) are inside the instance, so an out-of-instance write is never legitimate.
func fsGuardHook() map[string]any {
	cmd := fmt.Sprintf(
		`%s watch guard-fs; ec=$?; if [ "$ec" = "0" ]; then exit 0; else exit 2; fi`,
		shellQuote(guardBinPath()),
	)
	return map[string]any{
		"matcher": fsGuardMatcher,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": cmd,
			},
		},
	}
}

// postGuardHook returns the PreToolUse hook that refuses an accidental review or
// comment post. It inspects the Bash tool's command payload and blocks a
// `gh pr review`/`gh pr comment`. Applied in BOTH modes: the session runs with
// the developer's real credentials, so this is accident prevention (posting is
// always a human act), NOT a security boundary. The prompt already tells the
// agent not to post; this catches a stray prompt-following.
func postGuardHook() map[string]any {
	return map[string]any{
		"matcher": postGuardMatcher,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": "grep -qE '\"command\":.*gh[[:space:]]+pr[[:space:]]+(review|comment)' && { echo 'niwa watch: posting is a human step -- draft the review and stop' >&2; exit 2; } || exit 0",
			},
		},
	}
}

// ApplyReviewSettings merges the review-session settings into a provisioned
// instance's .claude/settings.json and re-verifies they survived the merge. The
// post-guard PreToolUse hook is always appended (dedup by matcher, preserving any
// existing hooks and other keys). When sandbox is true the no-egress sandbox
// stanza is written (fully owned, so no pre-existing sandbox config can relax the
// posture) and both the egress-deny and the filesystem-guard PreToolUse hooks are
// appended (dedup by matcher) -- the two channels the OS sandbox does not cage. It
// does NOT set permissions.defaultMode or permissions.ask -- the review session
// runs under the developer's real environment and daemon, and the dispatch that
// launches it already sets bypassPermissions. The re-verification is the
// per-instance check that runs before launch; a dropped or relaxed stanza means
// the PR must not be launched.
func ApplyReviewSettings(instancePath string, sandbox bool) error {
	settingsPath := filepath.Join(instancePath, ".claude", "settings.json")
	settings := map[string]any{}
	if data, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("apply review settings: parsing %s: %w", settingsPath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("apply review settings: reading %s: %w", settingsPath, err)
	}

	if sandbox {
		// The sandbox stanza is fully owned -- overwrite it so no pre-existing
		// sandbox config can relax the no-egress posture.
		settings["sandbox"] = noEgressSandboxStanza()
	}

	// Ensure hooks.PreToolUse is an array, preserving any existing entries and
	// other hook events.
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	preToolUse, _ := hooks["PreToolUse"].([]any)

	// Always append the Bash post-guard, deduped by matcher.
	if !preToolUseHasMatcher(preToolUse, postGuardMatcher) {
		preToolUse = append(preToolUse, postGuardHook())
	}
	// In sandbox mode, append the egress-deny and filesystem-guard hooks (the two
	// channels the OS sandbox does not cage), each deduped by matcher.
	if sandbox && !preToolUseHasMatcher(preToolUse, egressDenyMatcher) {
		preToolUse = append(preToolUse, egressDenyHook())
	}
	if sandbox && !preToolUseHasMatcher(preToolUse, fsGuardMatcher) {
		preToolUse = append(preToolUse, fsGuardHook())
	}
	hooks["PreToolUse"] = preToolUse
	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("apply review settings: encoding settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("apply review settings: creating .claude dir: %w", err)
	}
	if err := os.WriteFile(settingsPath, out, 0o644); err != nil {
		return fmt.Errorf("apply review settings: writing settings: %w", err)
	}

	// Re-read from disk and re-verify the settings survived the write/merge.
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("apply review settings: re-reading settings: %w", err)
	}
	var merged map[string]any
	if err := json.Unmarshal(data, &merged); err != nil {
		return fmt.Errorf("apply review settings: re-parsing settings: %w", err)
	}
	return VerifyReviewSettings(merged, sandbox)
}

// VerifyReviewSettings re-reads a merged settings document and asserts the
// review-session settings survived the merge. The post-guard PreToolUse hook
// (matcher "Bash") is always required. When sandbox is true it additionally
// asserts the no-egress sandbox stanza (enabled, empty allowedDomains,
// failIfUnavailable, no unsandboxed escape hatch), the egress-deny PreToolUse hook
// (matcher "WebFetch|WebSearch|mcp__"), AND the filesystem-guard PreToolUse hook
// (matcher "Write|Edit|NotebookEdit").
func VerifyReviewSettings(merged map[string]any, sandbox bool) error {
	if sandbox {
		sb, ok := merged["sandbox"].(map[string]any)
		if !ok {
			return fmt.Errorf("review settings check: sandbox stanza missing from merged settings")
		}
		enabled, _ := sb["enabled"].(bool)
		if !enabled {
			return fmt.Errorf("review settings check: sandbox.enabled is not true")
		}
		network, ok := sb["network"].(map[string]any)
		if !ok {
			return fmt.Errorf("review settings check: sandbox.network missing")
		}
		domains, ok := network["allowedDomains"].([]any)
		if !ok {
			return fmt.Errorf("review settings check: sandbox.network.allowedDomains missing")
		}
		if len(domains) != 0 {
			return fmt.Errorf("review settings check: allowedDomains must be empty (deny-all), got %d entries", len(domains))
		}
		// Fail-open closure: the harness must refuse rather than silently disable
		// the sandbox, and must not permit an unsandboxed escape hatch.
		if fail, _ := sb["failIfUnavailable"].(bool); !fail {
			return fmt.Errorf("review settings check: sandbox.failIfUnavailable must be true")
		}
		if allow, _ := sb["allowUnsandboxedCommands"].(bool); allow {
			return fmt.Errorf("review settings check: sandbox.allowUnsandboxedCommands must be false")
		}
		// The egress-deny hook closes the out-of-sandbox network channels (WebFetch,
		// WebSearch, MCP) that the OS sandbox does not cage.
		if !hasPreToolUseMatcher(merged, egressDenyMatcher) {
			return fmt.Errorf("review settings check: egress-deny PreToolUse hook (matcher %q) missing", egressDenyMatcher)
		}
		// The filesystem-guard hook closes the out-of-sandbox write channel
		// (Write/Edit/NotebookEdit) that the OS sandbox does not cage.
		if !hasPreToolUseMatcher(merged, fsGuardMatcher) {
			return fmt.Errorf("review settings check: filesystem-guard PreToolUse hook (matcher %q) missing", fsGuardMatcher)
		}
	}
	// The Bash post-guard is required in every mode.
	if !hasPreToolUseMatcher(merged, postGuardMatcher) {
		return fmt.Errorf("review settings check: post-guard PreToolUse hook (matcher %q) missing", postGuardMatcher)
	}
	return nil
}

// hasPreToolUseMatcher walks merged["hooks"]["PreToolUse"] (a []any of maps) and
// returns true if any entry's "matcher" equals the given string.
func hasPreToolUseMatcher(merged map[string]any, matcher string) bool {
	hooks, ok := merged["hooks"].(map[string]any)
	if !ok {
		return false
	}
	preToolUse, ok := hooks["PreToolUse"].([]any)
	if !ok {
		return false
	}
	return preToolUseHasMatcher(preToolUse, matcher)
}

// preToolUseHasMatcher reports whether any entry in a PreToolUse array has the
// given matcher. Shared by the apply-time dedupe and the verify-time check.
func preToolUseHasMatcher(preToolUse []any, matcher string) bool {
	for _, entry := range preToolUse {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if s, _ := m["matcher"].(string); s == matcher {
			return true
		}
	}
	return false
}
