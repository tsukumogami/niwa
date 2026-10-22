package config

import (
	"errors"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/tsukumogami/niwa/internal/authority"
)

// AuthorityConfig is the base workspace config's [authority] table: the
// workspace level, its grant ceiling and its roles.
//
// Every value is decoded untyped so a wrong type (ceiling = "merge-pr",
// level = 3) reaches authority.ValidateConfig as an invalid value instead of
// failing the whole decode. Validation is kept out of Parse on purpose: Parse
// failing would stop niwa apply too, and apply is how a workspace pulls the
// fix for a bad value. Dispatch and the authority commands refuse on the
// validation error; apply and every other loader only warn.
type AuthorityConfig struct {
	Level   any `toml:"level"`
	Ceiling any `toml:"ceiling"`
	// Roles is [authority.roles]: each role a table with an authorities list.
	Roles any `toml:"roles"`
	// UnknownKeys are the keys under [authority] Parse didn't recognize,
	// relative to the table. They are invalid values, not the generic
	// "unknown config field" warning, because a misspelt narrowing key would
	// otherwise leave policy silently wider.
	UnknownKeys []string `toml:"-"`
}

// RawLayer returns the base layer for authority.ValidateConfig.
func (a AuthorityConfig) RawLayer() authority.RawLayer {
	return authority.RawLayer{Level: a.Level, Ceiling: a.Ceiling, Roles: a.Roles, UnknownKeys: a.UnknownKeys}
}

// OverlayAuthorityConfig is an overlay's [authority] table. An overlay can
// only narrow: its level and ceiling are resolved beside the base config's,
// never merged into it, so the record can name the layer that narrowed.
type OverlayAuthorityConfig struct {
	Level   any `toml:"level"`
	Ceiling any `toml:"ceiling"`
	// Roles is a tombstone. An overlay can't declare or change a role; the
	// table is decoded only so every command that loads the overlay warns it
	// has no effect.
	Roles       any      `toml:"roles"`
	UnknownKeys []string `toml:"-"`
}

// AuthorityRawLayer returns the overlay layer for authority.ValidateConfig.
// A nil overlay is an empty layer. Roles are left out: they never take
// effect from an overlay.
func (o *WorkspaceOverlay) AuthorityRawLayer() authority.RawLayer {
	if o == nil {
		return authority.RawLayer{}
	}
	return authority.RawLayer{Level: o.Authority.Level, Ceiling: o.Authority.Ceiling, UnknownKeys: o.Authority.UnknownKeys}
}

// AuthorityRawLayer returns the machine layer ([global] authority_level and
// authority_ceiling) for authority.ValidateConfig. A nil config is empty.
func (g *GlobalConfig) AuthorityRawLayer() authority.RawLayer {
	if g == nil {
		return authority.RawLayer{}
	}
	return authority.RawLayer{Level: g.Global.AuthorityLevel, Ceiling: g.Global.AuthorityCeiling, UnknownKeys: g.authorityUnknownKeys}
}

// AuthorityLevel returns the validated machine-wide authority level cap, or
// nil when the key is absent or blank. An invalid value is an error, so
// callers can fail closed, mirroring DispatchPermissionMode.
func (g *GlobalConfig) AuthorityLevel() (*authority.Level, error) {
	if g == nil {
		return nil, nil
	}
	p, _, err := authority.ValidateConfig(authority.RawLayer{}, authority.RawLayer{}, authority.RawLayer{Level: g.Global.AuthorityLevel})
	if err != nil {
		return nil, err
	}
	return p.MachineLevel, nil
}

// AuthorityCeiling returns the validated machine-wide grant ceiling, nil when
// the key is absent and a non-nil (possibly empty) list when it is set. An
// invalid value is an error. A name this niwa doesn't know is dropped, as in
// every ceiling.
func (g *GlobalConfig) AuthorityCeiling() (*[]authority.Authority, error) {
	if g == nil {
		return nil, nil
	}
	p, _, err := authority.ValidateConfig(authority.RawLayer{}, authority.RawLayer{}, authority.RawLayer{Ceiling: g.Global.AuthorityCeiling})
	if err != nil {
		return nil, err
	}
	return p.MachineCeiling, nil
}

// overlayRolesTombstone is the warning for an overlay [authority.roles] table.
const overlayRolesTombstone = "workspace overlay sets [authority.roles], which has no effect: " +
	"roles are declared in the base workspace config only, and an overlay can only narrow the level and ceiling"

// AuthorityPolicy builds the authority policy from the three layers: the base
// workspace config, the overlay (nil when there is none) and the machine
// config (nil when there is none). It returns the policy, the warnings to
// print (names this niwa doesn't know, dropped; an overlay or machine ceiling
// naming an authority the base ceiling lacks; an overlay roles table), and an
// *authority.ConfigError when any value is invalid.
func AuthorityPolicy(cfg *WorkspaceConfig, overlay *WorkspaceOverlay, global *GlobalConfig) (authority.Policy, []string, error) {
	var base authority.RawLayer
	if cfg != nil {
		base = cfg.Authority.RawLayer()
	}
	p, warnings, err := authority.ValidateConfig(base, overlay.AuthorityRawLayer(), global.AuthorityRawLayer())
	warnings = append(warnings, authority.CeilingWarnings(p)...)
	if overlay != nil && overlay.Authority.Roles != nil {
		warnings = append(warnings, overlayRolesTombstone)
	}
	return p, warnings, err
}

// AuthorityWarnings returns every authority problem in the three layers as
// warning lines, for loaders that warn and carry on (niwa apply): each
// invalid value, then the warnings AuthorityPolicy returns. The overlay roles
// tombstone is reported here only, never by TombstoneWarnings.
func AuthorityWarnings(cfg *WorkspaceConfig, overlay *WorkspaceOverlay, global *GlobalConfig) []string {
	_, warnings, err := AuthorityPolicy(cfg, overlay, global)
	var out []string
	var ce *authority.ConfigError
	if errors.As(err, &ce) {
		for _, p := range ce.Problems {
			out = append(out, p.Error())
		}
	} else if err != nil {
		out = append(out, err.Error())
	}
	return append(out, warnings...)
}

// splitAuthorityKeys separates the undecoded keys under table (a one-element
// prefix such as "authority") from the rest. Keys under table.roles are
// dropped from both: a role table is decoded untyped, so its inner keys always
// look undecoded, and authority.ValidateConfig checks them itself.
func splitAuthorityKeys(keys []toml.Key, table string) (unknown []string, rest []toml.Key) {
	seen := map[string]bool{}
	for _, k := range keys {
		if len(k) == 0 || k[0] != table {
			rest = append(rest, k)
			continue
		}
		if len(k) < 2 || k[1] == "roles" {
			continue
		}
		name := strings.Join(k[1:], ".")
		if !seen[name] {
			seen[name] = true
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown, rest
}

// globalAuthorityKeys returns the undecoded authority_-prefixed keys under
// [global].
func globalAuthorityKeys(keys []toml.Key) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range keys {
		if len(k) >= 2 && k[0] == "global" && strings.HasPrefix(k[1], "authority_") && !seen[k[1]] {
			seen[k[1]] = true
			out = append(out, k[1])
		}
	}
	sort.Strings(out)
	return out
}
