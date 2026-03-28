package workspace

import (
	"maps"

	"github.com/tsukumogami/niwa/internal/config"
)

// EffectiveConfig represents the merged workspace-level and per-repo config
// for a single repository. It holds the final hooks, settings, and env that
// should apply after overlay semantics are resolved.
type EffectiveConfig struct {
	Hooks    map[string]any
	Settings map[string]any
	Env      map[string]any
}

// MergeOverrides produces the effective configuration for a repo by combining
// workspace-level defaults with per-repo overrides. The merge semantics are:
//
//   - Settings: repo values win (override workspace values per key)
//   - Env "files" key: repo values are appended to workspace values
//   - Env non-"files" keys: repo values win (override workspace values per key)
//   - Hooks: repo values extend workspace values (lists are concatenated)
func MergeOverrides(ws *config.WorkspaceConfig, repoName string) EffectiveConfig {
	override, hasOverride := ws.Repos[repoName]

	result := EffectiveConfig{
		Hooks:    copyMap(ws.Hooks),
		Settings: copyMap(ws.Settings),
		Env:      copyMap(ws.Env),
	}

	if !hasOverride {
		return result
	}

	// Settings: repo wins per key.
	for k, v := range override.Settings {
		if result.Settings == nil {
			result.Settings = map[string]any{}
		}
		result.Settings[k] = v
	}

	// Env: "files" appends, other keys: repo wins.
	for k, v := range override.Env {
		if result.Env == nil {
			result.Env = map[string]any{}
		}
		if k == "files" {
			result.Env[k] = appendSliceValues(result.Env[k], v)
		} else {
			result.Env[k] = v
		}
	}

	// Hooks: extend (concatenate lists per key).
	for k, v := range override.Hooks {
		if result.Hooks == nil {
			result.Hooks = map[string]any{}
		}
		result.Hooks[k] = appendSliceValues(result.Hooks[k], v)
	}

	return result
}

// ClaudeEnabled returns whether Claude content installation (CLAUDE.local.md,
// hooks, settings, env) should be performed for the given repo. When the
// repo has no override or the override doesn't set claude, it defaults to true.
func ClaudeEnabled(ws *config.WorkspaceConfig, repoName string) bool {
	override, ok := ws.Repos[repoName]
	if !ok || override.Claude == nil {
		return true
	}
	return *override.Claude
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
	for name := range ws.Repos {
		if !known[name] {
			warnings = append(warnings, "repos override "+name+" does not match any discovered repo")
		}
	}
	return warnings
}

// copyMap returns a shallow copy of a map. Returns nil if input is nil.
func copyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	maps.Copy(out, m)
	return out
}

// appendSliceValues concatenates two values that are expected to be []any
// (TOML arrays). If either value is not a slice, it's treated as a single
// element. Nil base returns the override as-is.
func appendSliceValues(base, override any) any {
	baseSlice := toSlice(base)
	overrideSlice := toSlice(override)
	if baseSlice == nil {
		return overrideSlice
	}
	return append(baseSlice, overrideSlice...)
}

func toSlice(v any) []any {
	if v == nil {
		return nil
	}
	if s, ok := v.([]any); ok {
		return s
	}
	return []any{v}
}
