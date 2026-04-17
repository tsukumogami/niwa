package workspace

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/vault"
)

// EffectiveConfig represents the merged workspace-level and per-repo config
// for a single repository. It holds the final hooks, settings, and env that
// should apply after overlay semantics are resolved.
type EffectiveConfig struct {
	Claude  config.ClaudeConfig
	Env     config.EnvConfig
	Files   map[string]string
	Plugins []string
}

// MergeOverrides produces the effective configuration for a repo by combining
// workspace-level defaults with per-repo overrides. The merge semantics are:
//
//   - Settings: repo values win (override workspace values per key)
//   - Env files: repo values are appended to workspace values
//   - Env vars: repo values win (override workspace values per key)
//   - Hooks: repo values extend workspace values (lists are concatenated)
func MergeOverrides(ws *config.WorkspaceConfig, repoName string) EffectiveConfig {
	override, hasOverride := ws.Repos[repoName]

	// Resolve workspace-level plugins (deref pointer to plain slice).
	var wsPlugins []string
	if ws.Claude.Plugins != nil {
		wsPlugins = slices.Clone(*ws.Claude.Plugins)
	}

	result := EffectiveConfig{
		Claude: config.ClaudeConfig{
			Hooks:    copyHooks(ws.Claude.Hooks),
			Settings: copySettings(ws.Claude.Settings),
			Env:      copyClaudeEnv(ws.Claude.Env),
		},
		Env:     copyEnv(ws.Env),
		Files:   copyStringMap(ws.Files),
		Plugins: wsPlugins,
	}

	if !hasOverride {
		return result
	}

	// Claude: apply repo overrides if present.
	if override.Claude != nil {
		// Settings: repo wins per key.
		for k, v := range override.Claude.Settings {
			if result.Claude.Settings == nil {
				result.Claude.Settings = config.SettingsConfig{}
			}
			result.Claude.Settings[k] = v
		}

		// Hooks: extend (concatenate lists per key).
		for k, v := range override.Claude.Hooks {
			if result.Claude.Hooks == nil {
				result.Claude.Hooks = config.HooksConfig{}
			}
			result.Claude.Hooks[k] = append(result.Claude.Hooks[k], v...)
		}

		// Claude env promote: repo extends (union).
		if len(override.Claude.Env.Promote) > 0 {
			seen := make(map[string]bool, len(result.Claude.Env.Promote))
			for _, k := range result.Claude.Env.Promote {
				seen[k] = true
			}
			for _, k := range override.Claude.Env.Promote {
				if !seen[k] {
					result.Claude.Env.Promote = append(result.Claude.Env.Promote, k)
				}
			}
		}

		// Claude env vars: repo wins per key.
		for k, v := range override.Claude.Env.Vars.Values {
			if result.Claude.Env.Vars.Values == nil {
				result.Claude.Env.Vars.Values = map[string]config.MaybeSecret{}
			}
			result.Claude.Env.Vars.Values[k] = v
		}

		// Claude env secrets: repo wins per key. Mirrors the vars
		// merge: env.vars and env.secrets are sensitivity-coded
		// siblings (PRD R33/Issue 3), so the merge must flow through
		// both branches identically.
		for k, v := range override.Claude.Env.Secrets.Values {
			if result.Claude.Env.Secrets.Values == nil {
				result.Claude.Env.Secrets.Values = map[string]config.MaybeSecret{}
			}
			result.Claude.Env.Secrets.Values[k] = v
		}

		// Plugins: repo replaces workspace entirely (nil = inherit).
		if override.Claude.Plugins != nil {
			result.Plugins = slices.Clone(*override.Claude.Plugins)
		}
	}

	// Env files: append repo files after workspace files.
	if len(override.Env.Files) > 0 {
		result.Env.Files = append(result.Env.Files, override.Env.Files...)
	}

	// Env vars: repo wins per key.
	for k, v := range override.Env.Vars.Values {
		if result.Env.Vars.Values == nil {
			result.Env.Vars.Values = map[string]config.MaybeSecret{}
		}
		result.Env.Vars.Values[k] = v
	}

	// Env secrets: repo wins per key. See Claude env secrets comment
	// above for the rationale.
	for k, v := range override.Env.Secrets.Values {
		if result.Env.Secrets.Values == nil {
			result.Env.Secrets.Values = map[string]config.MaybeSecret{}
		}
		result.Env.Secrets.Values[k] = v
	}

	// Files: repo wins per source key. Empty string removes workspace mapping.
	for k, v := range override.Files {
		if result.Files == nil {
			result.Files = map[string]string{}
		}
		if v == "" {
			delete(result.Files, k)
		} else {
			result.Files[k] = v
		}
	}

	return result
}

