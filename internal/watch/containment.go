package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// envAllowlist is the explicit set of environment variable names the contained
// review session may carry. It is an ALLOWLIST (fail-closed): anything not
// named here is dropped, so a secret the design did not anticipate cannot leak
// through -- unlike a denylist, which fails open. The review task is read-only
// and reaches nothing but the model channel, so it needs the model/harness auth
// plus the minimum to run a process. It explicitly does NOT include the GitHub
// token or any host credential.
//
// Note HOME is deliberately absent here: it is replaced by a synthetic HOME
// (see BuildContainedEnv) so on-disk credentials under the developer's real
// home (~/.config/gh, ~/.netrc, ~/.ssh) are not reachable.
var envAllowlist = map[string]bool{
	// Anthropic / Claude model-channel auth (the one secret the session needs).
	"ANTHROPIC_API_KEY":       true,
	"ANTHROPIC_AUTH_TOKEN":    true,
	"ANTHROPIC_BASE_URL":      true,
	"CLAUDE_CODE_OAUTH_TOKEN": true,
	// Minimum to run a process.
	"PATH":     true,
	"LANG":     true,
	"LC_ALL":   true,
	"LC_CTYPE": true,
	"TERM":     true,
	"TZ":       true,
}

// deniedEnvNames is a defense-in-depth explicit-deny list checked by tests and
// asserted never to appear in a contained env even if some future edit to the
// allowlist is careless. These are the credential-bearing variables the
// containment must keep out of the session.
var deniedEnvNames = []string{
	"GITHUB_TOKEN", "GH_TOKEN", "GH_ENTERPRISE_TOKEN", "SSH_AUTH_SOCK",
}

// BuildContainedEnv builds the environment for a contained review session from
// the parent environment (typically os.Environ()), keeping only the allowlist
// and substituting a synthetic HOME. syntheticHome is a scratch directory
// inside the dispatched instance (no developer dotfiles); it becomes the
// session's HOME so credential files under the real home are absent.
//
// The result never contains the GitHub token, SSH agent socket, GH_*/GITHUB_*
// variables, or any variable outside the allowlist.
func BuildContainedEnv(parentEnv []string, syntheticHome string) []string {
	out := make([]string, 0, len(envAllowlist)+1)
	for _, kv := range parentEnv {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		name := kv[:eq]
		if name == "HOME" {
			continue // replaced by the synthetic HOME below
		}
		// Belt-and-suspenders: never carry a GH_*/GITHUB_* variable even if one
		// were mistakenly added to the allowlist.
		if strings.HasPrefix(name, "GH_") || strings.HasPrefix(name, "GITHUB_") {
			continue
		}
		if envAllowlist[name] {
			out = append(out, kv)
		}
	}
	if syntheticHome != "" {
		out = append(out, "HOME="+syntheticHome)
	}
	return out
}

// sandboxSettings is the no-egress containment profile merged into a dispatched
// instance's .claude/settings.json. An EMPTY allowedDomains is the deny-all
// posture (the live adversarial test proves the harness honors it as deny-all,
// not allow-all). permissions.defaultMode is "default", which fails closed in
// an unattended --bg session: a would-prompt tool call is rejected, not
// auto-allowed.
type sandboxSettings struct {
	Sandbox struct {
		Enabled bool `json:"enabled"`
		Network struct {
			AllowedDomains []string `json:"allowedDomains"`
		} `json:"network"`
	} `json:"sandbox"`
	Permissions struct {
		DefaultMode string `json:"defaultMode"`
	} `json:"permissions"`
}

