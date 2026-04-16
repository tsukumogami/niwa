package resolve

import (
	"maps"
	"slices"

	"github.com/tsukumogami/niwa/internal/config"
)

// Deep-copy helpers for resolver output. The resolver's contract is
// "returns a NEW *WorkspaceConfig -- never mutate the input", which
// means every map and slice carrying MaybeSecret must be cloned so
// the walker can mutate the copy in place.
//
// Fields with no MaybeSecret values (Workspace metadata, Sources,
// Groups, ContentConfig) are shared by value; they carry only
// immutable strings and read-only slices.
//
// Keep these helpers in sync with internal/workspace/override.go's
// copy* functions: they are separate so resolve doesn't import
// workspace, but the merge semantics share the same copy strategy.

func deepCopyWorkspaceConfig(in *config.WorkspaceConfig) *config.WorkspaceConfig {
	if in == nil {
		return nil
	}
	out := *in
	out.Claude = deepCopyClaudeConfig(in.Claude)
	out.Env = deepCopyEnv(in.Env)
	out.Files = cloneStringMap(in.Files)
	out.Repos = deepCopyRepos(in.Repos)
	out.Instance = deepCopyInstance(in.Instance)
	// Vault is not mutated by the resolver: it is the source of
	// truth for provider selection. Share by pointer.
	return &out
}

func deepCopyClaudeConfig(in config.ClaudeConfig) config.ClaudeConfig {
	out := in
	out.Settings = cloneSettings(in.Settings)
	out.Env = deepCopyClaudeEnv(in.Env)
	if in.Plugins != nil {
		p := slices.Clone(*in.Plugins)
		out.Plugins = &p
	}
	out.Hooks = cloneHooks(in.Hooks)
	return out
}

func deepCopyClaudeOverride(in *config.ClaudeOverride) *config.ClaudeOverride {
	if in == nil {
		return nil
	}
	out := *in
	out.Settings = cloneSettings(in.Settings)
	out.Env = deepCopyClaudeEnv(in.Env)
	if in.Plugins != nil {
		p := slices.Clone(*in.Plugins)
		out.Plugins = &p
	}
	out.Hooks = cloneHooks(in.Hooks)
	return &out
}

func deepCopyEnv(in config.EnvConfig) config.EnvConfig {
	return config.EnvConfig{
		Files:   slices.Clone(in.Files),
		Vars:    cloneEnvVarsTable(in.Vars),
		Secrets: cloneEnvVarsTable(in.Secrets),
	}
}

func deepCopyClaudeEnv(in config.ClaudeEnvConfig) config.ClaudeEnvConfig {
	return config.ClaudeEnvConfig{
		Promote: slices.Clone(in.Promote),
		Vars:    cloneEnvVarsTable(in.Vars),
		Secrets: cloneEnvVarsTable(in.Secrets),
	}
}

func deepCopyRepos(in map[string]config.RepoOverride) map[string]config.RepoOverride {
	if in == nil {
		return nil
	}
	out := make(map[string]config.RepoOverride, len(in))
	for name, ov := range in {
		out[name] = config.RepoOverride{
			URL:      ov.URL,
			Group:    ov.Group,
			Branch:   ov.Branch,
			Scope:    ov.Scope,
			Claude:   deepCopyClaudeOverride(ov.Claude),
			Env:      deepCopyEnv(ov.Env),
			Files:    cloneStringMap(ov.Files),
			SetupDir: ov.SetupDir,
		}
	}
	return out
}

func deepCopyInstance(in config.InstanceConfig) config.InstanceConfig {
	return config.InstanceConfig{
		Claude: deepCopyClaudeOverride(in.Claude),
		Env:    deepCopyEnv(in.Env),
		Files:  cloneStringMap(in.Files),
	}
}

func deepCopyGlobalConfigOverride(in *config.GlobalConfigOverride) *config.GlobalConfigOverride {
	if in == nil {
		return nil
	}
	out := &config.GlobalConfigOverride{
		Global: deepCopyGlobalOverride(in.Global),
	}
	if in.Workspaces != nil {
		out.Workspaces = make(map[string]config.GlobalOverride, len(in.Workspaces))
		for name, ov := range in.Workspaces {
			out.Workspaces[name] = deepCopyGlobalOverride(ov)
		}
	}
	return out
}

func deepCopyGlobalOverride(in config.GlobalOverride) config.GlobalOverride {
	return config.GlobalOverride{
		Claude: deepCopyClaudeOverride(in.Claude),
		Env:    deepCopyEnv(in.Env),
		Files:  cloneStringMap(in.Files),
		Vault:  in.Vault, // shared; resolver does not mutate
	}
}

func cloneEnvVarsTable(in config.EnvVarsTable) config.EnvVarsTable {
	out := config.EnvVarsTable{}
	if in.Values != nil {
		out.Values = make(map[string]config.MaybeSecret, len(in.Values))
		maps.Copy(out.Values, in.Values)
	}
	if in.Required != nil {
		out.Required = make(map[string]string, len(in.Required))
		maps.Copy(out.Required, in.Required)
	}
	if in.Recommended != nil {
		out.Recommended = make(map[string]string, len(in.Recommended))
		maps.Copy(out.Recommended, in.Recommended)
	}
	if in.Optional != nil {
		out.Optional = make(map[string]string, len(in.Optional))
		maps.Copy(out.Optional, in.Optional)
	}
	return out
}

func cloneSettings(in config.SettingsConfig) config.SettingsConfig {
	if in == nil {
		return nil
	}
	out := make(config.SettingsConfig, len(in))
	maps.Copy(out, in)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func cloneHooks(in config.HooksConfig) config.HooksConfig {
	if in == nil {
		return nil
	}
	out := make(config.HooksConfig, len(in))
	for event, entries := range in {
		cp := make([]config.HookEntry, len(entries))
		for i, e := range entries {
			cp[i] = config.HookEntry{
				Matcher: e.Matcher,
				Scripts: slices.Clone(e.Scripts),
			}
		}
		out[event] = cp
	}
	return out
}
