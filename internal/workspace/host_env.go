package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"

	"github.com/tsukumogami/niwa/internal/config"
)

// This file is the workspace half of the host-local env layer (see
// config.HostEnvLayer): applying it to a materialized env map, and naming, for
// apply's output, the layer each host key won over.
//
// The layer is applied at materialization, not merged into the config, so it
// is the last word over every source a key can have: workspace and per-repo
// tables, the personal overlay, env files and a discovered repo env file. A nil
// layer leaves every output exactly as it was.

// hostEnvLayer returns the host-local env layer the effective config carries,
// or nil.
func hostEnvLayer(ctx *MaterializeContext) *config.HostEnvLayer {
	if ctx == nil || ctx.Config == nil {
		return nil
	}
	return ctx.Config.HostEnv
}

// applyHostEnvTable clears and then sets the table's keys in dst, and drops
// each touched key from unresolved (which may be nil): a cleared key is meant
// to be absent, and a set key now has a value, so neither gets a record. It
// returns a source entry per set key, so a changed host value changes the
// written file's fingerprint; the entry carries a hash, never the value.
func applyHostEnvTable(dst map[string]string, unresolved map[string]unresolvedEnvKey, label string, t config.HostEnvTable) []SourceEntry {
	for _, k := range t.Unset {
		delete(dst, k)
		delete(unresolved, k)
	}
	keys := make([]string, 0, len(t.Vars))
	for k := range t.Vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sources := make([]SourceEntry, 0, len(keys))
	for _, k := range keys {
		v := t.Vars[k]
		dst[k] = v
		delete(unresolved, k)
		sum := sha256.Sum256([]byte(v))
		sources = append(sources, SourceEntry{
			Kind:         SourceKindPlaintext,
			SourceID:     "config.toml:" + label + ".vars." + k,
			VersionToken: hex.EncodeToString(sum[:]),
		})
	}
	return sources
}

// Layer names a HostEnvOverride reports as the one the host key won over.
const (
	hostOverLayerPersonal  = "the personal overlay"
	hostOverLayerWorkspace = "the workspace config"
)

// HostEnvOverride is one key of the host-local env layer, as apply reports
// it. Like Shadow it never carries a value: only the table, the key, and the
// layer that would have supplied the key without the host.
type HostEnvOverride struct {
	// Table is "env.vars", "env unset", "claude.env.vars" or
	// "claude.env unset".
	Table string
	Key   string
	// Over is the layer whose value the host key replaced or cleared, or ""
	// when no config layer declares the key.
	Over string
}

// Line renders the override as an apply report line.
func (o HostEnvOverride) Line() string {
	if o.Over == "" {
		return fmt.Sprintf("host-local %s %q: no other config layer sets this key", o.Table, o.Key)
	}
	return fmt.Sprintf("host-local %s %q wins over %s", o.Table, o.Key, o.Over)
}

// HostEnvOverrides lists each key of the host layer with the layer it won
// over, in a fixed order: [env] before [claude.env], unsets before vars, keys
// sorted. cfg is the workspace config before the personal overlay merge (team
// plus workspace overlay); personal is the flattened personal overlay, or nil
// when none applies.
//
// The attribution reads declarations, not resolved values, and follows the
// order a value is chosen in: for [env] the personal overlay over the
// workspace config; for [claude.env] an inline var over a promoted one, each
// with the personal overlay first. A key only an env file supplies reports no
// layer, since no config table names it.
func HostEnvOverrides(cfg *config.WorkspaceConfig, personal *config.GlobalOverride, host *config.HostEnvLayer) []HostEnvOverride {
	if host == nil || cfg == nil {
		return nil
	}
	var out []HostEnvOverride
	add := func(table string, keys []string, over func(string) string) {
		keys = slices.Clone(keys)
		sort.Strings(keys)
		for _, k := range slices.Compact(keys) {
			out = append(out, HostEnvOverride{Table: table, Key: k, Over: over(k)})
		}
	}
	envOver := func(k string) string {
		switch {
		case personal != nil && envTablesDeclare(personal.Env, k):
			return hostOverLayerPersonal
		case workspaceEnvDeclares(cfg, k):
			return hostOverLayerWorkspace
		}
		return ""
	}
	claudeOver := func(k string) string {
		switch {
		case personal != nil && personal.Claude != nil && hasValue(personal.Claude.Env.Vars, k):
			return hostOverLayerPersonal
		case workspaceClaudeVarDeclares(cfg, k):
			return hostOverLayerWorkspace
		case (personal != nil && personal.Claude != nil && slices.Contains(personal.Claude.Env.Promote, k)) || workspacePromotes(cfg, k):
			return envOver(k)
		}
		return ""
	}
	add("env unset", host.Env.Unset, envOver)
	add("env.vars", mapKeys(host.Env.Vars), envOver)
	add("claude.env unset", host.Claude.Unset, claudeOver)
	add("claude.env.vars", mapKeys(host.Claude.Vars), claudeOver)
	return out
}

