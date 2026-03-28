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

// WorkspaceConfig is the top-level configuration parsed from workspace.toml.
type WorkspaceConfig struct {
	Workspace WorkspaceMeta           `toml:"workspace"`
	Sources   []SourceConfig          `toml:"sources"`
	Groups    map[string]GroupConfig  `toml:"groups"`
	Repos     map[string]RepoOverride `toml:"repos"`
	Content   ContentConfig           `toml:"content"`
	Hooks     map[string]any          `toml:"hooks"`    // placeholder
	Settings  map[string]any          `toml:"settings"` // placeholder
	Env       map[string]any          `toml:"env"`      // placeholder
	Channels  map[string]any          `toml:"channels"` // placeholder
}

// WorkspaceMeta holds top-level workspace metadata.
type WorkspaceMeta struct {
	Name          string `toml:"name"`
	DefaultBranch string `toml:"default_branch,omitempty"`
	ContentDir    string `toml:"content_dir,omitempty"`
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

// RepoOverride holds per-repo configuration overrides.
type RepoOverride struct {
	URL      string         `toml:"url,omitempty"`
	Branch   string         `toml:"branch,omitempty"`
	Scope    string         `toml:"scope,omitempty"`
	Claude   *bool          `toml:"claude,omitempty"`
	Hooks    map[string]any `toml:"hooks,omitempty"`
	Settings map[string]any `toml:"settings,omitempty"`
	Env      map[string]any `toml:"env,omitempty"`
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

// Load parses a workspace.toml file at the given path and returns the config.
func Load(path string) (*WorkspaceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	return Parse(data)
}

// Parse decodes TOML bytes into a WorkspaceConfig and validates it.
func Parse(data []byte) (*WorkspaceConfig, error) {
	var cfg WorkspaceConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
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

	for name := range cfg.Repos {
		if !validName.MatchString(name) {
			return fmt.Errorf("repo override name %q: must match [a-zA-Z0-9._-]+", name)
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
