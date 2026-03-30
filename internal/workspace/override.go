package workspace

import (
	"maps"
	"slices"

	"github.com/tsukumogami/niwa/internal/config"
)

// EffectiveConfig represents the merged workspace-level and per-repo config
// for a single repository. It holds the final hooks, settings, and env that
// should apply after overlay semantics are resolved.
type EffectiveConfig struct {
	Claude config.ClaudeConfig
	Env    config.EnvConfig
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

	result := EffectiveConfig{
		Claude: config.ClaudeConfig{
			Hooks:    copyHooks(ws.Claude.Hooks),
			Settings: copySettings(ws.Claude.Settings),
			Env:      copyClaudeEnv(ws.Claude.Env),
		},
		Env: copyEnv(ws.Env),
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

	return result
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

// copyHooks returns a deep copy of a HooksConfig. Each hook event's script
// list is independently copied so mutations don't affect the original.
func copyHooks(h config.HooksConfig) config.HooksConfig {
	if h == nil {
		return nil
	}
	out := make(config.HooksConfig, len(h))
	for k, v := range h {
		out[k] = slices.Clone(v)
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
