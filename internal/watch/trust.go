package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Claude Code persists workspace trust ONLY in the user-global ~/.claude.json, under
// projects[<abs-path>].hasTrustDialogAccepted. In an untrusted workspace it ignores
// project-level permissions.allow entries AND hook allow/ask decisions, so a review
// session run under a non-bypass permission mode would hang on the normal in-instance
// work. There is no project-local trust store and no CLI flag that grants trust while
// keeping hook decisions honored, so niwa seeds the entry itself before launch. The
// ephemeral instance is the only path it ever trusts, and the entry is removed when
// the instance is reclaimed.
//
// ~/.claude.json is a file in HOME, not under the protected ~/.claude directory, so
// writing it does not touch Claude Code's sensitive-location circuit breaker.

// trustHomeDir resolves the HOME the review session runs under (it launches with the
// developer's real environment). It is a seam tests override to point at a temp HOME.
var trustHomeDir = os.UserHomeDir

// claudeConfigPath returns the path to the user-global ~/.claude.json for the resolved
// HOME. It returns an error when HOME cannot be resolved so the caller can fall back
// to the hard-deny posture rather than trusting an empty path.
func claudeConfigPath() (string, error) {
	home, err := trustHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving HOME for the Claude Code trust store: %w", err)
	}
	if home == "" {
		return "", fmt.Errorf("resolving HOME for the Claude Code trust store: empty HOME")
	}
	return filepath.Join(home, ".claude.json"), nil
}

// trustKey is the absolute, cleaned instance path used as the projects map key. Claude
// Code keys trust on the directory the session started from (the instance root niwa
// launches in), so the key must be that path in absolute form.
func trustKey(instancePath string) (string, error) {
	abs, err := filepath.Abs(instancePath)
	if err != nil {
		return "", fmt.Errorf("resolving instance path for trust: %w", err)
	}
	return filepath.Clean(abs), nil
}

// EnsureInstanceTrusted marks instancePath as a trusted Claude Code workspace by
// adding projects[<abs>].hasTrustDialogAccepted (and hasTrustDialogHooksAccepted, so a
// hooks-trust prompt cannot hang the --bg session) to ~/.claude.json. It preserves
// every other key and project entry and writes atomically (temp file + rename) so a
// partial write cannot corrupt the daemon's config. It returns an error -- so the
// caller falls back to the hard-deny posture -- when HOME is unresolvable or the
// existing file cannot be parsed.
func EnsureInstanceTrusted(instancePath string) error {
	path, err := claudeConfigPath()
	if err != nil {
		return err
	}
	key, err := trustKey(instancePath)
	if err != nil {
		return err
	}

	config, err := readClaudeConfig(path)
	if err != nil {
		return err
	}

	projects, _ := config["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	entry, _ := projects[key].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	entry["hasTrustDialogAccepted"] = true
	entry["hasTrustDialogHooksAccepted"] = true
	projects[key] = entry
	config["projects"] = projects

	return writeClaudeConfig(path, config)
}

// RemoveInstanceTrust removes the projects[<abs>] entry EnsureInstanceTrusted added,
// preserving the rest of ~/.claude.json. A missing file or missing entry is a no-op,
// not an error, so it is safe to call best-effort on instance destroy.
func RemoveInstanceTrust(instancePath string) error {
	path, err := claudeConfigPath()
	if err != nil {
		return err
	}
	key, err := trustKey(instancePath)
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return nil // no config file -> nothing to remove
	}
	config, err := readClaudeConfig(path)
	if err != nil {
		return err
	}
	projects, _ := config["projects"].(map[string]any)
	if projects == nil {
		return nil // no projects map -> nothing to remove
	}
	if _, present := projects[key]; !present {
		return nil // entry already absent -> no-op
	}
	delete(projects, key)
	config["projects"] = projects
	return writeClaudeConfig(path, config)
}

// readClaudeConfig reads and parses ~/.claude.json into a generic map. A missing file
// yields an empty map (a fresh config niwa can seed). A present-but-unparseable file is
// an error: niwa refuses to clobber a config it cannot round-trip, so the caller falls
// back to the hard-deny posture rather than risk corrupting the daemon's state.
func readClaudeConfig(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parsing %s (refusing to overwrite an unparseable Claude config): %w", path, err)
	}
	if config == nil {
		config = map[string]any{}
	}
	return config, nil
}

// writeClaudeConfig serializes config and writes it to path atomically via a temp file
// in the same directory plus a rename, so a concurrent reader (the Claude daemon) never
// observes a half-written file. The existing file mode is preserved when present, else
// a private 0600 is used.
func writeClaudeConfig(path string, config map[string]any) error {
	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding Claude config: %w", err)
	}

	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".claude.json.tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp for Claude config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp Claude config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp Claude config: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("setting mode on temp Claude config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replacing Claude config: %w", err)
	}
	return nil
}
