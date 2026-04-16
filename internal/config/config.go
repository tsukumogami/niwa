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

// ClaudeConfig is the workspace-level Claude configuration under [claude].
// It carries every Claude-related field: the common subset used at override
// positions (Enabled, Plugins, Hooks, Settings, Env) plus workspace-scoped
// fields (Marketplaces, Content) that don't flow through override merges.
// For override positions (RepoOverride.Claude, InstanceConfig.Claude,
// GlobalOverride.Claude), use ClaudeOverride instead.
type ClaudeConfig struct {
	Enabled *bool     `toml:"enabled,omitempty"`
	Plugins *[]string `toml:"plugins,omitempty"`
	// Marketplaces is workspace-wide. Not merged from per-repo overrides.
	Marketplaces []string        `toml:"marketplaces,omitempty"`
	Hooks        HooksConfig     `toml:"hooks,omitempty"`
	Settings     SettingsConfig  `toml:"settings,omitempty"`
	Env          ClaudeEnvConfig `toml:"env,omitempty"`
	// Content declares the CLAUDE.md content hierarchy under
	// [claude.content]. Workspace-scoped: per-repo overrides are not
	// honored via RepoOverride.Claude. Migrated from the deprecated
	// top-level [content] in v0.7; the old path is accepted as an alias
	// with a deprecation warning until v1.0.
	Content ContentConfig `toml:"content,omitempty"`
}

// ClaudeOverride is the narrower Claude configuration used at override
// positions where workspace-scoped fields (Content, Marketplaces) are
// not meaningful. Keeping Content and Marketplaces off this type makes
// [repos.<name>.claude.content] (and marketplaces) surface as a TOML
// "unknown field" warning automatically -- no runtime validation
// needed.
type ClaudeOverride struct {
	Enabled  *bool           `toml:"enabled,omitempty"`
	Plugins  *[]string       `toml:"plugins,omitempty"`
	Hooks    HooksConfig     `toml:"hooks,omitempty"`
	Settings SettingsConfig  `toml:"settings,omitempty"`
	Env      ClaudeEnvConfig `toml:"env,omitempty"`
}

// EnvVarsTable holds a map of env key→value paired with the three
// requirement-description sub-tables (required / recommended / optional)
// that classify the key's importance when it's missing at resolve time
// (PRD R33/R34).
//
// The table's top-level string entries populate Values; the reserved
// nested tables required/recommended/optional populate Required,
// Recommended, and Optional.
//
// TOML authors write:
//
//	[env.vars]
//	LOG_LEVEL = "debug"
//	[env.vars.required]
//	GH_TOKEN = "GitHub token used by niwa apply"
//
// and the decoder routes each sub-key to the right field via a custom
// UnmarshalTOML hook (see env_tables.go).
type EnvVarsTable struct {
	Values      map[string]MaybeSecret `toml:"-"`
	Required    map[string]string      `toml:"required,omitempty"`
	Recommended map[string]string      `toml:"recommended,omitempty"`
	Optional    map[string]string      `toml:"optional,omitempty"`
}

// ClaudeEnvConfig declares env vars for the Claude Code settings.local.json env
// block. Promote lists keys to pull from the resolved [env] pipeline. Vars and
// Secrets are sensitivity-coded siblings (PRD R33): both are inline key→value
// maps usable for settings-only vars, but values under [claude.env.secrets]
// are wrapped by the resolver into secret.Value regardless of whether the
// configured value is a vault:// reference. When a key appears in both the
// [env] pipeline (via Promote) and Vars/Secrets, the inline entry wins.
//
// Vars and Secrets carry the same three-level requirement sub-tables as
// EnvConfig (required / recommended / optional).
type ClaudeEnvConfig struct {
	Promote []string     `toml:"promote,omitempty"`
	Vars    EnvVarsTable `toml:"vars,omitempty"`
	Secrets EnvVarsTable `toml:"secrets,omitempty"`
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
	Files     map[string]string       `toml:"files,omitempty"`
	Instance  InstanceConfig          `toml:"instance,omitempty"`
	Channels  map[string]any          `toml:"channels"` // placeholder
	// Vault carries the optional [vault] block (anonymous [vault.provider]
	// or named [vault.providers.<name>] shape, plus [vault].team_only).
	// nil when the config declares no vault providers.
	Vault *VaultRegistry `toml:"vault,omitempty"`
}