// MergeInstanceOverrides produces the effective configuration for the
// instance root by combining workspace-level defaults with [instance]
// overrides. Uses the same merge semantics as MergeOverrides.
func MergeInstanceOverrides(ws *config.WorkspaceConfig) EffectiveConfig {
	// Start with workspace defaults.
	var wsPlugins []string
	if ws.Claude.Plugins != nil {
		wsPlugins = slices.Clone(*ws.Claude.Plugins)
	}

	result := EffectiveConfig{
		Claude: config.ClaudeConfig{
			Hooks:        copyHooks(ws.Claude.Hooks),
			Settings:     copySettings(ws.Claude.Settings),
			Env:          copyClaudeEnv(ws.Claude.Env),
			Marketplaces: slices.Clone(ws.Claude.Marketplaces),
		},
		Env:     copyEnv(ws.Env),
		Files:   copyStringMap(ws.Files),
		Plugins: wsPlugins,
	}

	override := ws.Instance
	if override.Claude == nil && len(override.Env.Files) == 0 && override.Env.Vars.IsEmpty() && override.Env.Secrets.IsEmpty() && len(override.Files) == 0 {
		return result
	}

	if override.Claude != nil {
		for k, v := range override.Claude.Settings {
			if result.Claude.Settings == nil {
				result.Claude.Settings = config.SettingsConfig{}
			}
			result.Claude.Settings[k] = v
		}

		for k, v := range override.Claude.Hooks {
			if result.Claude.Hooks == nil {
				result.Claude.Hooks = config.HooksConfig{}
			}
			result.Claude.Hooks[k] = append(result.Claude.Hooks[k], v...)
		}

		if len(override.Claude.Env.Promote) > 0 {
			seen := make(map[string]bool, len(result.Claude.Env.Promote))
			for _, k := range result.Claude.Env.Promote {
				seen[k] = true
			}
			for _, k := range override.Claude.Env.Promote {
				if !seen[k] {
					result.Claude.Env.Promote = append(result.Claude.Env.Promote, k)
				}
			}
		}

		for k, v := range override.Claude.Env.Vars.Values {
			if result.Claude.Env.Vars.Values == nil {
				result.Claude.Env.Vars.Values = map[string]config.MaybeSecret{}
			}
			result.Claude.Env.Vars.Values[k] = v
		}

		// Instance-level claude env secrets: mirror vars.
		for k, v := range override.Claude.Env.Secrets.Values {
			if result.Claude.Env.Secrets.Values == nil {
				result.Claude.Env.Secrets.Values = map[string]config.MaybeSecret{}
			}
			result.Claude.Env.Secrets.Values[k] = v
		}

		if override.Claude.Plugins != nil {
			result.Plugins = slices.Clone(*override.Claude.Plugins)
		}
	}

	if len(override.Env.Files) > 0 {
		result.Env.Files = append(result.Env.Files, override.Env.Files...)
	}
	for k, v := range override.Env.Vars.Values {
		if result.Env.Vars.Values == nil {
			result.Env.Vars.Values = map[string]config.MaybeSecret{}
		}
		result.Env.Vars.Values[k] = v
	}

	// Instance-level env secrets: mirror vars.
	for k, v := range override.Env.Secrets.Values {
		if result.Env.Secrets.Values == nil {
			result.Env.Secrets.Values = map[string]config.MaybeSecret{}
		}
		result.Env.Secrets.Values[k] = v
	}

	for k, v := range override.Files {
		if result.Files == nil {
			result.Files = map[string]string{}
		}
		if v == "" {
			delete(result.Files, k)
		} else {
			result.Files[k] = v
		}
	}

	return result
}

// CheckVaultScopeAmbiguity enforces PRD R5: a workspace with more than
// one [[sources]] block MUST declare [workspace].vault_scope so niwa
// knows which [workspaces.<scope>] entry in the personal overlay
// applies. Single-source or zero-source workspaces do not need
// vault_scope (single-source scopes implicitly by source org; zero-
// source workspaces fall back to team-only resolution).
//
// The check has an escape: when globalOverride is nil (no personal
// overlay registered), the scope selection is moot and this function
// returns nil regardless of source count. That keeps multi-source
// team-only workspaces usable without forcing a vault_scope value
// that has nothing to select against.
func CheckVaultScopeAmbiguity(cfg *config.WorkspaceConfig, globalOverride *config.GlobalConfigOverride) error {
	if cfg == nil {
		return nil
	}
	// No personal overlay → scope selection is moot.
	if globalOverride == nil {
		return nil
	}
	// Explicit scope wins; no ambiguity possible.
	if cfg.Workspace.VaultScope != "" {
		return nil
	}
	if len(cfg.Sources) <= 1 {
		return nil
	}

	// Surface the declared source orgs so the user has an obvious
	// candidate list to pick from when writing vault_scope.
	orgs := make([]string, 0, len(cfg.Sources))
	for _, s := range cfg.Sources {
		orgs = append(orgs, s.Org)
	}
	return fmt.Errorf(
		"[workspace].vault_scope is required for multi-source workspaces "+
			"(sources: %s); set it to the name of a [workspaces.<scope>] block "+
			"in your personal overlay (or any string if you want to target the "+
			"default [global] block and skip per-scope routing)",
		strings.Join(orgs, ", "),
	)
}

