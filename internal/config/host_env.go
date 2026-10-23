package config

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// HostEnvTable is one table of the host-local env layer in
// ~/.config/niwa/config.toml: literal values to set and keys to clear.
//
// Values are plain strings, never MaybeSecret. The layer is applied after
// vault resolution has run, so a vault:// reference here would land in the
// output as the literal reference text; Validate refuses one instead.
//
// Unset exists because an empty value is not a removal: `KEY = ""` writes an
// empty assignment, which a consumer reads as "set to empty". Unset drops the
// key from the output entirely.
type HostEnvTable struct {
	Vars  map[string]string `toml:"vars,omitempty"`
	Unset []string          `toml:"unset,omitempty"`
}

// HostClaudeTable carries the host-local [claude.env] table.
type HostClaudeTable struct {
	Env HostEnvTable `toml:"env,omitempty"`
}

// IsEmpty reports whether the table sets or clears nothing.
func (t HostEnvTable) IsEmpty() bool {
	return len(t.Vars) == 0 && len(t.Unset) == 0
}

// Clears reports whether key is in the table's unset list.
func (t HostEnvTable) Clears(key string) bool {
	return slices.Contains(t.Unset, key)
}

// HostEnvLayer is the host-local env layer: the last word one machine has on
// the env that reaches repo env files ([env]) and Claude sessions
// ([claude.env]), applied after the team config, the workspace overlay and the
// personal overlay, and after per-repo tables and discovered env files too.
//
// It is the per-key counterpart to `niwa init --skip-global`, which drops the
// whole personal overlay for an instance: a host that needs one key different
// sets or clears just that key, and the rest of the overlay still applies.
type HostEnvLayer struct {
	// Env applies to the resolved [env] pipeline: what each repo's env file
	// receives, and what a [claude.env] or [session.env] promote reads.
	Env HostEnvTable
	// Claude applies to the Claude session env written into each
	// settings.local.json, after promotion and the inline tables.
	Claude HostEnvTable
}

// HostEnv returns the host-local env layer this machine config declares, or
// nil when it declares none. It does not validate; see HostEnvLayer.Validate.
func (g *GlobalConfig) HostEnv() *HostEnvLayer {
	if g == nil || (g.Env.IsEmpty() && g.Claude.Env.IsEmpty()) {
		return nil
	}
	return &HostEnvLayer{Env: g.Env, Claude: g.Claude.Env}
}

// Validate checks the layer for values it cannot apply faithfully: an empty
// key, a key both set and cleared in the same table, and a vault:// value,
// which would reach the output unresolved. A nil layer is valid.
func (h *HostEnvLayer) Validate() error {
	if h == nil {
		return nil
	}
	if err := validateHostEnvTable("env", h.Env); err != nil {
		return err
	}
	return validateHostEnvTable("claude.env", h.Claude)
}

func validateHostEnvTable(table string, t HostEnvTable) error {
	keys := make([]string, 0, len(t.Vars))
	for k := range t.Vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("[%s.vars]: empty key", table)
		}
		if strings.HasPrefix(t.Vars[k], "vault://") {
			return fmt.Errorf("[%s.vars] %s: host-local values are literal; a vault:// reference is not resolved here", table, k)
		}
		if t.Clears(k) {
			return fmt.Errorf("[%s] %s: key is both set in vars and listed in unset", table, k)
		}
	}
	for _, k := range t.Unset {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("[%s] unset: empty key", table)
		}
	}
	return nil
}
