package config

import "fmt"

// VaultRegistry is the parsed [vault] table. It accepts two mutually-
// exclusive shapes:
//
//   - [vault.provider] — a single anonymous provider. Stored as Provider
//     (non-nil, Name = "").
//   - [vault.providers.<name>] — one or more named providers. Stored as
//     Providers, keyed by name.
//
// Declaring both forms in the same file is a parse error; see Validate.
//
// [vault].team_only is a workspace-level list of keys whose effective
// value must come from the team layer only, even when a personal
// overlay declares the same key.
type VaultRegistry struct {
	// Provider is populated by a top-level [vault.provider] inline
	// table. nil when the file uses [vault.providers.<name>] instead.
	Provider *VaultProviderConfig `toml:"provider,omitempty"`

	// Providers is populated by [vault.providers.<name>] tables. nil
	// when the file uses the anonymous [vault.provider] shape.
	Providers map[string]VaultProviderConfig `toml:"providers,omitempty"`

	// TeamOnly lists keys whose personal-overlay values must be
	// suppressed when the team config declares a matching key. Rejected
	// by the resolver (Issue 4), not the parser.
	TeamOnly []string `toml:"team_only,omitempty"`
}

// VaultProviderConfig is one provider declaration. Kind selects which
// Factory (from internal/vault) opens this provider at resolve time.
// Remaining backend-specific fields are held as a generic map so the
// config layer stays decoupled from the set of backends compiled in;
// each backend validates its own fields when its Factory.Open runs.
type VaultProviderConfig struct {
	Kind string `toml:"kind"`

	// Config captures every non-Kind field declared on the same table.
	// Populated via the custom TOML decoder (UnmarshalTOML) below. The
	// parser does not inspect these values; Issue 5 (Infisical) wires
	// its own validation through the Factory.
	Config map[string]any `toml:"-"`
}

// UnmarshalTOML implements toml.Unmarshaler so the decoder can capture
// every backend-specific field alongside Kind. BurntSushi/toml passes
// the raw map[string]any into data.
func (v *VaultProviderConfig) UnmarshalTOML(data any) error {
	raw, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("vault provider must be a table, got %T", data)
	}
	// Reset to avoid surprising carry-over on reused pointers.
	v.Kind = ""
	v.Config = nil

	if k, present := raw["kind"]; present {
		ks, ok := k.(string)
		if !ok {
			return fmt.Errorf("vault provider kind must be a string, got %T", k)
		}
		v.Kind = ks
	}

	// Everything else goes into Config. Use an empty map rather than
	// nil so downstream consumers can test len() without nil guards.
	rest := make(map[string]any, len(raw))
	for k, val := range raw {
		if k == "kind" {
			continue
		}
		rest[k] = val
	}
	if len(rest) > 0 {
		v.Config = rest
	}
	return nil
}

// Validate enforces the structural rules on a VaultRegistry:
//
//   - Provider and Providers are mutually exclusive.
//   - Each provider (anonymous or named) MUST declare a non-empty kind.
//   - Each named provider name MUST be non-empty and must satisfy the
//     same name regex as workspaces, groups, and repo overrides.
//
// fileLabel is used only for error attribution (e.g., "workspace.toml"
// or "global overlay") and is never compared.
func (v *VaultRegistry) Validate(fileLabel string) error {
	if v == nil {
		return nil
	}
	if v.Provider != nil && len(v.Providers) > 0 {
		return fmt.Errorf(
			"%s declares both [vault.provider] (anonymous) and "+
				"[vault.providers.*] (named) -- pick one shape",
			fileLabel,
		)
	}
	if v.Provider != nil {
		if v.Provider.Kind == "" {
			return fmt.Errorf("%s [vault.provider] requires kind", fileLabel)
		}
	}
	for name, p := range v.Providers {
		if name == "" {
			return fmt.Errorf(
				"%s [vault.providers.*] has an empty provider name",
				fileLabel,
			)
		}
		if !validName.MatchString(name) {
			return fmt.Errorf(
				"%s [vault.providers.%s]: name must match [a-zA-Z0-9._-]+",
				fileLabel, name,
			)
		}
		if p.Kind == "" {
			return fmt.Errorf(
				"%s [vault.providers.%s] requires kind",
				fileLabel, name,
			)
		}
	}
	return nil
}

// IsEmpty reports whether the registry declares no providers. team_only
// alone does not count as "non-empty" for routing purposes: a [vault]
// block with only team_only and no providers has nothing to bootstrap,
// nothing to resolve, and nothing to re-check. Callers that need to
// detect a wholly-empty block can still inspect the individual fields.
func (v *VaultRegistry) IsEmpty() bool {
	if v == nil {
		return true
	}
	return v.Provider == nil && len(v.Providers) == 0
}

// KnownProviderNames returns the set of provider names declared in this
// registry. The anonymous [vault.provider] shape contributes the empty
// string; named providers contribute their respective keys.
func (v *VaultRegistry) KnownProviderNames() map[string]bool {
	known := make(map[string]bool)
	if v == nil {
		return known
	}
	if v.Provider != nil {
		known[""] = true
	}
	for name := range v.Providers {
		known[name] = true
	}
	return known
}
