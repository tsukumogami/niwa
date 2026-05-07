package workspace

import (
	"context"
	"errors"
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
// VaultRegistry and, for each spec whose (kind, project) matches a
// credential entry the pool can supply, authenticates and injects
// the resulting token into the provider's config map. The token
// flows through to Factory.Open via the existing
// ProviderConfig["token"] mechanism.
//
// The pool argument owns both the eager local-file layer and the
// optional lazy vault layer (added in I7). Each spec's lookup also
// appends an AuditRecord to the pool, so downstream phases can read
// pool.AuditLog() to render audit surfaces (state save, R12 stderr,
// `niwa status --audit-auth`).
//
// Modifies vr's provider configs in place. A nil vr or nil pool is
// a no-op.
func injectProviderTokens(ctx context.Context, pool *CredentialPool, vr *config.VaultRegistry) error {
	if vr == nil || pool == nil {
		return nil
	}

	// Handle anonymous singular provider.
	if vr.Provider != nil {
		project, _ := vr.Provider.Config["project"].(string)
		entry, _, err := pool.Lookup(ctx, vr.Provider.Kind, project)
		if err != nil {
			if !isSoftenable(err) {
				return err
			}
			// Soft path (PRD R13.1): vault unreachable. Record was
			// already buffered on the pool by lookupVault; continue
			// without injecting a token. The backend's later
			// universal-auth call will fall through to its CLI
			// session (R13.1 success) or fail (R13.2 — backend
			// error propagates).
		} else if entry != nil {
			token, authErr := authenticateEntry(ctx, entry)
			if authErr != nil {
				// PRD R13.6: wrap with the (kind, project) context
				// so users can identify which credential failed
				// (AC-22).
				return fmt.Errorf("authenticating credential for vault provider (kind=%q, project=%q) via %s: %w",
					vr.Provider.Kind, project, sourceLabel(entry), authErr)
			}
			if vr.Provider.Config == nil {
				vr.Provider.Config = map[string]any{}
			}
			vr.Provider.Config["token"] = token
		}
	}

	// Handle named providers.
	for name, p := range vr.Providers {
		project, _ := p.Config["project"].(string)
		entry, _, err := pool.Lookup(ctx, p.Kind, project)
		if err != nil {
			if !isSoftenable(err) {
				return err
			}
			// Soft path: same rationale as the anonymous branch above.
			continue
		}
		if entry != nil {
			token, authErr := authenticateEntry(ctx, entry)
			if authErr != nil {
				return fmt.Errorf("authenticating credential for vault provider %q (kind=%q, project=%q) via %s: %w",
					name, p.Kind, project, sourceLabel(entry), authErr)
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

// isSoftenable reports whether a Lookup error is a recoverable
// vault-unreachable case (PRD R13.1: continue iterating; the
// aggregated warning fires once apply-side after all three
// injectProviderTokens calls). Other vault errors (R13.4 / R13.5 /
// R13.7 — body parse / missing field / unsupported version) stay
// hard.
func isSoftenable(err error) bool {
	var vue *vaultUnreachableError
	return errors.As(err, &vue)
}

// sourceLabel renders a short categorical label for a credential
// entry's origin, used in R13.6 wrap text. Today there's only one
// shape (file-or-vault entry produced by the pool); the helper
// exists so I9's stderr emitter and any future category share the
// same vocabulary if more origins appear.
func sourceLabel(entry *ProviderAuthEntry) string {
	if entry == nil {
		return "unknown source"
	}
	return "machine-identity entry"
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