// ContainmentProfile returns the settings fragment that enforces no egress and
// a fail-closed permission mode. It is marshaled and merged into the instance
// settings before launch.
func ContainmentProfile() map[string]any {
	var s sandboxSettings
	s.Sandbox.Enabled = true
	s.Sandbox.Network.AllowedDomains = []string{} // deny-all
	s.Permissions.DefaultMode = "default"         // fail-closed in --bg

	// Round-trip through JSON to a generic map so the caller can merge it into
	// the existing settings document.
	data, _ := json.Marshal(s)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

// VerifyContainmentApplied re-reads a merged settings document and asserts the
// sandbox stanza survived the merge: sandbox.enabled is true, allowedDomains is
// present and empty, and the permission mode is fail-closed. This is the
// per-instance re-verification that runs immediately before launch; a false
// result means the stanza was dropped or relaxed by the merge and the PR must
// NOT be launched.
func VerifyContainmentApplied(merged map[string]any) error {
	sandbox, ok := merged["sandbox"].(map[string]any)
	if !ok {
		return fmt.Errorf("containment check: sandbox stanza missing from merged settings")
	}
	enabled, _ := sandbox["enabled"].(bool)
	if !enabled {
		return fmt.Errorf("containment check: sandbox.enabled is not true")
	}
	network, ok := sandbox["network"].(map[string]any)
	if !ok {
		return fmt.Errorf("containment check: sandbox.network missing")
	}
	domains, ok := network["allowedDomains"].([]any)
	if !ok {
		return fmt.Errorf("containment check: sandbox.network.allowedDomains missing")
	}
	if len(domains) != 0 {
		return fmt.Errorf("containment check: allowedDomains must be empty (deny-all), got %d entries", len(domains))
	}
	perms, ok := merged["permissions"].(map[string]any)
	if !ok {
		return fmt.Errorf("containment check: permissions stanza missing")
	}
	// Allowlist, not denylist: assert the mode is exactly one of the intended
	// fail-closed modes. A denylist (reject only "bypassPermissions") fails open
	// -- it would accept "acceptEdits", "dontAsk", "auto", or a typo as
	// fail-closed -- which is the pattern this file's env handling and the
	// design's Decisions 3/4/7 deliberately reject.
	mode, _ := perms["defaultMode"].(string)
	if !failClosedPermissionModes[mode] {
		return fmt.Errorf("containment check: permission mode %q is not an allowed fail-closed mode", mode)
	}
	return nil
}

// failClosedPermissionModes is the allowlist of permission modes the contained
// session may run under. It matches exactly what ContainmentProfile produces
// ("default", which rejects a would-prompt call in an unattended --bg session).
// A new fail-closed mode is admitted only by being added here explicitly.
var failClosedPermissionModes = map[string]bool{
	"default": true,
}

// ApplyContainment merges the no-egress sandbox profile into a provisioned
// instance's .claude/settings.json and re-verifies that the stanza survived the
// merge, returning an error if it did not. niwa already wrote the instance
// settings during provisioning, so this is a second, containment-enforcing
// write; the re-verification is the per-instance fail-closed check that runs
// before launch (a dropped or relaxed stanza means the PR must not be
// launched). The re-verification here confirms niwa's own merge; the harness's
// downstream merge and live enforcement are proven separately by the
// adversarial egress test.
func ApplyContainment(instancePath string) error {
	settingsPath := filepath.Join(instancePath, ".claude", "settings.json")
	settings := map[string]any{}
	if data, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("apply containment: parsing %s: %w", settingsPath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("apply containment: reading %s: %w", settingsPath, err)
	}

	profile := ContainmentProfile()
	// The sandbox stanza is fully owned by the containment profile -- overwrite
	// it so no pre-existing sandbox config can relax the no-egress posture.
	settings["sandbox"] = profile["sandbox"]
	// Merge permissions at the key level: set the fail-closed defaultMode while
	// preserving any existing allow/deny/ask entries.
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	for k, v := range profile["permissions"].(map[string]any) {
		perms[k] = v
	}
	settings["permissions"] = perms

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("apply containment: encoding settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("apply containment: creating .claude dir: %w", err)
	}
	if err := os.WriteFile(settingsPath, out, 0o644); err != nil {
		return fmt.Errorf("apply containment: writing settings: %w", err)
	}

	// Re-read from disk and re-verify the stanza survived the write/merge.
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("apply containment: re-reading settings: %w", err)
	}
	var merged map[string]any
	if err := json.Unmarshal(data, &merged); err != nil {
		return fmt.Errorf("apply containment: re-parsing settings: %w", err)
	}
	if err := VerifyContainmentApplied(merged); err != nil {
		return err
	}
	return nil
}

// SyntheticHomeDir returns the path of the synthetic HOME inside an instance and
// ensures it exists. It holds no developer dotfiles.
func SyntheticHomeDir(instanceDir string) (string, error) {
	home := filepath.Join(instanceDir, ".watch-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", fmt.Errorf("creating synthetic HOME: %w", err)
	}
	return home, nil
}