// ResolveGlobalOverride merges the flat [global] section with any
// [workspaces.<workspaceName>] override and returns the result as a single
// GlobalOverride. Workspace-specific values win per field. Returns the flat
// [global] section unchanged when no matching workspace entry exists.
func ResolveGlobalOverride(g *config.GlobalConfigOverride, workspaceName string) config.GlobalOverride {
	if g == nil {
		return config.GlobalOverride{}
	}
	base := g.Global
	ws, ok := g.Workspaces[workspaceName]
	if !ok {
		return base
	}

	result := config.GlobalOverride{
		Claude: base.Claude,
		Env:    copyEnv(base.Env),
		Files:  copyStringMap(base.Files),
	}

	// Claude: workspace-specific wins per field.
	if ws.Claude != nil {
		if base.Claude == nil {
			result.Claude = ws.Claude
		} else {
			merged := *base.Claude
			if ws.Claude.Enabled != nil {
				merged.Enabled = ws.Claude.Enabled
			}
			// Settings: ws wins per key.
			if len(ws.Claude.Settings) > 0 {
				if merged.Settings == nil {
					merged.Settings = config.SettingsConfig{}
				}
				for k, v := range ws.Claude.Settings {
					merged.Settings[k] = v
				}
			}
			// Hooks: ws wins per event (replace, not append).
			if len(ws.Claude.Hooks) > 0 {
				if merged.Hooks == nil {
					merged.Hooks = config.HooksConfig{}
				}
				for k, v := range ws.Claude.Hooks {
					merged.Hooks[k] = v
				}
			}
			// Plugins: ws wins.
			if ws.Claude.Plugins != nil {
				merged.Plugins = ws.Claude.Plugins
			}
			// Claude.Env.Promote: union.
			if len(ws.Claude.Env.Promote) > 0 {
				seen := make(map[string]bool)
				for _, k := range merged.Env.Promote {
					seen[k] = true
				}
				for _, k := range ws.Claude.Env.Promote {
					if !seen[k] {
						merged.Env.Promote = append(merged.Env.Promote, k)
					}
				}
			}
			// Claude.Env.Vars: ws wins.
			for k, v := range ws.Claude.Env.Vars.Values {
				if merged.Env.Vars.Values == nil {
					merged.Env.Vars.Values = map[string]config.MaybeSecret{}
				}
				merged.Env.Vars.Values[k] = v
			}
			// Claude.Env.Secrets: ws wins. Mirrors the vars branch --
			// both tables carry MaybeSecret and must flow through
			// merges identically so the resolver's output in either
			// table survives the overlay step.
			for k, v := range ws.Claude.Env.Secrets.Values {
				if merged.Env.Secrets.Values == nil {
					merged.Env.Secrets.Values = map[string]config.MaybeSecret{}
				}
				merged.Env.Secrets.Values[k] = v
			}
			result.Claude = &merged
		}
	}

	// Env.Files: append ws files.
	if len(ws.Env.Files) > 0 {
		result.Env.Files = append(result.Env.Files, ws.Env.Files...)
	}
	// Env.Vars: ws wins per key.
	for k, v := range ws.Env.Vars.Values {
		if result.Env.Vars.Values == nil {
			result.Env.Vars.Values = map[string]config.MaybeSecret{}
		}
		result.Env.Vars.Values[k] = v
	}
	// Env.Secrets: ws wins per key. Required so per-workspace
	// personal overlays can supply secret-table values that flow
	// through to the resolver output.
	for k, v := range ws.Env.Secrets.Values {
		if result.Env.Secrets.Values == nil {
			result.Env.Secrets.Values = map[string]config.MaybeSecret{}
		}
		result.Env.Secrets.Values[k] = v
	}

	// Files: ws wins per key.
	for k, v := range ws.Files {
		if result.Files == nil {
			result.Files = map[string]string{}
		}
		result.Files[k] = v
	}

	return result
}

