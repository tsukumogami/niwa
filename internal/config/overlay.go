package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// WorkspaceOverlay is parsed from workspace-overlay.toml in the overlay clone.
// It carries additive configuration (sources, groups, repos, content, MCP
// servers) and override configuration (hooks, settings, env, files) that
// MergeWorkspaceOverlay layers on top of the base WorkspaceConfig. It is
// intentionally a distinct type from WorkspaceConfig — overlay TOML has
// different validation rules and lacks workspace metadata fields.
type WorkspaceOverlay struct {
	Sources  []OverlaySourceConfig   `toml:"sources"`
	Groups   map[string]GroupConfig  `toml:"groups"`
	Repos    map[string]RepoOverride `toml:"repos"`
	Claude   OverlayClaudeConfig     `toml:"claude"`
	Env      EnvConfig               `toml:"env"`
	Files    map[string]string       `toml:"files,omitempty"`
	Instance OverlayInstanceConfig   `toml:"instance,omitempty"`
	Vault    *VaultRegistry          `toml:"vault,omitempty"`
	// MCP declares servers in the same [mcp.servers.<name>] shape the base
	// config takes, so a server whose name or credentials are private can be
	// declared where only the people who can clone the overlay see it. A name
	// the base config also declares is the base config's: the apply pipeline
	// drops the overlay entry with a warning before resolving anything in it,
	// and MergeWorkspaceOverlay skips it too. The vault:// values that survive
	// resolve against the overlay's own [vault] providers, like its [env].
	MCP MCPConfig `toml:"mcp,omitempty"`
	// Workspace is a tombstone, not configuration. Nothing reads it into the
	// merged config -- MergeWorkspaceOverlay never assigns Workspace at all --
	// so every field under it is inert by construction. It is decoded solely
	// so an author who writes the stanza is told it does nothing, instead of
	// watching a security-relevant setting be silently ignored.
	Workspace OverlayWorkspaceTombstone `toml:"workspace"`
}

// OverlayInstanceConfig is the [instance] section of workspace-overlay.toml.
// Only the verbatim file-distribution table is carried: an overlay can place
// files at each instance root under their exact names, which is how a private
// overlay distributes a file that must keep its name to be read at all (a
// project .mcp.json, say) without declaring it in the public base config.
// Sources resolve in the overlay clone, not the base config directory, and
// the base [instance.files] table wins per destination on collision.
type OverlayInstanceConfig struct {
	Files map[string]string `toml:"files,omitempty"`
}

// OverlayWorkspaceTombstone decodes the [workspace] settings an overlay author
// might reasonably expect to be able to set, for the sole purpose of warning
// that they are not honored here.
//
// Only strict_secrets is listed today because it is the one the placement
// decision was made against: strict mode changes what a first run does for a
// contributor, and the contributor cannot read the overlay that would be
// changing it. Add a field here when a new [workspace] setting is worth the
// same warning, never to make one take effect.
type OverlayWorkspaceTombstone struct {
	StrictSecrets *bool `toml:"strict_secrets,omitempty"`
}

// TombstoneWarnings returns one warning per inert [workspace] setting the
// overlay declares, for the caller to surface through whatever diagnostic
// channel it owns. An overlay that declares none returns nil, which is the
// common case.
//
// The warnings are returned rather than printed because ParseOverlay runs on
// two paths with different sinks (the apply pipeline's deferred reporter and
// the worktree path's stderr), and neither is reachable from this package.
func (o *WorkspaceOverlay) TombstoneWarnings() []string {
	if o == nil {
		return nil
	}
	var out []string
	if o.Workspace.StrictSecrets != nil {
		out = append(out, "workspace overlay sets [workspace] strict_secrets, which is ignored: "+
			"strict mode is a base-workspace setting so a visibility overlay cannot change what a first run does. "+
			"Set strict_secrets in the workspace config, or pass --strict-secrets.")
	}
	return out
}