func hasValue(t config.EnvVarsTable, k string) bool {
	_, ok := t.Values[k]
	return ok
}

func envTablesDeclare(e config.EnvConfig, k string) bool {
	return hasValue(e.Vars, k) || hasValue(e.Secrets, k)
}

// workspaceEnvDeclares reports whether any [env] table of the workspace config
// (top level, per repo, or instance) gives k a value.
func workspaceEnvDeclares(cfg *config.WorkspaceConfig, k string) bool {
	if envTablesDeclare(cfg.Env, k) || envTablesDeclare(cfg.Instance.Env, k) {
		return true
	}
	for _, r := range cfg.Repos {
		if envTablesDeclare(r.Env, k) {
			return true
		}
	}
	return false
}

// workspaceClaudeVarDeclares reports whether an inline session table of the
// workspace config ([claude.env.vars] at any level, or [session.env.vars])
// gives k a value.
func workspaceClaudeVarDeclares(cfg *config.WorkspaceConfig, k string) bool {
	if hasValue(cfg.Claude.Env.Vars, k) || hasValue(cfg.Session.Env.Vars, k) {
		return true
	}
	if cfg.Instance.Claude != nil && hasValue(cfg.Instance.Claude.Env.Vars, k) {
		return true
	}
	for _, r := range cfg.Repos {
		if r.Claude != nil && hasValue(r.Claude.Env.Vars, k) {
			return true
		}
	}
	return false
}

// workspacePromotes reports whether any promote list of the workspace config
// names k.
func workspacePromotes(cfg *config.WorkspaceConfig, k string) bool {
	if slices.Contains(cfg.Claude.Env.Promote, k) || slices.Contains(cfg.Session.Env.Promote, k) {
		return true
	}
	if cfg.Instance.Claude != nil && slices.Contains(cfg.Instance.Claude.Env.Promote, k) {
		return true
	}
	for _, r := range cfg.Repos {
		if r.Claude != nil && slices.Contains(r.Claude.Env.Promote, k) {
			return true
		}
	}
	return false
}

func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// hostEnv returns the host-local env layer of the machine config: the
// caller's MachineConfig, or the one loaded now. A machine config that can't
// be loaded gives no layer, but unlike the authority warnings' loader it says
// so, because a host that relies on clearing a key would otherwise get the
// overlay's value with nothing to show why. A layer that declares something it
// can't apply fails the apply.
func (a *Applier) hostEnv() (*config.HostEnvLayer, error) {
	g := a.MachineConfig
	if g == nil {
		loaded, err := config.LoadGlobalConfig()
		if err != nil {
			a.Reporter.Warn("host-local env layer not applied: %v", err)
			return nil, nil
		}
		g = loaded
	}
	layer := g.HostEnv()
	if err := layer.Validate(); err != nil {
		return nil, fmt.Errorf("machine config host-local env: %w", err)
	}
	return layer, nil
}
