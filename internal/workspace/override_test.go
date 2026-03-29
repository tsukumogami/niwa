package workspace

import (
	"sort"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func boolPtr(b bool) *bool { return &b }

func TestClaudeEnabledDefault(t *testing.T) {
	ws := &config.WorkspaceConfig{}
	if !ClaudeEnabled(ws, "myrepo") {
		t.Error("claude should default to true when no override exists")
	}
}

func TestClaudeEnabledTrue(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"myrepo": {Claude: &config.ClaudeConfig{Enabled: boolPtr(true)}},
		},
	}
	if !ClaudeEnabled(ws, "myrepo") {
		t.Error("claude.enabled = true should return true")
	}
}

func TestClaudeEnabledFalse(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"myrepo": {Claude: &config.ClaudeConfig{Enabled: boolPtr(false)}},
		},
	}
	if ClaudeEnabled(ws, "myrepo") {
		t.Error("claude.enabled = false should return false")
	}
}

func TestClaudeEnabledNilPointer(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"myrepo": {Scope: "tactical"}, // claude not set
		},
	}
	if !ClaudeEnabled(ws, "myrepo") {
		t.Error("claude = nil (unset) should default to true")
	}
}

func TestRepoCloneURLDefault(t *testing.T) {
	ws := &config.WorkspaceConfig{}
	got := RepoCloneURL(ws, "myrepo", "git@github.com:org/myrepo.git", "https://github.com/org/myrepo.git")
	if got != "git@github.com:org/myrepo.git" {
		t.Errorf("expected SSH URL, got %q", got)
	}
}

func TestRepoCloneURLOverride(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"myrepo": {URL: "git@gitlab.com:custom/myrepo.git"},
		},
	}
	got := RepoCloneURL(ws, "myrepo", "git@github.com:org/myrepo.git", "https://github.com/org/myrepo.git")
	if got != "git@gitlab.com:custom/myrepo.git" {
		t.Errorf("expected override URL, got %q", got)
	}
}

func TestRepoCloneURLFallbackHTTPS(t *testing.T) {
	ws := &config.WorkspaceConfig{}
	got := RepoCloneURL(ws, "myrepo", "", "https://github.com/org/myrepo.git")
	if got != "https://github.com/org/myrepo.git" {
		t.Errorf("expected HTTPS URL, got %q", got)
	}
}

func TestRepoCloneBranchDefault(t *testing.T) {
	ws := &config.WorkspaceConfig{}
	if got := RepoCloneBranch(ws, "myrepo"); got != "" {
		t.Errorf("expected empty branch, got %q", got)
	}
}

func TestRepoCloneBranchOverride(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"myrepo": {Branch: "develop"},
		},
	}
	if got := RepoCloneBranch(ws, "myrepo"); got != "develop" {
		t.Errorf("expected develop, got %q", got)
	}
}

func TestMergeOverridesNoOverride(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Hooks:    config.HooksConfig{"pre_tool_use": {"a.sh"}},
			Settings: config.SettingsConfig{"permissions": "bypass"},
		},
		Env: config.EnvConfig{Files: []string{"ws.env"}},
	}
	eff := MergeOverrides(ws, "unknown-repo")

	// Should return copies of workspace values.
	if len(eff.Claude.Hooks) != 1 {
		t.Errorf("expected 1 hook key, got %d", len(eff.Claude.Hooks))
	}
	if eff.Claude.Settings["permissions"] != "bypass" {
		t.Errorf("expected permissions=bypass, got %v", eff.Claude.Settings["permissions"])
	}
}

