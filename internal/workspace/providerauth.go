package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/infisical"
)

// ProviderAuthEntry represents one [[providers]] entry from the
// local credential file (~/.config/niwa/provider-auth.toml).
type ProviderAuthEntry struct {
	Kind   string
	Config map[string]any // backend-specific fields (client_id, client_secret, api_url, etc.)
}

// providerAuthFile is the filename for the local credential file.
const providerAuthFile = "provider-auth.toml"

// providerAuthTOML is the intermediate shape used to decode the
// [[providers]] array from the credential file. Each entry is a flat
// TOML table; "kind" is pulled out into the Kind field and everything
// else lands in Config.
type providerAuthTOML struct {
	Providers []map[string]any `toml:"providers"`
}

// LoadProviderAuth reads and parses the local credential file.
// Returns nil (no error) when the file doesn't exist -- single-org
// users never create it. Returns error when the file exists but:
//   - permissions are not 0o600 (security guardrail)
//   - TOML is malformed
//   - any entry lacks a "kind" field
func LoadProviderAuth(configDir string) ([]ProviderAuthEntry, error) {
	path := filepath.Join(configDir, providerAuthFile)

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("checking provider-auth.toml: %w", err)
	}

	perm := info.Mode().Perm()
	if perm != 0o600 {
		return nil, fmt.Errorf(
			"provider-auth.toml has permissions %04o; must be 0600 (chmod 0600 %s)",
			perm, path,
		)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading provider-auth.toml: %w", err)
	}

	var raw providerAuthTOML
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing provider-auth.toml: %w", err)
	}

	entries := make([]ProviderAuthEntry, 0, len(raw.Providers))
	for i, p := range raw.Providers {
		kindRaw, ok := p["kind"]
		if !ok {
			return nil, fmt.Errorf("provider-auth.toml: providers[%d] missing required field \"kind\"", i)
		}
		kind, ok := kindRaw.(string)
		if !ok {
			return nil, fmt.Errorf("provider-auth.toml: providers[%d] \"kind\" must be a string, got %T", i, kindRaw)
		}
		if kind == "" {
			return nil, fmt.Errorf("provider-auth.toml: providers[%d] \"kind\" must be non-empty", i)
		}

		config := make(map[string]any, len(p)-1)
		for k, v := range p {
			if k == "kind" {
				continue
			}
			config[k] = v
		}

		entries = append(entries, ProviderAuthEntry{
			Kind:   kind,
			Config: config,
		})
	}

	return entries, nil
}

// MatchProviderAuth finds the first credential entry that matches a
// given ProviderSpec. For Infisical, matching is by (kind, project).
// Returns nil if no match found (single-org fallback).
func MatchProviderAuth(spec vault.ProviderSpec, entries []ProviderAuthEntry) *ProviderAuthEntry {
	for i := range entries {
		entry := &entries[i]
		if entry.Kind != spec.Kind {
			continue
		}
		switch entry.Kind {
		case "infisical":
			// Match by project UUID.
			entryProject, _ := entry.Config["project"].(string)
			specProject, _ := spec.Config["project"].(string)
			if entryProject != "" && entryProject == specProject {
				return entry
			}
		default:
			// Future backends define their own matching keys. For now,
			// kind-only match is insufficient (too broad); skip.
		}
	}
	return nil
}

// injectProviderTokens iterates the provider specs derived from a
// VaultRegistry and, for each spec that matches a credential entry,
// authenticates and injects the resulting token into the provider's
// config map. The token flows through to Factory.Open via the
// existing ProviderConfig["token"] mechanism.
//
// Modifies vr's provider configs in place. A nil vr or empty entries
// slice is a no-op.
func injectProviderTokens(ctx context.Context, entries []ProviderAuthEntry, vr *config.VaultRegistry) error {
	if vr == nil || len(entries) == 0 {
		return nil
	}

	// Handle anonymous singular provider.
	if vr.Provider != nil {
		spec := vault.ProviderSpec{
			Kind:   vr.Provider.Kind,
			Config: vault.ProviderConfig(vr.Provider.Config),
		}
		if match := MatchProviderAuth(spec, entries); match != nil {
			token, err := authenticateEntry(ctx, match)
			if err != nil {
				return err
			}
			if vr.Provider.Config == nil {
				vr.Provider.Config = map[string]any{}
			}
			vr.Provider.Config["token"] = token
		}
	}

	// Handle named providers.
	for name, p := range vr.Providers {
		cfg := vault.ProviderConfig(p.Config)
		if cfg == nil {
			cfg = vault.ProviderConfig{}
		}
		spec := vault.ProviderSpec{
			Name:   name,
			Kind:   p.Kind,
			Config: cfg,
		}
		if match := MatchProviderAuth(spec, entries); match != nil {
			token, err := authenticateEntry(ctx, match)
			if err != nil {
				return err
			}
			if p.Config == nil {
				p.Config = map[string]any{}
			}
			p.Config["token"] = token
			vr.Providers[name] = p
		}
	}

	return nil
}

// authenticateEntry dispatches authentication to the appropriate
// backend based on the entry's Kind. Currently only Infisical is
// supported; other backends can be added here in the future.
func authenticateEntry(ctx context.Context, entry *ProviderAuthEntry) (string, error) {
	switch entry.Kind {
	case "infisical":
		return infisical.Authenticate(ctx, entry.Config)
	default:
		return "", fmt.Errorf("provider-auth: unsupported auth kind %q", entry.Kind)
	}
}

// NiwaConfigDir returns the niwa configuration directory
// (~/.config/niwa/ by default, respecting XDG_CONFIG_HOME). This is
// the parent of the global config clone directory and where
// provider-auth.toml lives.
func NiwaConfigDir() (string, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determining home directory: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "niwa"), nil
}