// MergeGlobalOverride applies a resolved GlobalOverride on top of a workspace
// config baseline and returns a new *WorkspaceConfig. The input ws is never
// mutated. The globalConfigDir is used to resolve hook script paths to absolute
// paths so the HooksMaterializer can locate them without knowing their origin.
//
// Merge semantics:
//   - Claude.Hooks: global hooks appended after workspace hooks; scripts
//     resolved to absolute paths using globalConfigDir.
//   - Claude.Settings: global value wins per key.
//   - Claude.Env.Promote: union (no entries dropped).
//   - Claude.Env.Vars: global value wins per key.
//   - Claude.Env.Secrets: global value wins per key (mirrors vars).
//   - Claude.Plugins: global plugins unioned with workspace plugins (deduplicated).
//   - Env.Files: global files appended after workspace files.
//   - Env.Vars: global value wins per key.
//   - Env.Secrets: global value wins per key (mirrors vars).
//   - Files: global value wins per key; empty global value suppresses workspace mapping.
//
// R8 enforcement (team_only): before an overlay value wins over a team
// value, MergeGlobalOverride consults cfg.Vault.TeamOnly. A personal
// overlay attempting to override a listed key returns an error wrapping
// vault.ErrTeamOnlyLocked, naming the key. This is defense in depth: the
// resolver has already rejected a personal-overlay attempt to replace
// team-declared providers (R12); team_only is the per-key version of
// that rule for env and settings leaves.
func MergeGlobalOverride(ws *config.WorkspaceConfig, g config.GlobalOverride, globalConfigDir string) (*config.WorkspaceConfig, error) {
	// Build the team_only key set up front so we can flag overlay
	// writes before spending work on the merge. The set is nil-safe
	// and empty for configs without a [vault] block.
	teamOnly := teamOnlyKeys(ws.Vault)

	// Deep-copy the workspace config so we never mutate the original.
	merged := *ws
	merged.Claude = *copyClaudeConfigFull(&ws.Claude)
	merged.Env = copyEnv(ws.Env)
	merged.Files = copyStringMap(ws.Files)

	// Claude overrides.
	if g.Claude != nil {
		// Hooks: append global hooks after workspace hooks; resolve scripts to absolute paths.
		for event, entries := range g.Claude.Hooks {
			absEntries := make([]config.HookEntry, 0, len(entries))
			for _, e := range entries {
				absScripts := make([]string, 0, len(e.Scripts))
				for _, s := range e.Scripts {
					if filepath.IsAbs(s) {
						absScripts = append(absScripts, s)
					} else {
						absScripts = append(absScripts, filepath.Join(globalConfigDir, s))
					}
				}
				absEntries = append(absEntries, config.HookEntry{
					Matcher: e.Matcher,
					Scripts: absScripts,
				})
			}
			if merged.Claude.Hooks == nil {
				merged.Claude.Hooks = config.HooksConfig{}
			}
			merged.Claude.Hooks[event] = append(merged.Claude.Hooks[event], absEntries...)
		}

		// Settings: global wins per key.
		for k, v := range g.Claude.Settings {
			if _, existed := merged.Claude.Settings[k]; existed && teamOnly[k] {
				return nil, fmt.Errorf(
					"claude.settings.%s: key is locked by [vault].team_only; "+
						"remove it from the personal overlay or drop it from team_only: %w",
					k, vault.ErrTeamOnlyLocked,
				)
			}
			if merged.Claude.Settings == nil {
				merged.Claude.Settings = config.SettingsConfig{}
			}
			merged.Claude.Settings[k] = v
		}

		// Claude.Env.Promote: union.
		if len(g.Claude.Env.Promote) > 0 {
			seen := make(map[string]bool)
			for _, k := range merged.Claude.Env.Promote {
				seen[k] = true
			}
			for _, k := range g.Claude.Env.Promote {
				if !seen[k] {
					merged.Claude.Env.Promote = append(merged.Claude.Env.Promote, k)
				}
			}
		}

		// Claude.Env.Vars: global wins per key.
		for k, v := range g.Claude.Env.Vars.Values {
			if _, existed := merged.Claude.Env.Vars.Values[k]; existed && teamOnly[k] {
				return nil, fmt.Errorf(
					"claude.env.vars.%s: key is locked by [vault].team_only; "+
						"remove it from the personal overlay or drop it from team_only: %w",
					k, vault.ErrTeamOnlyLocked,
				)
			}
			if merged.Claude.Env.Vars.Values == nil {
				merged.Claude.Env.Vars.Values = map[string]config.MaybeSecret{}
			}
			merged.Claude.Env.Vars.Values[k] = v
		}

		// Claude.Env.Secrets: global wins per key. Mirrors vars.
		for k, v := range g.Claude.Env.Secrets.Values {
			if _, existed := merged.Claude.Env.Secrets.Values[k]; existed && teamOnly[k] {
				return nil, fmt.Errorf(
					"claude.env.secrets.%s: key is locked by [vault].team_only; "+
						"remove it from the personal overlay or drop it from team_only: %w",
					k, vault.ErrTeamOnlyLocked,
				)
			}
			if merged.Claude.Env.Secrets.Values == nil {
				merged.Claude.Env.Secrets.Values = map[string]config.MaybeSecret{}
			}
			merged.Claude.Env.Secrets.Values[k] = v
		}

		// Plugins: union, deduplicated; workspace plugins are never removed.
		if g.Claude.Plugins != nil {
			existing := make(map[string]bool)
			if merged.Claude.Plugins != nil {
				for _, p := range *merged.Claude.Plugins {
					existing[p] = true
				}
			}
			var combined []string
			if merged.Claude.Plugins != nil {
				combined = slices.Clone(*merged.Claude.Plugins)
			}
			for _, p := range *g.Claude.Plugins {
				if !existing[p] {
					combined = append(combined, p)
					existing[p] = true
				}
			}
			merged.Claude.Plugins = &combined
		}
	}

	// Env.Files: append global files after workspace files.
	if len(g.Env.Files) > 0 {
		merged.Env.Files = append(merged.Env.Files, g.Env.Files...)
	}

	// Env.Vars: global wins per key.
	for k, v := range g.Env.Vars.Values {
		if _, existed := merged.Env.Vars.Values[k]; existed && teamOnly[k] {
			return nil, fmt.Errorf(
				"env.vars.%s: key is locked by [vault].team_only; "+
					"remove it from the personal overlay or drop it from team_only: %w",
				k, vault.ErrTeamOnlyLocked,
			)
		}
		if merged.Env.Vars.Values == nil {
			merged.Env.Vars.Values = map[string]config.MaybeSecret{}
		}
		merged.Env.Vars.Values[k] = v
	}

	// Env.Secrets: global wins per key. Mirrors vars; team_only also
	// applies here (the common case).
	for k, v := range g.Env.Secrets.Values {
		if _, existed := merged.Env.Secrets.Values[k]; existed && teamOnly[k] {
			return nil, fmt.Errorf(
				"env.secrets.%s: key is locked by [vault].team_only; "+
					"remove it from the personal overlay or drop it from team_only: %w",
				k, vault.ErrTeamOnlyLocked,
			)
		}
		if merged.Env.Secrets.Values == nil {
			merged.Env.Secrets.Values = map[string]config.MaybeSecret{}
		}
		merged.Env.Secrets.Values[k] = v
	}

	// Files: global wins per key; empty global value suppresses workspace mapping.
	for k, v := range g.Files {
		if _, existed := merged.Files[k]; existed && teamOnly[k] {
			return nil, fmt.Errorf(
				"files[%q]: key is locked by [vault].team_only; "+
					"remove it from the personal overlay or drop it from team_only: %w",
				k, vault.ErrTeamOnlyLocked,
			)
		}
		if merged.Files == nil {
			merged.Files = map[string]string{}
		}
		if v == "" {
			delete(merged.Files, k)
		} else {
			merged.Files[k] = v
		}
	}

	return &merged, nil
}

