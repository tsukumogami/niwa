package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// WorkspaceOverlay is parsed from workspace-overlay.toml in the overlay clone.
// It carries additive configuration (sources, groups, repos, content) and
// override configuration (hooks, settings, env, files) that MergeWorkspaceOverlay
// layers on top of the base WorkspaceConfig. It is intentionally a distinct type
// from WorkspaceConfig — overlay TOML has different validation rules and lacks
// workspace metadata fields.
type WorkspaceOverlay struct {
	Sources []OverlaySourceConfig   `toml:"sources"`
	Groups  map[string]GroupConfig  `toml:"groups"`
	Repos   map[string]RepoOverride `toml:"repos"`
	Claude  OverlayClaudeConfig     `toml:"claude"`
	Env     EnvConfig               `toml:"env"`
	Files   map[string]string       `toml:"files,omitempty"`
}

// OverlaySourceConfig defines a GitHub org source in the overlay. Unlike the
// base SourceConfig, the Repos field is required — auto-discovery is not
// permitted in overlay sources.
type OverlaySourceConfig struct {
	Org   string   `toml:"org"`
	Repos []string `toml:"repos"`
}

// OverlayClaudeConfig is the Claude section of workspace-overlay.toml.
// It carries hooks (appended to base), settings (base-wins per key), and
// content (additive repos). It does not carry Enabled, Plugins, or Marketplaces.
type OverlayClaudeConfig struct {
	Hooks    HooksConfig          `toml:"hooks"`
	Settings SettingsConfig       `toml:"settings"`
	Content  OverlayContentConfig `toml:"content"`
}

// OverlayContentConfig holds the content.repos map for the overlay.
type OverlayContentConfig struct {
	Repos map[string]OverlayContentRepoConfig `toml:"repos"`
}

// OverlayContentRepoConfig is a content entry in the overlay. Exactly one of
// Source or Overlay must be set:
//   - Source: the overlay adds a new content entry for a repo not in the base.
//   - Overlay: the overlay appends a file to the base repo's CLAUDE.local.md.
type OverlayContentRepoConfig struct {
	Source  string `toml:"source"`
	Overlay string `toml:"overlay"`
}

// ParseOverlay reads workspace-overlay.toml from path and returns a validated
// WorkspaceOverlay. Validation rejects:
//   - Absolute paths and ".." components in any path field.
//   - Hook script paths that are not relative.
//   - [files] destination paths beginning with ".claude/" or ".niwa/".
//   - Content entries where both source and overlay are set, or neither is set.
//   - Sources without an explicit repos list (no auto-discovery).
func ParseOverlay(path string) (*WorkspaceOverlay, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading overlay config: %w", err)
	}

	var o WorkspaceOverlay
	if err := toml.Unmarshal(data, &o); err != nil {
		return nil, fmt.Errorf("parsing overlay config: %w", err)
	}

	if err := validateOverlay(&o); err != nil {
		return nil, err
	}

	return &o, nil
}

// validateOverlay applies all overlay-specific validation rules.
func validateOverlay(o *WorkspaceOverlay) error {
	// Sources: every source must have an explicit repos list.
	for i, src := range o.Sources {
		if src.Org == "" {
			return fmt.Errorf("overlay sources[%d]: org is required", i)
		}
		if len(src.Repos) == 0 {
			return fmt.Errorf("overlay sources[%d] (org %q): repos list is required; auto-discovery is not permitted in overlay sources", i, src.Org)
		}
	}

	// Env.Files: no absolute paths or ".." components.
	for _, f := range o.Env.Files {
		if err := validateContentSource("overlay.env.files", f); err != nil {
			return err
		}
	}

	// Files: destination values must not be absolute, must not contain "..",
	// and must not target protected directories (.claude/ or .niwa/).
	for src, dest := range o.Files {
		if dest == "" {
			continue
		}
		if err := validateContentSource(fmt.Sprintf("overlay.files[%q]", src), dest); err != nil {
			return err
		}
		if isProtectedDestination(dest) {
			return fmt.Errorf("overlay.files[%q]: destination %q targets a protected directory (.claude/ or .niwa/)", src, dest)
		}
	}

	// Claude.Hooks: all hook script paths must be relative (not absolute, no "..").
	for event, entries := range o.Claude.Hooks {
		for i, entry := range entries {
			for j, script := range entry.Scripts {
				if filepath.IsAbs(script) {
					return fmt.Errorf("overlay.claude.hooks.%s[%d].scripts[%d]: hook script path %q must be relative", event, i, j, script)
				}
				if err := validateContentSource(fmt.Sprintf("overlay.claude.hooks.%s[%d].scripts[%d]", event, i, j), script); err != nil {
					return err
				}
			}
		}
	}

	// Claude.Content.Repos: exactly one of source or overlay must be set.
	for repoName, entry := range o.Claude.Content.Repos {
		hasSource := entry.Source != ""
		hasOverlay := entry.Overlay != ""
		if hasSource && hasOverlay {
			return fmt.Errorf("overlay.claude.content.repos.%s: exactly one of source or overlay must be set, not both", repoName)
		}
		if !hasSource && !hasOverlay {
			return fmt.Errorf("overlay.claude.content.repos.%s: exactly one of source or overlay must be set; neither is set", repoName)
		}
		// Validate the path that is set.
		if hasSource {
			if err := validateContentSource(fmt.Sprintf("overlay.claude.content.repos.%s.source", repoName), entry.Source); err != nil {
				return err
			}
		}
		if hasOverlay {
			if err := validateContentSource(fmt.Sprintf("overlay.claude.content.repos.%s.overlay", repoName), entry.Overlay); err != nil {
				return err
			}
		}
	}

	return nil
}

