// Package config handles parsing and validation of workspace.toml configuration.
package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

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
	URL    string `toml:"url,omitempty"`
	Branch string `toml:"branch,omitempty"`
	Scope  string `toml:"scope,omitempty"`
	Claude *bool  `toml:"claude,omitempty"`
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

	for _, s := range cfg.Sources {
		if s.Org == "" {
			return fmt.Errorf("source org is required")
		}
	}

	return nil
}
