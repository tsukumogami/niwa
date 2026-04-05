package workspace

import (
	"maps"
	"path/filepath"
	"slices"

	"github.com/tsukumogami/niwa/internal/config"
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
		for k, v := range override.Claude.Env.Vars {
			if result.Claude.Env.Vars == nil {
				result.Claude.Env.Vars = map[string]string{}
			}
			result.Claude.Env.Vars[k] = v
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
	for k, v := range override.Env.Vars {
		if result.Env.Vars == nil {
			result.Env.Vars = map[string]string{}
		}
		result.Env.Vars[k] = v
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
	if override.Claude == nil && len(override.Env.Files) == 0 && len(override.Env.Vars) == 0 && len(override.Files) == 0 {
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

		for k, v := range override.Claude.Env.Vars {
			if result.Claude.Env.Vars == nil {
				result.Claude.Env.Vars = map[string]string{}
			}
			result.Claude.Env.Vars[k] = v
		}

		if override.Claude.Plugins != nil {
			result.Plugins = slices.Clone(*override.Claude.Plugins)
		}
	}

	if len(override.Env.Files) > 0 {
		result.Env.Files = append(result.Env.Files, override.Env.Files...)
	}
	for k, v := range override.Env.Vars {
		if result.Env.Vars == nil {
			result.Env.Vars = map[string]string{}
		}
		result.Env.Vars[k] = v
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
			for k, v := range ws.Claude.Env.Vars {
				if merged.Env.Vars == nil {
					merged.Env.Vars = map[string]string{}
				}
				merged.Env.Vars[k] = v
			}
			result.Claude = &merged
		}
	}

	// Env.Files: append ws files.
	if len(ws.Env.Files) > 0 {
		result.Env.Files = append(result.Env.Files, ws.Env.Files...)
	}
	// Env.Vars: ws wins per key.
	for k, v := range ws.Env.Vars {
		if result.Env.Vars == nil {
			result.Env.Vars = map[string]string{}
		}
		result.Env.Vars[k] = v
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
//   - Claude.Plugins: global plugins unioned with workspace plugins (deduplicated).
//   - Env.Files: global files appended after workspace files.
//   - Env.Vars: global value wins per key.
//   - Files: global value wins per key; empty global value suppresses workspace mapping.
func MergeGlobalOverride(ws *config.WorkspaceConfig, g config.GlobalOverride, globalConfigDir string) *config.WorkspaceConfig {
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
		for k, v := range g.Claude.Env.Vars {
			if merged.Claude.Env.Vars == nil {
				merged.Claude.Env.Vars = map[string]string{}
			}
			merged.Claude.Env.Vars[k] = v
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
	for k, v := range g.Env.Vars {
		if merged.Env.Vars == nil {
			merged.Env.Vars = map[string]string{}
		}
		merged.Env.Vars[k] = v
	}

	// Files: global wins per key; empty global value suppresses workspace mapping.
	for k, v := range g.Files {
		if merged.Files == nil {
			merged.Files = map[string]string{}
		}
		if v == "" {
			delete(merged.Files, k)
		} else {
			merged.Files[k] = v
		}
	}

	return &merged
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

// copySettings returns a shallow copy of a SettingsConfig.
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
	out := config.ClaudeEnvConfig{
		Promote: slices.Clone(e.Promote),
	}
	if e.Vars != nil {
		out.Vars = make(map[string]string, len(e.Vars))
		maps.Copy(out.Vars, e.Vars)
	}
	return out
}

// copyEnv returns a deep copy of an EnvConfig.
func copyEnv(e config.EnvConfig) config.EnvConfig {
	out := config.EnvConfig{
		Files: slices.Clone(e.Files),
	}
	if e.Vars != nil {
		out.Vars = make(map[string]string, len(e.Vars))
		maps.Copy(out.Vars, e.Vars)
	}
	return out
}