// OverlaySourceConfig defines a GitHub org source in the overlay. Unlike the
// base SourceConfig, the Repos field is required — auto-discovery is not
// permitted in overlay sources.
type OverlaySourceConfig struct {
	Org   string   `toml:"org"`
	Repos []string `toml:"repos"`
}

// OverlayClaudeConfig is the Claude section of workspace-overlay.toml.
// It carries hooks (appended to base), settings (base-wins per key),
// content (additive repos), marketplaces (append-union), plugins
// (append-union), and trust_instances (used when the base leaves it out). It
// does not carry Enabled.
//
// Marketplaces and Plugins use append-union semantics: entries already
// present in the base config are silently skipped; new entries are appended
// after the base config's entries. This lets a private overlay add marketplace
// sources that reference overlay-managed repos (via repo: prefix) without
// exposing those repo names in the public base config.
type OverlayClaudeConfig struct {
	Hooks        HooksConfig          `toml:"hooks"`
	Settings     SettingsConfig       `toml:"settings"`
	Content      OverlayContentConfig `toml:"content"`
	Marketplaces []string             `toml:"marketplaces,omitempty"`
	Plugins      []string             `toml:"plugins,omitempty"`
	// TrustInstances is the overlay's [claude] trust_instances. It applies
	// only when the base config leaves the key out; see
	// ClaudeConfig.TrustInstances.
	TrustInstances *bool `toml:"trust_instances,omitempty"`
}

// OverlayContentConfig holds the content groups and repos maps for the overlay.
type OverlayContentConfig struct {
	Groups map[string]OverlayContentGroupConfig `toml:"groups"`
	Repos  map[string]OverlayContentRepoConfig  `toml:"repos"`
}

