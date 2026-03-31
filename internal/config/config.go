// Package config handles parsing and validation of workspace.toml configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// validName matches names that contain only alphanumerics, dots, hyphens, and underscores.
var validName = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// ClaudeConfig groups hooks and settings under a single [claude] namespace.
// On RepoOverride, Enabled controls whether Claude Code configuration is
// installed for the repo (defaults to true when nil).
type ClaudeConfig struct {
	Enabled *bool  `toml:"enabled,omitempty"`
	Plugins *[]string `toml:"plugins,omitempty"`
	// Marketplaces is workspace-wide. Not merged from per-repo overrides.
	Marketplaces []string        `toml:"marketplaces,omitempty"`
	Hooks        HooksConfig     `toml:"hooks,omitempty"`
	Settings     SettingsConfig  `toml:"settings,omitempty"`
	Env          ClaudeEnvConfig `toml:"env,omitempty"`
}

// ClaudeEnvConfig declares env vars for the Claude Code settings.local.json env
// block. Promote lists keys to pull from the resolved [env] pipeline. Vars are
// inline key-value pairs for settings-only vars. When the same key appears in
// both, Vars wins.
type ClaudeEnvConfig struct {
	Promote []string          `toml:"promote,omitempty"`
	Vars    map[string]string `toml:"vars,omitempty"`
}

// WorkspaceConfig is the top-level configuration parsed from workspace.toml.
type WorkspaceConfig struct {
	Workspace WorkspaceMeta           `toml:"workspace"`
	Sources   []SourceConfig          `toml:"sources"`
	Groups    map[string]GroupConfig  `toml:"groups"`
	Repos     map[string]RepoOverride `toml:"repos"`
	Content   ContentConfig           `toml:"content"`
	Claude    ClaudeConfig            `toml:"claude"`
	Env       EnvConfig               `toml:"env"`
	Files     map[string]string        `toml:"files,omitempty"`
	Instance  InstanceConfig          `toml:"instance,omitempty"`
	Channels  map[string]any          `toml:"channels"` // placeholder
}

// InstanceConfig holds overrides for the workspace instance root.
// Uses the same fields and merge semantics as RepoOverride but applies
// to the instance root directory (above all repos).
type InstanceConfig struct {
	Claude *ClaudeConfig     `toml:"claude,omitempty"`
	Env    EnvConfig         `toml:"env,omitempty"`
	Files  map[string]string `toml:"files,omitempty"`
}

// WorkspaceMeta holds top-level workspace metadata.
type WorkspaceMeta struct {
	Name          string `toml:"name"`
	Version       string `toml:"version,omitempty"`
	DefaultBranch string `toml:"default_branch,omitempty"`
	ContentDir    string `toml:"content_dir,omitempty"`
	SetupDir      string `toml:"setup_dir,omitempty"`
}

// ParseResult holds the parsed config and any non-fatal warnings.
type ParseResult struct {
	Config   *WorkspaceConfig
	Warnings []string
}

// SourceConfig defines a GitHub org source for repo discovery.
type SourceConfig struct {
	Org      string   `toml:"org"`
	Repos    []string `toml:"repos,omitempty"`
	MaxRepos int      `toml:"max_repos,omitempty"`
}

// GroupConfig defines a classification group for repos.
type GroupConfig struct {
	Visibility string   `toml:"visibility,omitempty"`
	Repos      []string `toml:"repos,omitempty"`
}

// HooksConfig maps hook event names (e.g., "pre_tool_use", "stop") to lists
// of hook entries. Each entry has an optional matcher (tool name filter) and
// a list of script paths.
type HooksConfig map[string][]HookEntry

// HookEntry defines a hook with an optional matcher and script paths.
// The matcher filters which tools trigger the hook (e.g., "Bash", "Edit|Write").
// Empty matcher means match all tools.
type HookEntry struct {
	Matcher string   `toml:"matcher,omitempty"`
	Scripts []string `toml:"scripts"`
}

// SettingsConfig maps setting keys to their values. The primary key today is
// "permissions" (values: "bypass", "ask").
type SettingsConfig map[string]string

// EnvConfig defines environment configuration with explicit file paths and
// key-value variable pairs.
type EnvConfig struct {
	Files []string          `toml:"files,omitempty"`
	Vars  map[string]string `toml:"vars,omitempty"`
}