func TestMergeOverridesSettingsWin(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Settings: config.SettingsConfig{"permissions": "bypass", "keep": "yes"},
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeConfig{
					Settings: config.SettingsConfig{"permissions": "ask"},
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Claude.Settings["permissions"] != "ask" {
		t.Errorf("repo setting should win, got %v", eff.Claude.Settings["permissions"])
	}
	if eff.Claude.Settings["keep"] != "yes" {
		t.Errorf("workspace setting should be preserved, got %v", eff.Claude.Settings["keep"])
	}
}

func TestMergeOverridesEnvFilesAppend(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Env: config.EnvConfig{Files: []string{"ws.env"}},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Env: config.EnvConfig{Files: []string{"repo.env"}},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if len(eff.Env.Files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(eff.Env.Files), eff.Env.Files)
	}
	if eff.Env.Files[0] != "ws.env" || eff.Env.Files[1] != "repo.env" {
		t.Errorf("expected [ws.env repo.env], got %v", eff.Env.Files)
	}
}

func TestMergeOverridesEnvVarsWin(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Env: config.EnvConfig{Vars: map[string]string{"LOG_LEVEL": "info"}},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Env: config.EnvConfig{Vars: map[string]string{"LOG_LEVEL": "debug"}},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Env.Vars["LOG_LEVEL"] != "debug" {
		t.Errorf("repo env var should win, got %v", eff.Env.Vars["LOG_LEVEL"])
	}
}

func TestMergeOverridesEnvVarsMerge(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Env: config.EnvConfig{Vars: map[string]string{"LOG_LEVEL": "info", "MODE": "prod"}},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Env: config.EnvConfig{Vars: map[string]string{"DEBUG": "true"}},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Env.Vars["LOG_LEVEL"] != "info" {
		t.Errorf("workspace var should be preserved, got %v", eff.Env.Vars["LOG_LEVEL"])
	}
	if eff.Env.Vars["MODE"] != "prod" {
		t.Errorf("workspace var should be preserved, got %v", eff.Env.Vars["MODE"])
	}
	if eff.Env.Vars["DEBUG"] != "true" {
		t.Errorf("repo var should be added, got %v", eff.Env.Vars["DEBUG"])
	}
}

