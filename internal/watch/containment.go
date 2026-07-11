package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// sandboxSettings is the no-egress sandbox profile merged into a dispatched
// instance's .claude/settings.json. An EMPTY allowedDomains is the deny-all
// posture (the live adversarial test proves the harness honors it as deny-all,
// not allow-all).
type sandboxSettings struct {
	Sandbox struct {
		Enabled bool `json:"enabled"`
		Network struct {
			AllowedDomains []string `json:"allowedDomains"`
		} `json:"network"`
		// failIfUnavailable makes the harness REFUSE to run rather than
		// silently disabling the sandbox and proceeding with a warning when its
		// backend is unavailable. allowUnsandboxedCommands=false is the paired
		// belt-and-suspenders. Together they close the harness fail-open so that
		// once niwa has decided to enforce the OS sandbox (watch_sandbox
		// required), a silent harness degradation cannot quietly drop it. (Exact
		// setting-key paths are per the Claude Code sandbox settings schema; see
		// the sandboxing docs.)
		FailIfUnavailable        bool `json:"failIfUnavailable"`
		AllowUnsandboxedCommands bool `json:"allowUnsandboxedCommands"`
	} `json:"sandbox"`
	Permissions struct {
		Ask []string `json:"ask,omitempty"`
	} `json:"permissions"`
}

// postGuardAskRules require operator approval before the dispatched session can
// submit a review or comment. It is a convenience guard against accidental
// posting: the session runs with the developer's real credentials, so this is an
// accident-prevention click, NOT a security boundary (command-string matching is
// not one -- that is what the OS sandbox is for). The prompt already tells the
// agent not to post; this catches a stray prompt-following. It is applied in
// every mode.
var postGuardAskRules = []string{
	"Bash(gh pr review:*)",
	"Bash(gh pr comment:*)",
}

// SandboxProfile returns the settings fragment used for a sandboxed review run:
// the no-egress OS sandbox stanza plus the post-guard ask rules. It does NOT set
// permissions.defaultMode -- the review session runs under the developer's real
// environment and daemon, so no fail-closed permission mode is imposed.
func SandboxProfile() map[string]any {
	var s sandboxSettings
	s.Sandbox.Enabled = true
	s.Sandbox.Network.AllowedDomains = []string{} // deny-all
	s.Sandbox.FailIfUnavailable = true            // refuse rather than run uncontained
	s.Sandbox.AllowUnsandboxedCommands = false    // no unsandboxed escape hatch
	s.Permissions.Ask = postGuardAskRules[:]      // require approval before posting

	// Round-trip through JSON to a generic map so the caller can merge it into
	// the existing settings document.
	data, _ := json.Marshal(s)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

// ApplyReviewSettings merges the review-session settings into a provisioned
// instance's .claude/settings.json and re-verifies they survived the merge. The
// post-guard ask rules are always merged in (dedup, preserving any existing ask
// entries and other permission keys). When sandbox is true the no-egress sandbox
// stanza is written (fully owned, so no pre-existing sandbox config can relax the
// posture). The re-verification is the per-instance check that runs before
// launch; a dropped or relaxed stanza means the PR must not be launched.
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
		settings["sandbox"] = SandboxProfile()["sandbox"]
	}

	// Always merge the post-guard ask rules into permissions.ask, deduping and
	// preserving any existing ask entries and other permission keys.
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	existing, _ := perms["ask"].([]any)
	have := make(map[string]bool, len(existing))
	for _, v := range existing {
		if s, ok := v.(string); ok {
			have[s] = true
		}
	}
	for _, rule := range postGuardAskRules {
		if !have[rule] {
			existing = append(existing, rule)
			have[rule] = true
		}
	}
	perms["ask"] = existing
	settings["permissions"] = perms

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
// review-session settings survived the merge. The post-guard ask rules are always
// required. When sandbox is true it additionally asserts the no-egress sandbox
// stanza (enabled, empty allowedDomains, failIfUnavailable, no unsandboxed escape
// hatch).
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
	}
	perms, ok := merged["permissions"].(map[string]any)
	if !ok {
		return fmt.Errorf("review settings check: permissions stanza missing")
	}
	ask, _ := perms["ask"].([]any)
	have := make(map[string]bool, len(ask))
	for _, v := range ask {
		if s, ok := v.(string); ok {
			have[s] = true
		}
	}
	for _, rule := range postGuardAskRules {
		if !have[rule] {
			return fmt.Errorf("review settings check: post-guard ask rule %q missing", rule)
		}
	}
	return nil
}