// InstanceConfig holds overrides for the workspace instance root.
// Uses the same fields and merge semantics as RepoOverride but applies
// to the instance root directory (above all repos).
type InstanceConfig struct {
	Claude *ClaudeOverride   `toml:"claude,omitempty"`
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
	// VaultScope (PRD D-11) selects which [workspaces.<scope>] entry in
	// the personal overlay applies to this workspace. When unset, the
	// personal overlay falls back to matching on workspace source-org
	// name (see internal/workspace/scope.go).
	VaultScope string `toml:"vault_scope,omitempty"`
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
// "permissions" (values: "bypass", "ask"). Values are MaybeSecret so a vault
// reference can back a setting value per PRD R3; the plaintext path still
// accepts raw strings through MaybeSecret's TextUnmarshaler.
type SettingsConfig map[string]MaybeSecret

// EnvConfig defines environment configuration under [env]. It carries a list
// of env files, a non-sensitive var map ([env.vars]) and a sensitive var map
// ([env.secrets]), plus the three requirement-description sub-tables under
// each (required / recommended / optional).
type EnvConfig struct {
	Files   []string     `toml:"files,omitempty"`
	Vars    EnvVarsTable `toml:"vars,omitempty"`
	Secrets EnvVarsTable `toml:"secrets,omitempty"`
}

// RepoOverride holds per-repo configuration overrides.
type RepoOverride struct {
	URL      string            `toml:"url,omitempty"`
	Group    string            `toml:"group,omitempty"`
	Branch   string            `toml:"branch,omitempty"`
	Scope    string            `toml:"scope,omitempty"`
	Claude   *ClaudeOverride   `toml:"claude,omitempty"`
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
// Returns warnings for unknown fields (forward-compatibility) and for the
// deprecated [content] path.
func Parse(data []byte) (*ParseResult, error) {
	var cfg WorkspaceConfig
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	var warnings []string

	// Handle deprecated [content] alias. The canonical location is
	// [claude.content]; [content] is accepted through the deprecation
	// window (until v1.0) with a warning. Both forms together is a
	// configuration error -- we don't want silent precedence rules.
	legacyHasContent := !isContentConfigZero(cfg.Content)
	canonicalHasContent := !isContentConfigZero(cfg.Claude.Content)
	switch {
	case legacyHasContent && canonicalHasContent:
		return nil, fmt.Errorf("config uses both [content] and [claude.content]; " +
			"pick one -- [claude.content] is canonical, [content] is deprecated")
	case legacyHasContent:
		cfg.Claude.Content = cfg.Content
		cfg.Content = ContentConfig{}
		warnings = append(warnings,
			"[content] is deprecated; use [claude.content] instead (removed at v1.0)")
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	// Validate [vault] shape (anon-vs-named, kind requirement).
	if err := cfg.Vault.Validate("workspace config"); err != nil {
		return nil, err
	}

	// Post-parse vault:// ref validation (R3 deny list + same-file
	// provider-name resolution).
	if err := validateNoVaultRefs(&cfg); err != nil {
		return nil, err
	}

	for _, key := range md.Undecoded() {
		warnings = append(warnings, fmt.Sprintf("unknown config field: %s", key))
	}

	return &ParseResult{Config: &cfg, Warnings: warnings}, nil
}

// isContentConfigZero reports whether a ContentConfig carries any data.
// A zero ContentConfig has an empty Workspace source and nil/empty
// Groups and Repos maps.
func isContentConfigZero(c ContentConfig) bool {
	if c.Workspace.Source != "" {
		return false
	}
	if len(c.Groups) > 0 {
		return false
	}
	if len(c.Repos) > 0 {
		return false
	}
	return true
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
	// Reads from cfg.Claude.Content because Parse() migrates the legacy
	// top-level [content] into [claude.content] before validate() runs.
	if err := validateContentSource("claude.content.workspace.source", cfg.Claude.Content.Workspace.Source); err != nil {
		return err
	}
	for name, entry := range cfg.Claude.Content.Groups {
		if err := validateContentSource(fmt.Sprintf("claude.content.groups.%s.source", name), entry.Source); err != nil {
			return err
		}
	}
	for name, entry := range cfg.Claude.Content.Repos {
		if err := validateContentSource(fmt.Sprintf("claude.content.repos.%s.source", name), entry.Source); err != nil {
			return err
		}
		for subdir, src := range entry.Subdirs {
			if err := validateContentSource(fmt.Sprintf("claude.content.repos.%s.subdirs.%s", name, subdir), src); err != nil {
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

// GlobalOverride defines the global config layer that applies on top of
// workspace config before per-repo overrides. Fields mirror RepoOverride
// but omit repo-specific fields (URL, Branch, Group, Scope, SetupDir)
// and Claude.Enabled.
//
// Vault is the personal-overlay counterpart to WorkspaceConfig.Vault and
// accepts the same anonymous-or-named shapes. A personal overlay may
// declare its own providers for a workspace; the resolver (Issue 4)
// stacks team and personal bundles per-source-org.
type GlobalOverride struct {
	Claude *ClaudeOverride   `toml:"claude,omitempty"`
	Env    EnvConfig         `toml:"env,omitempty"`
	Files  map[string]string `toml:"files,omitempty"`
	Vault  *VaultRegistry    `toml:"vault,omitempty"`
}

// GlobalConfigOverride is the top-level structure parsed from the global
// config repo's niwa.toml (or equivalent). It defines a [global] section
// applied to all workspaces and per-workspace overrides under [workspaces].
type GlobalConfigOverride struct {
	Global     GlobalOverride            `toml:"global"`
	Workspaces map[string]GlobalOverride `toml:"workspaces"`
}

// ParseGlobalConfigOverride parses TOML bytes into a GlobalConfigOverride and
// validates path safety: Files destination values and Env.Files source paths
// must not contain ".." traversal or be absolute paths.
func ParseGlobalConfigOverride(data []byte) (*GlobalConfigOverride, error) {
	var cfg GlobalConfigOverride
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing global config override: %w", err)
	}
	if err := validateGlobalOverridePaths("global", cfg.Global); err != nil {
		return nil, err
	}
	if err := cfg.Global.Vault.Validate("global overlay"); err != nil {
		return nil, err
	}
	for name, ws := range cfg.Workspaces {
		if err := validateGlobalOverridePaths(fmt.Sprintf("workspaces.%s", name), ws); err != nil {
			return nil, err
		}
		if err := ws.Vault.Validate(fmt.Sprintf("workspaces.%s overlay", name)); err != nil {
			return nil, err
		}
	}
	return &cfg, nil
}

// validateGlobalOverridePaths checks that Files destination values and
// Env.Files source paths in a GlobalOverride are safe (no ".." or absolute).
func validateGlobalOverridePaths(prefix string, g GlobalOverride) error {
	for src, dest := range g.Files {
		if dest == "" {
			continue // empty value suppresses a workspace mapping, no destination to validate
		}
		if err := validateContentSource(fmt.Sprintf("%s.files[%q]", prefix, src), dest); err != nil {
			return err
		}
	}
	for _, src := range g.Env.Files {
		if err := validateContentSource(fmt.Sprintf("%s.env.files", prefix), src); err != nil {
			return err
		}
	}
	return nil
}

// validateSubdirKey ensures a subdirectory key resolves within its repo
// directory and doesn't escape via ".." or absolute path components.
func validateSubdirKey(repoName, subdir string) error {
	if filepath.IsAbs(subdir) {
		return fmt.Errorf("claude.content.repos.%s.subdirs key %q: absolute paths are not allowed", repoName, subdir)
	}
	// Clean the path and verify it doesn't escape.
	cleaned := filepath.Clean(subdir)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("claude.content.repos.%s.subdirs key %q: must resolve within the repo directory", repoName, subdir)
	}
	return nil
}