// teamOnlyKeys builds a set of key names that the team config has
// locked against personal-overlay override via [vault].team_only. A
// nil *VaultRegistry yields an empty set; the rest of MergeGlobalOverride
// can safely index without further nil checks.
func teamOnlyKeys(vr *config.VaultRegistry) map[string]bool {
	if vr == nil || len(vr.TeamOnly) == 0 {
		return nil
	}
	out := make(map[string]bool, len(vr.TeamOnly))
	for _, k := range vr.TeamOnly {
		out[k] = true
	}
	return out
}

// MergeWorkspaceOverlay merges an overlay config on top of a base WorkspaceConfig
// and returns a new *WorkspaceConfig. The input ws is never mutated.
//
// Merge semantics (base-wins where applicable):
//   - Sources: overlay sources appended after base sources. Error if any overlay
//     source org already exists in the base (duplicate-org check).
//   - Groups: overlay groups added; base wins on key collision.
//   - Repos: overlay repos added; base wins on key collision.
//   - Claude.Hooks: overlay hooks appended after base hooks; each script path
//     is resolved to an absolute path via filepath.Join(overlayDir, script), then
//     confirmed to remain within overlayDir using symlink-resolving containment.
//   - Claude.Settings: base wins per key.
//   - Claude.Env.Promote: union (no entries dropped).
//   - Claude.Env.Vars: base wins per key.
//   - Env.Files: overlay files appended after base files.
//   - Env.Vars: base wins per key.
//   - Files: base wins per key; overlay keys not in base are added, but only
//     after checking destination is not a protected path.
//   - Claude.Content.Repos: overlay entries with source= add new content entries
//     (base wins on key collision). Overlay entries with overlay= set OverlaySource
//     on an existing base entry (error if the base entry does not exist).
func MergeWorkspaceOverlay(ws *config.WorkspaceConfig, overlay *config.WorkspaceOverlay, overlayDir string) (*config.WorkspaceConfig, error) {
	// Deep-copy the input config.
	merged := *ws
	merged.Claude = *copyClaudeConfigFull(&ws.Claude)
	merged.Claude.Content = copyContentConfig(ws.Claude.Content)
	merged.Env = copyEnv(ws.Env)
	merged.Files = copyStringMap(ws.Files)
	merged.Sources = append([]config.SourceConfig(nil), ws.Sources...)
	merged.Groups = copyGroupMap(ws.Groups)
	merged.Repos = copyRepoOverrideMap(ws.Repos)

	// Build a set of orgs already in the base config for the duplicate-org check.
	baseOrgs := make(map[string]bool, len(ws.Sources))
	for _, src := range ws.Sources {
		baseOrgs[src.Org] = true
	}

	// Sources: append overlay sources; error on duplicate org.
	for _, src := range overlay.Sources {
		if baseOrgs[src.Org] {
			return nil, fmt.Errorf("overlay source org %q already exists in base workspace config", src.Org)
		}
		merged.Sources = append(merged.Sources, config.SourceConfig{
			Org:   src.Org,
			Repos: append([]string(nil), src.Repos...),
		})
		baseOrgs[src.Org] = true
	}

	// Groups: add overlay groups; base wins on collision.
	for k, v := range overlay.Groups {
		if _, exists := merged.Groups[k]; !exists {
			if merged.Groups == nil {
				merged.Groups = make(map[string]config.GroupConfig)
			}
			g := v
			g.Repos = append([]string(nil), v.Repos...)
			merged.Groups[k] = g
		}
	}

	// Repos: add overlay repos; base wins on collision.
	for k, v := range overlay.Repos {
		if _, exists := merged.Repos[k]; !exists {
			if merged.Repos == nil {
				merged.Repos = make(map[string]config.RepoOverride)
			}
			merged.Repos[k] = v
		}
	}

	// Claude.Hooks: append overlay hooks; resolve scripts to absolute paths within overlayDir.
	for event, entries := range overlay.Claude.Hooks {
		absEntries := make([]config.HookEntry, 0, len(entries))
		for _, e := range entries {
			absScripts := make([]string, 0, len(e.Scripts))
			for _, s := range e.Scripts {
				absScript := filepath.Join(overlayDir, s)
				if err := checkContainment(absScript, overlayDir); err != nil {
					return nil, fmt.Errorf("overlay hook script %q escapes overlay directory: %w", s, err)
				}
				absScripts = append(absScripts, absScript)
			}
			absEntries = append(absEntries, config.HookEntry{
				Matcher: e.Matcher,
				Scripts: absScripts,
			})
		}
		if merged.Claude.Hooks == nil {
			merged.Claude.Hooks = config.HooksConfig{}
		}
		merged.Claude.Hooks[event] = append(merged.Claude.Hooks[event], absEntries...)
	}

	// Claude.Settings: base wins per key (only add keys not in base).
	for k, v := range overlay.Claude.Settings {
		if _, exists := merged.Claude.Settings[k]; !exists {
			if merged.Claude.Settings == nil {
				merged.Claude.Settings = config.SettingsConfig{}
			}
			merged.Claude.Settings[k] = v
		}
	}

	// Claude.Env.Promote and Claude.Env.Vars: OverlayClaudeConfig does not carry
	// a Claude env section (claude env is workspace-scoped and flows through the
	// top-level Env pipeline). Nothing to merge here.

	// Env.Files: append overlay files after base files.
	if len(overlay.Env.Files) > 0 {
		merged.Env.Files = append(merged.Env.Files, overlay.Env.Files...)
	}

	// Env.Vars: base wins per key.
	for k, v := range overlay.Env.Vars.Values {
		if _, exists := merged.Env.Vars.Values[k]; !exists {
			if merged.Env.Vars.Values == nil {
				merged.Env.Vars.Values = map[string]config.MaybeSecret{}
			}
			merged.Env.Vars.Values[k] = v
		}
	}

	// Files: base wins per key; add overlay keys not already in base.
	for k, v := range overlay.Files {
		if _, exists := merged.Files[k]; !exists {
			if merged.Files == nil {
				merged.Files = map[string]string{}
			}
			merged.Files[k] = v
		}
	}

	// Claude.Content.Repos: process overlay content entries.
	for repoName, entry := range overlay.Claude.Content.Repos {
		if entry.Source != "" {
			if _, exists := merged.Claude.Content.Repos[repoName]; exists {
				// R13: source= on a repo already defined in the base config is an error.
				// Use overlay= to append content to a base-config repo's CLAUDE.local.md.
				return nil, fmt.Errorf("overlay content entry for repo %q uses source= but %q is already defined in the base config; use overlay= to append content instead", repoName, repoName)
			}
			// Overlay-only repo: add a new content entry.
			if merged.Claude.Content.Repos == nil {
				merged.Claude.Content.Repos = make(map[string]config.RepoContentEntry)
			}
			merged.Claude.Content.Repos[repoName] = config.RepoContentEntry{
				Source:        entry.Source,
				OverlaySource: "",
			}
		} else if entry.Overlay != "" {
			// Overlay appends to an existing base entry via OverlaySource.
			base, exists := merged.Claude.Content.Repos[repoName]
			if !exists {
				return nil, fmt.Errorf("overlay content entry for repo %q uses overlay= but the repo has no entry in the base config", repoName)
			}
			base.OverlaySource = entry.Overlay
			merged.Claude.Content.Repos[repoName] = base
		}
	}

	return &merged, nil
}