// OverlayContentGroupConfig is a content entry for a group in the overlay.
// Only groups not already defined in the base config may be added; base wins on collision.
type OverlayContentGroupConfig struct {
	Source string `toml:"source"`
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
//   - [mcp.servers.*] vault:// values naming a provider the overlay itself
//     does not declare.
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

	// Files: source keys and destination values must not be absolute, must not
	// contain "..", and destinations must not target protected directories
	// (.claude/ or .niwa/).
	if err := validateOverlayFileTable("overlay.files", o.Files); err != nil {
		return err
	}

	// Instance.Files: same rules as Files. Destinations land at each instance
	// root, where .claude/ and .niwa/ are just as protected as they are in a
	// repository.
	if err := validateOverlayFileTable("overlay.instance.files", o.Instance.Files); err != nil {
		return err
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

	// Claude.Content.Groups: source must be set and must be a valid relative path.
	for groupName, entry := range o.Claude.Content.Groups {
		if entry.Source == "" {
			return fmt.Errorf("overlay.claude.content.groups.%s: source is required", groupName)
		}
		if err := validateContentSource(fmt.Sprintf("overlay.claude.content.groups.%s.source", groupName), entry.Source); err != nil {
			return err
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

	// MCP: a vault:// value must name a provider this file declares, the
	// same same-file rule the base config's parse applies to its own
	// servers, and through the same walker so the error reads the same.
	// Without it, a reference to a provider only the base declares would
	// not fail at all: the overlay's bundle cannot answer it, the resolver
	// marks it rather than erroring, and the header would quietly go
	// missing from the generated file. The rest of an overlay server's
	// validation (transport, url or command) runs on the merged config at
	// generation time, where base and overlay servers share one code path.
	if len(o.MCP.Servers) > 0 {
		scoped := &WorkspaceConfig{MCP: o.MCP, Vault: o.Vault}
		if err := walkVaultRefsForUnknownProvider(scoped, o.Vault.KnownProviderNames()); err != nil {
			return err
		}
	}

	return nil
}

// validateOverlayFileTable applies the shared rules for an overlay
// file-distribution table (per-repo [files] or [instance.files]): source keys
// must be non-empty and relative with no "..", and destinations must be
// relative with no "..", must not resolve to the target root itself, and must
// not target a protected directory. Destinations are judged on their cleaned
// form so a spelling variant ("./.claude/x", "a//b") obeys the same rules as
// the plain one.
func validateOverlayFileTable(table string, files map[string]string) error {
	for src, dest := range files {
		if src == "" {
			return fmt.Errorf("%s: a source key is empty", table)
		}
		if err := validateContentSource(fmt.Sprintf("%s[%q] source", table, src), src); err != nil {
			return err
		}
		if dest == "" {
			continue
		}
		if err := validateContentSource(fmt.Sprintf("%s[%q]", table, src), dest); err != nil {
			return err
		}
		if CleanDestination(dest) == "." {
			return fmt.Errorf("%s[%q]: destination %q resolves to the target root itself", table, src, dest)
		}
		if isProtectedDestination(dest) {
			return fmt.Errorf("%s[%q]: destination %q targets a protected directory (.claude/ or .niwa/)", table, src, dest)
		}
	}
	return nil
}

// CleanDestination normalizes a destination path for comparison and prefix
// checks: forward slashes, with redundant separators and "." components
// removed. A trailing slash is semantic in a destination (place the file
// under this directory), so callers keep the original string for writing and
// use the cleaned form only to judge it. The overlay merge uses it too, so a
// collision between two spellings of one destination is judged by the same
// rule validation applies.
func CleanDestination(dest string) string {
	return path.Clean(filepath.ToSlash(dest))
}

// isProtectedDestination reports whether a file destination path targets a
// directory that overlay authors are not permitted to write into (.claude/ or
// .niwa/ subtrees). These directories contain generated artifacts and Claude
// Code configuration that must not be overwritten by additive overlay keys.
// The judgment runs on the cleaned path, so "./.claude/x" and ".claude//x"
// are as protected as ".claude/x", and the bare directories themselves
// (".claude", ".claude/") are protected too: with a trailing slash either is
// a valid directory destination that would drop a file straight into it.
func isProtectedDestination(dest string) bool {
	d := CleanDestination(dest)
	return d == ".claude" || d == ".niwa" ||
		strings.HasPrefix(d, ".claude/") || strings.HasPrefix(d, ".niwa/")
}

// DeriveOverlayURL converts a workspace source URL into a convention overlay
// URL. Accepts HTTPS URLs (https://github.com/org/repo[.git]), SSH URLs
// (git@github.com:org/repo.git), shorthand (org/repo), and file:// URLs
// (file:///path/to/repo[.git]). Returns ok=false when the input cannot be
// parsed.
func DeriveOverlayURL(sourceURL string) (conventionURL string, ok bool) {
	s := strings.TrimSpace(sourceURL)
	if strings.HasPrefix(s, "file://") {
		// file:///path/to/ws.git  →  file:///path/to/ws-overlay.git
		path := strings.TrimPrefix(s, "file://")
		path = strings.TrimSuffix(path, ".git")
		return "file://" + path + "-overlay.git", true
	}
	org, repo, parsed := parseOrgRepo(s)
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
// The path is $XDG_CONFIG_HOME/niwa/overlays/<name>/ (falling back to
// $HOME/.config/niwa/overlays/<name>/ when XDG_CONFIG_HOME is unset).
// overlayURL may be a shorthand (org/repo), HTTPS URL, SSH URL, or file://
// URL.
func OverlayDir(overlayURL string) (string, error) {
	s := strings.TrimSpace(overlayURL)

	var dirName string
	if strings.HasPrefix(s, "file://") {
		// Derive a stable local dir name from the last path component.
		// file:///sandbox/gitserver/ws-overlay.git → file-ws-overlay
		path := strings.TrimPrefix(s, "file://")
		path = filepath.Clean(strings.TrimSuffix(path, ".git"))
		dirName = "file-" + filepath.Base(path)
	} else {
		org, repo, ok := parseOrgRepo(s)
		if !ok {
			return "", fmt.Errorf("cannot parse overlay URL %q", overlayURL)
		}
		dirName = org + "-" + repo
	}

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
