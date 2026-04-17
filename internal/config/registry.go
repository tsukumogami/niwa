package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"unicode"

	"github.com/BurntSushi/toml"
)

// GlobalConfig is the top-level structure parsed from ~/.config/niwa/config.toml.
type GlobalConfig struct {
	Global       GlobalSettings           `toml:"global"`
	GlobalConfig GlobalConfigSource       `toml:"global_config"`
	Registry     map[string]RegistryEntry `toml:"registry"`
}

// GlobalConfigSource records the registered global config repo. The local clone
// path is derived at runtime from XDG_CONFIG_HOME so it is never stored.
type GlobalConfigSource struct {
	Repo string `toml:"repo,omitempty"`
}

// GlobalSettings holds global niwa settings.
type GlobalSettings struct {
	CloneProtocol string `toml:"clone_protocol,omitempty"`
}

// RegistryEntry records a workspace's source config file and root directory.
type RegistryEntry struct {
	Source    string `toml:"source"`
	Root      string `toml:"root"`
	SourceURL string `toml:"source_url,omitempty"`
}

// CloneProtocol returns the configured clone protocol, defaulting to "ssh".
// SSH is the default because it handles both public and private repos without
// requiring a credential helper.
func (g *GlobalConfig) CloneProtocol() string {
	if g.Global.CloneProtocol != "" {
		return g.Global.CloneProtocol
	}
	return "ssh"
}

// RegisteredNames returns a sorted, sanitized list of workspace names from
// g.Registry. Names containing ASCII controls or Unicode Cf/Zl/Zp codepoints
// are filtered out for the same reason filesystem-derived names are (see
// workspace.ValidName). Registry entries are validated at registration time,
// so in practice no entries should be filtered; the guard is defense in
// depth.
func (g *GlobalConfig) RegisteredNames() []string {
	if g == nil || len(g.Registry) == 0 {
		return []string{}
	}
	names := make([]string, 0, len(g.Registry))
	for name := range g.Registry {
		if !validRegistryName(name) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ListRegisteredWorkspaces loads the global config and returns
// RegisteredNames on it. Returns (nil, err) on config load error. Returns
// ([]string{}, nil) when the config file is absent, when the registry
// section is empty, or when no entries pass the sanitization filter.
func ListRegisteredWorkspaces() ([]string, error) {
	cfg, err := LoadGlobalConfig()
	if err != nil {
		return nil, err
	}
	return cfg.RegisteredNames(), nil
}

// validRegistryName mirrors workspace.ValidName but is duplicated here to
// avoid a config -> workspace import edge. Kept internal because the
// registration path already validates names at write time.
func validRegistryName(name string) bool {
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
		if unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}

// LookupWorkspace returns the registry entry for the given workspace name.
// It returns nil if the name is not registered.
func (g *GlobalConfig) LookupWorkspace(name string) *RegistryEntry {
	if g.Registry == nil {
		return nil
	}
	entry, ok := g.Registry[name]
	if !ok {
		return nil
	}
	return &entry
}

// SetRegistryEntry adds or updates a workspace entry in the registry.
func (g *GlobalConfig) SetRegistryEntry(name string, entry RegistryEntry) {
	if g.Registry == nil {
		g.Registry = make(map[string]RegistryEntry)
	}
	g.Registry[name] = entry
}

// GlobalConfigPath returns the path to the global config file, respecting
// XDG_CONFIG_HOME. Falls back to ~/.config/niwa/config.toml.
func GlobalConfigPath() (string, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determining home directory: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "niwa", "config.toml"), nil
}

// LoadGlobalConfig reads and parses the global config file. If the file does
// not exist, it returns an empty config with defaults (not an error).
func LoadGlobalConfig() (*GlobalConfig, error) {
	path, err := GlobalConfigPath()
	if err != nil {
		return nil, err
	}
	return LoadGlobalConfigFrom(path)
}

// LoadGlobalConfigFrom reads and parses a global config from the given path.
// If the file does not exist, it returns an empty config with defaults.
func LoadGlobalConfigFrom(path string) (*GlobalConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &GlobalConfig{}, nil
		}
		return nil, fmt.Errorf("reading global config: %w", err)
	}
	return ParseGlobalConfig(data)
}

// ParseGlobalConfig decodes TOML bytes into a GlobalConfig.
func ParseGlobalConfig(data []byte) (*GlobalConfig, error) {
	var cfg GlobalConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing global config: %w", err)
	}
	return &cfg, nil
}

// SaveGlobalConfig writes the global config to its default path.
func SaveGlobalConfig(cfg *GlobalConfig) error {
	path, err := GlobalConfigPath()
	if err != nil {
		return err
	}
	return SaveGlobalConfigTo(path, cfg)
}

// SaveGlobalConfigTo writes the global config to the given path, creating
// parent directories as needed. The file is created with 0o600 permissions
// because config.toml may contain sensitive values like repo URLs.
func SaveGlobalConfigTo(path string, cfg *GlobalConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("creating global config file: %w", err)
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(cfg); err != nil {
		return fmt.Errorf("encoding global config: %w", err)
	}
	return nil
}

// GlobalConfigDir returns the path to the global config clone directory,
// respecting XDG_CONFIG_HOME. Falls back to ~/.config/niwa/global.
func GlobalConfigDir() (string, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determining home directory: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "niwa", "global"), nil
}