// copyContentConfig returns a deep copy of a ContentConfig, including the
// Repos map (with OverlaySource preserved per entry).
func copyContentConfig(c config.ContentConfig) config.ContentConfig {
	out := config.ContentConfig{
		Workspace: c.Workspace,
		Groups:    nil,
		Repos:     nil,
	}
	if c.Groups != nil {
		out.Groups = make(map[string]config.ContentEntry, len(c.Groups))
		for k, v := range c.Groups {
			out.Groups[k] = v
		}
	}
	if c.Repos != nil {
		out.Repos = make(map[string]config.RepoContentEntry, len(c.Repos))
		for k, v := range c.Repos {
			entry := v
			if v.Subdirs != nil {
				entry.Subdirs = make(map[string]string, len(v.Subdirs))
				for sk, sv := range v.Subdirs {
					entry.Subdirs[sk] = sv
				}
			}
			out.Repos[k] = entry
		}
	}
	return out
}

// copyGroupMap returns a deep copy of a groups map.
func copyGroupMap(m map[string]config.GroupConfig) map[string]config.GroupConfig {
	if m == nil {
		return nil
	}
	out := make(map[string]config.GroupConfig, len(m))
	for k, v := range m {
		g := v
		g.Repos = append([]string(nil), v.Repos...)
		out[k] = g
	}
	return out
}