// RepoOverride holds per-repo configuration overrides.
type RepoOverride struct {
	URL      string            `toml:"url,omitempty"`
	Group    string            `toml:"group,omitempty"`
	Branch   string            `toml:"branch,omitempty"`
	Scope    string            `toml:"scope,omitempty"`
	Claude   *ClaudeConfig     `toml:"claude,omitempty"`
	Env      EnvConfig         `toml:"env,omitempty"`
	Files    map[string]string `toml:"files,omitempty"`
	SetupDir *string           `toml:"setup_dir,omitempty"`
}

// ContentConfig declares the CLAUDE.md content hierarchy.
type ContentConfig struct {
	Workspace ContentEntry                `toml:"workspace"`
	Groups    map[string]ContentEntry     `toml:"groups"`
	Repos     map[string]RepoContentEntry `toml:"repos"`
}

// ContentEntry is a single content source reference.
type ContentEntry struct {
	Source string `toml:"source"`
}

// RepoContentEntry is a content entry for a repo, with optional subdirectory mappings.
type RepoContentEntry struct {
	Source  string            `toml:"source,omitempty"`
	Subdirs map[string]string `toml:"subdirs,omitempty"`
}

// Load parses a workspace.toml file at the given path and returns the config
// along with any non-fatal warnings (e.g., unknown fields).
func Load(path string) (*ParseResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	return Parse(data)
}

// Parse decodes TOML bytes into a WorkspaceConfig and validates it.
// Returns warnings for unknown fields (forward-compatibility).
func Parse(data []byte) (*ParseResult, error) {
	var cfg WorkspaceConfig
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	var warnings []string
	for _, key := range md.Undecoded() {
		warnings = append(warnings, fmt.Sprintf("unknown config field: %s", key))
	}

	return &ParseResult{Config: &cfg, Warnings: warnings}, nil
}

func validate(cfg *WorkspaceConfig) error {
	if cfg.Workspace.Name == "" {
		return fmt.Errorf("workspace.name is required")
	}

	if !validName.MatchString(cfg.Workspace.Name) {
		return fmt.Errorf("workspace.name %q: must match [a-zA-Z0-9._-]+", cfg.Workspace.Name)
	}

	for _, s := range cfg.Sources {
		if s.Org == "" {
			return fmt.Errorf("source org is required")
		}
	}

	for name := range cfg.Groups {
		if !validName.MatchString(name) {
			return fmt.Errorf("group name %q: must match [a-zA-Z0-9._-]+", name)
		}
	}

	for name, override := range cfg.Repos {
		if !validName.MatchString(name) {
			return fmt.Errorf("repo override name %q: must match [a-zA-Z0-9._-]+", name)
		}
		// Explicit repos: group requires url (url without group is a valid
		// clone URL override for discovered repos).
		if override.Group != "" && override.URL == "" {
			return fmt.Errorf("repo %q has group but no url: explicit repos require both url and group", name)
		}
	}

	// Validate content source paths don't escape the content directory.
	if err := validateContentSource("content.workspace.source", cfg.Content.Workspace.Source); err != nil {
		return err
	}
	for name, entry := range cfg.Content.Groups {
		if err := validateContentSource(fmt.Sprintf("content.groups.%s.source", name), entry.Source); err != nil {
			return err
		}
	}
	for name, entry := range cfg.Content.Repos {
		if err := validateContentSource(fmt.Sprintf("content.repos.%s.source", name), entry.Source); err != nil {
			return err
		}
		for subdir, src := range entry.Subdirs {
			if err := validateContentSource(fmt.Sprintf("content.repos.%s.subdirs.%s", name, subdir), src); err != nil {
				return err
			}
			if err := validateSubdirKey(name, subdir); err != nil {
				return err
			}
		}
	}

	return nil
}

// validateContentSource rejects source paths that contain ".." components or
// are absolute, which could escape the content directory.
func validateContentSource(field, source string) error {
	if source == "" {
		return nil
	}
	if filepath.IsAbs(source) {
		return fmt.Errorf("%s %q: absolute paths are not allowed", field, source)
	}
	for _, part := range strings.Split(filepath.ToSlash(source), "/") {
		if part == ".." {
			return fmt.Errorf("%s %q: path traversal (..) is not allowed", field, source)
		}
	}
	return nil
}

// validateSubdirKey ensures a subdirectory key resolves within its repo
// directory and doesn't escape via ".." or absolute path components.
func validateSubdirKey(repoName, subdir string) error {
	if filepath.IsAbs(subdir) {
		return fmt.Errorf("content.repos.%s.subdirs key %q: absolute paths are not allowed", repoName, subdir)
	}
	// Clean the path and verify it doesn't escape.
	cleaned := filepath.Clean(subdir)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("content.repos.%s.subdirs key %q: must resolve within the repo directory", repoName, subdir)
	}
	return nil
}