// isProtectedDestination reports whether a file destination path targets a
// directory that overlay authors are not permitted to write into (.claude/ or
// .niwa/ subtrees). These directories contain generated artifacts and Claude
// Code configuration that must not be overwritten by additive overlay keys.
func isProtectedDestination(dest string) bool {
	// Normalize slashes for consistent prefix matching.
	d := filepath.ToSlash(dest)
	return strings.HasPrefix(d, ".claude/") || strings.HasPrefix(d, ".niwa/")
}

// DeriveOverlayURL converts a workspace source URL into a convention overlay
// URL of the form "org/repo-overlay". Accepts HTTPS URLs
// (https://github.com/org/repo[.git]), SSH URLs (git@github.com:org/repo.git),
// and shorthand (org/repo). Returns ok=false when the input cannot be parsed.
func DeriveOverlayURL(sourceURL string) (conventionURL string, ok bool) {
	org, repo, parsed := parseOrgRepo(sourceURL)
	if !parsed {
		return "", false
	}
	return org + "/" + repo + "-overlay", true
}

// parseOrgRepo extracts (org, repo) from an HTTPS URL, SSH URL, or shorthand.
// Strips a trailing ".git" suffix from the repo name.
func parseOrgRepo(s string) (org, repo string, ok bool) {
	s = strings.TrimSpace(s)

	switch {
	case strings.HasPrefix(s, "https://"):
		// https://github.com/org/repo[.git]
		rest := strings.TrimPrefix(s, "https://")
		// drop host
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return "", "", false
		}
		rest = rest[slash+1:]
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return "", "", false
		}
		org = parts[0]
		repo = strings.TrimSuffix(parts[1], ".git")
		if repo == "" {
			return "", "", false
		}
		return org, repo, true

	case strings.HasPrefix(s, "git@"):
		// git@github.com:org/repo.git
		colon := strings.Index(s, ":")
		if colon < 0 {
			return "", "", false
		}
		rest := s[colon+1:]
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return "", "", false
		}
		org = parts[0]
		repo = strings.TrimSuffix(parts[1], ".git")
		if repo == "" {
			return "", "", false
		}
		return org, repo, true

	default:
		// shorthand org/repo
		parts := strings.SplitN(s, "/", 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return "", "", false
		}
		// reject anything that looks like an absolute path or has a scheme
		if strings.Contains(parts[0], ":") || strings.HasPrefix(s, "/") {
			return "", "", false
		}
		org = parts[0]
		repo = strings.TrimSuffix(parts[1], ".git")
		if repo == "" {
			return "", "", false
		}
		return org, repo, true
	}
}

// OverlayDir returns the local directory where the overlay repo is cloned.
// The path is $XDG_CONFIG_HOME/niwa/overlays/<org>-<repo>/ (falling back to
// $HOME/.config/niwa/overlays/<org>-<repo>/ when XDG_CONFIG_HOME is unset).
// overlayURL may be a shorthand (org/repo), HTTPS URL, or SSH URL.
func OverlayDir(overlayURL string) (string, error) {
	org, repo, ok := parseOrgRepo(overlayURL)
	if !ok {
		return "", fmt.Errorf("cannot parse overlay URL %q", overlayURL)
	}
	dirName := org + "-" + repo

	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determining home directory: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "niwa", "overlays", dirName), nil
}

// CloneOrSyncOverlay clones the overlay repo to dir when dir does not exist or
// contains no valid git repository (returning firstTime=true). When a valid
// clone already exists it pulls with --ff-only (returning firstTime=false).
func CloneOrSyncOverlay(url, dir string) (firstTime bool, err error) {
	if !isValidGitDir(dir) {
		// Clone fresh.
		if mkErr := os.MkdirAll(filepath.Dir(dir), 0o755); mkErr != nil {
			return true, fmt.Errorf("creating overlay parent directory: %w", mkErr)
		}
		cmd := exec.Command("git", "clone", url, dir)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if runErr := cmd.Run(); runErr != nil {
			return true, fmt.Errorf("cloning overlay %s: %w", url, runErr)
		}
		return true, nil
	}

	// Pull existing clone.
	cmd := exec.Command("git", "-C", dir, "pull", "--ff-only")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if runErr := cmd.Run(); runErr != nil {
		return false, fmt.Errorf("syncing overlay: %w", runErr)
	}
	return false, nil
}

// isValidGitDir returns true when dir contains a .git entry.
func isValidGitDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// HeadSHA returns the current HEAD commit SHA of the git repository at dir.
func HeadSHA(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("reading HEAD SHA: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