// copyRepoOverrideMap returns a deep copy of a repos map. RepoOverride
// contains pointer and reference fields (Claude *ClaudeOverride, Env.Files,
// Env.Vars, Files, SetupDir) that must be deep-copied so downstream mutations
// of the merged result's repo overrides do not corrupt the original config.
func copyRepoOverrideMap(m map[string]config.RepoOverride) map[string]config.RepoOverride {
	if m == nil {
		return nil
	}
	out := make(map[string]config.RepoOverride, len(m))
	for k, v := range m {
		out[k] = deepCopyRepoOverride(v)
	}
	return out
}

// deepCopyRepoOverride returns a deep copy of a RepoOverride, including all
// pointer and reference fields.
func deepCopyRepoOverride(v config.RepoOverride) config.RepoOverride {
	c := v
	// Deep-copy Claude *ClaudeOverride.
	if v.Claude != nil {
		claude := *v.Claude
		claude.Hooks = copyHooks(v.Claude.Hooks)
		claude.Settings = copySettings(v.Claude.Settings)
		claude.Env = copyClaudeEnv(v.Claude.Env)
		if v.Claude.Plugins != nil {
			p := slices.Clone(*v.Claude.Plugins)
			claude.Plugins = &p
		}
		c.Claude = &claude
	}
	// Deep-copy Env (contains Files slice and Vars map).
	c.Env = copyEnv(v.Env)
	// Deep-copy Files map.
	c.Files = copyStringMap(v.Files)
	// Deep-copy SetupDir *string.
	if v.SetupDir != nil {
		s := *v.SetupDir
		c.SetupDir = &s
	}
	return c
}

// copyClaudeConfigFull returns a deep copy of a ClaudeConfig pointer, including
// all nested fields. Returns a pointer to a zero-value ClaudeConfig if nil.
func copyClaudeConfigFull(c *config.ClaudeConfig) *config.ClaudeConfig {
	if c == nil {
		return &config.ClaudeConfig{}
	}
	out := *c
	out.Hooks = copyHooks(c.Hooks)
	out.Settings = copySettings(c.Settings)
	out.Env = copyClaudeEnv(c.Env)
	if c.Plugins != nil {
		p := slices.Clone(*c.Plugins)
		out.Plugins = &p
	}
	return &out
}

// ClaudeEnabled returns whether Claude content installation (CLAUDE.local.md,
// hooks, settings, env) should be performed for the given repo. When the
// repo has no override or the override doesn't set claude.enabled, it
// defaults to true.
func ClaudeEnabled(ws *config.WorkspaceConfig, repoName string) bool {
	override, ok := ws.Repos[repoName]
	if !ok || override.Claude == nil || override.Claude.Enabled == nil {
		return true
	}
	return *override.Claude.Enabled
}