func TestMergeOverridesHooksExtend(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Hooks: config.HooksConfig{"pre_tool_use": {"ws-gate.sh"}},
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeConfig{
					Hooks: config.HooksConfig{
						"pre_tool_use": {"repo-gate.sh"},
						"stop":         {"repo-stop.sh"},
					},
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	// pre_tool_use should be extended (concatenated).
	preToolUse := eff.Claude.Hooks["pre_tool_use"]
	if len(preToolUse) != 2 {
		t.Fatalf("expected 2 hooks, got %d: %v", len(preToolUse), preToolUse)
	}
	if preToolUse[0] != "ws-gate.sh" || preToolUse[1] != "repo-gate.sh" {
		t.Errorf("expected [ws-gate.sh repo-gate.sh], got %v", preToolUse)
	}

	// stop should be a new hook key from repo.
	stop := eff.Claude.Hooks["stop"]
	if len(stop) != 1 || stop[0] != "repo-stop.sh" {
		t.Errorf("expected [repo-stop.sh], got %v", stop)
	}
}

func TestMergeOverridesNilWorkspaceFields(t *testing.T) {
	ws := &config.WorkspaceConfig{
		// All nil/zero workspace-level hooks/settings/env.
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeConfig{
					Settings: config.SettingsConfig{"permissions": "ask"},
					Hooks:    config.HooksConfig{"stop": {"stop.sh"}},
				},
				Env: config.EnvConfig{Files: []string{"repo.env"}},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Claude.Settings["permissions"] != "ask" {
		t.Errorf("expected permissions=ask, got %v", eff.Claude.Settings["permissions"])
	}
	stop := eff.Claude.Hooks["stop"]
	if len(stop) != 1 || stop[0] != "stop.sh" {
		t.Errorf("expected [stop.sh], got %v", stop)
	}
	if len(eff.Env.Files) != 1 || eff.Env.Files[0] != "repo.env" {
		t.Errorf("expected [repo.env], got %v", eff.Env.Files)
	}
}

func TestMergeOverridesClaudeEnvWin(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Env: map[string]string{
				"GH_TOKEN":  "ws_token",
				"API_TOKEN": "ws_api",
			},
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeConfig{
					Env: map[string]string{
						"GH_TOKEN": "repo_token",
					},
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Claude.Env["GH_TOKEN"] != "repo_token" {
		t.Errorf("expected GH_TOKEN=repo_token, got %v", eff.Claude.Env["GH_TOKEN"])
	}
	if eff.Claude.Env["API_TOKEN"] != "ws_api" {
		t.Errorf("expected API_TOKEN=ws_api, got %v", eff.Claude.Env["API_TOKEN"])
	}
}

func TestMergeOverridesClaudeEnvNilWorkspace(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeConfig{
					Env: map[string]string{"GH_TOKEN": "repo_only"},
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Claude.Env["GH_TOKEN"] != "repo_only" {
		t.Errorf("expected GH_TOKEN=repo_only, got %v", eff.Claude.Env["GH_TOKEN"])
	}
}

func TestWarnUnknownRepos(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"known":   {Scope: "tactical"},
			"unknown": {Claude: &config.ClaudeConfig{Enabled: boolPtr(false)}},
		},
	}
	known := map[string]bool{"known": true, "other": true}

	warnings := WarnUnknownRepos(ws, known)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if warnings[0] != "repos override unknown does not match any discovered repo" {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestWarnUnknownReposAllKnown(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"alpha": {Scope: "tactical"},
			"beta":  {Branch: "develop"},
		},
	}
	known := map[string]bool{"alpha": true, "beta": true}

	warnings := WarnUnknownRepos(ws, known)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestKnownRepoNames(t *testing.T) {
	ws := &config.WorkspaceConfig{}
	discovered := []string{"alpha", "beta", "gamma"}
	known := KnownRepoNames(ws, discovered)

	for _, name := range discovered {
		if !known[name] {
			t.Errorf("%s should be known", name)
		}
	}
	if known["delta"] {
		t.Error("delta should not be known")
	}
}

func TestMergeOverridesMutationSafety(t *testing.T) {
	// Verify that merging doesn't mutate the workspace-level maps.
	wsHooks := config.HooksConfig{"pre_tool_use": {"ws.sh"}}
	ws := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Hooks: wsHooks,
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeConfig{
					Hooks: config.HooksConfig{"pre_tool_use": {"repo.sh"}},
				},
			},
		},
	}
	_ = MergeOverrides(ws, "myrepo")

	// The workspace hooks should not be modified.
	original := ws.Claude.Hooks["pre_tool_use"]
	if len(original) != 1 || original[0] != "ws.sh" {
		t.Errorf("workspace hooks were mutated: %v", ws.Claude.Hooks["pre_tool_use"])
	}
}

func TestMergeOverridesEnvMutationSafety(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Env: config.EnvConfig{
			Files: []string{"ws.env"},
			Vars:  map[string]string{"A": "1"},
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Env: config.EnvConfig{
					Files: []string{"repo.env"},
					Vars:  map[string]string{"B": "2"},
				},
			},
		},
	}
	_ = MergeOverrides(ws, "myrepo")

	if len(ws.Env.Files) != 1 || ws.Env.Files[0] != "ws.env" {
		t.Errorf("workspace env files were mutated: %v", ws.Env.Files)
	}
	if len(ws.Env.Vars) != 1 || ws.Env.Vars["A"] != "1" {
		t.Errorf("workspace env vars were mutated: %v", ws.Env.Vars)
	}
}

func TestWarnUnknownReposDeterministic(t *testing.T) {
	// Multiple unknown repos should all generate warnings.
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"a-unknown": {},
			"b-unknown": {},
		},
	}
	known := map[string]bool{}

	warnings := WarnUnknownRepos(ws, known)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d", len(warnings))
	}
	sort.Strings(warnings)
	if warnings[0] != "repos override a-unknown does not match any discovered repo" {
		t.Errorf("unexpected warning[0]: %s", warnings[0])
	}
	if warnings[1] != "repos override b-unknown does not match any discovered repo" {
		t.Errorf("unexpected warning[1]: %s", warnings[1])
	}
}