// RepoCloneURL returns the clone URL for a repo, preferring the per-repo
// override URL if set, then SSH URL, then HTTPS clone URL.
func RepoCloneURL(ws *config.WorkspaceConfig, repoName, sshURL, cloneURL string) string {
	if override, ok := ws.Repos[repoName]; ok && override.URL != "" {
		return override.URL
	}
	if sshURL != "" {
		return sshURL
	}
	return cloneURL
}

// DefaultBranch resolves the effective default branch for a repo.
// It checks: per-repo branch override -> workspace default_branch -> "main".
// Unlike RepoCloneBranch, this never returns an empty string.
func DefaultBranch(ws *config.WorkspaceConfig, repoName string) string {
	if override, ok := ws.Repos[repoName]; ok && override.Branch != "" {
		return override.Branch
	}
	if ws.Workspace.DefaultBranch != "" {
		return ws.Workspace.DefaultBranch
	}
	return "main"
}

// RepoCloneBranch returns the branch override for a repo, or empty string
// if no override is set (meaning use the default branch).
func RepoCloneBranch(ws *config.WorkspaceConfig, repoName string) string {
	if override, ok := ws.Repos[repoName]; ok {
		return override.Branch
	}
	return ""
}

// KnownRepoNames returns the set of repo names that appear in sources
// (explicit lists) or groups (explicit repos lists). This is used to warn
// about unknown repo names in [repos] overrides.
func KnownRepoNames(ws *config.WorkspaceConfig, discovered []string) map[string]bool {
	known := make(map[string]bool, len(discovered))
	for _, name := range discovered {
		known[name] = true
	}
	return known
}

// WarnUnknownRepos checks cfg.Repos keys against the set of known repo names
// and returns warnings for any that don't match a discovered repo.
func WarnUnknownRepos(ws *config.WorkspaceConfig, known map[string]bool) []string {
	var warnings []string
	for name, override := range ws.Repos {
		// Skip explicit repos (url set) -- they're intentionally outside discovery.
		if override.URL != "" {
			continue
		}
		if !known[name] {
			warnings = append(warnings, "repos override "+name+" does not match any discovered repo")
		}
	}
	return warnings
}

// copyHooks returns a deep copy of a HooksConfig. Each hook event's entry
// list is independently copied so mutations don't affect the original.
func copyHooks(h config.HooksConfig) config.HooksConfig {
	if h == nil {
		return nil
	}
	out := make(config.HooksConfig, len(h))
	for k, v := range h {
		entries := make([]config.HookEntry, len(v))
		for i, e := range v {
			entries[i] = config.HookEntry{
				Matcher: e.Matcher,
				Scripts: slices.Clone(e.Scripts),
			}
		}
		out[k] = entries
	}
	return out
}

// copySettings returns a shallow copy of a SettingsConfig. SettingsConfig
// values are MaybeSecret which is already a value-type copy per entry, so
// a simple maps.Copy is sufficient.
func copySettings(s config.SettingsConfig) config.SettingsConfig {
	if s == nil {
		return nil
	}
	out := make(config.SettingsConfig, len(s))
	maps.Copy(out, s)
	return out
}

// copyStringMap returns a shallow copy of a string map.
func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}

// copyClaudeEnv returns a deep copy of a ClaudeEnvConfig.
func copyClaudeEnv(e config.ClaudeEnvConfig) config.ClaudeEnvConfig {
	return config.ClaudeEnvConfig{
		Promote: slices.Clone(e.Promote),
		Vars:    copyEnvVarsTable(e.Vars),
		Secrets: copyEnvVarsTable(e.Secrets),
	}
}

// copyEnv returns a deep copy of an EnvConfig.
func copyEnv(e config.EnvConfig) config.EnvConfig {
	return config.EnvConfig{
		Files:   slices.Clone(e.Files),
		Vars:    copyEnvVarsTable(e.Vars),
		Secrets: copyEnvVarsTable(e.Secrets),
	}
}

// copyEnvVarsTable returns a deep copy of an EnvVarsTable, cloning
// each of the four underlying maps so the result can be mutated
// without aliasing the source.
func copyEnvVarsTable(t config.EnvVarsTable) config.EnvVarsTable {
	out := config.EnvVarsTable{}
	if t.Values != nil {
		out.Values = make(map[string]config.MaybeSecret, len(t.Values))
		maps.Copy(out.Values, t.Values)
	}
	if t.Required != nil {
		out.Required = make(map[string]string, len(t.Required))
		maps.Copy(out.Required, t.Required)
	}
	if t.Recommended != nil {
		out.Recommended = make(map[string]string, len(t.Recommended))
		maps.Copy(out.Recommended, t.Recommended)
	}
	if t.Optional != nil {
		out.Optional = make(map[string]string, len(t.Optional))
		maps.Copy(out.Optional, t.Optional)
	}
	return out
}
