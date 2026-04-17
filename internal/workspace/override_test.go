package workspace

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/vault"
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
			"myrepo": {Claude: &config.ClaudeOverride{Enabled: boolPtr(true)}},
		},
	}
	if !ClaudeEnabled(ws, "myrepo") {
		t.Error("claude.enabled = true should return true")
	}
}

func TestClaudeEnabledFalse(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"myrepo": {Claude: &config.ClaudeOverride{Enabled: boolPtr(false)}},
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

func TestDefaultBranch_PerRepoOverride(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test", DefaultBranch: "master"},
		Repos: map[string]config.RepoOverride{
			"myrepo": {Branch: "develop"},
		},
	}
	if got := DefaultBranch(ws, "myrepo"); got != "develop" {
		t.Errorf("expected develop, got %q", got)
	}
}

func TestDefaultBranch_WorkspaceDefault(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test", DefaultBranch: "master"},
	}
	if got := DefaultBranch(ws, "myrepo"); got != "master" {
		t.Errorf("expected master, got %q", got)
	}
}

func TestDefaultBranch_FallbackMain(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
	}
	if got := DefaultBranch(ws, "myrepo"); got != "main" {
		t.Errorf("expected main, got %q", got)
	}
}

func TestMergeOverridesNoOverride(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Hooks:    config.HooksConfig{"pre_tool_use": {{Scripts: []string{"a.sh"}}}},
			Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "bypass"}},
		},
		Env: config.EnvConfig{Files: []string{"ws.env"}},
	}
	eff := MergeOverrides(ws, "unknown-repo")

	// Should return copies of workspace values.
	if len(eff.Claude.Hooks) != 1 {
		t.Errorf("expected 1 hook key, got %d", len(eff.Claude.Hooks))
	}
	if eff.Claude.Settings["permissions"].Plain != "bypass" {
		t.Errorf("expected permissions=bypass, got %v", eff.Claude.Settings["permissions"].Plain)
	}
}

func TestMergeOverridesSettingsWin(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "bypass"}, "keep": config.MaybeSecret{Plain: "yes"}},
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeOverride{
					Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "ask"}},
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Claude.Settings["permissions"].Plain != "ask" {
		t.Errorf("repo setting should win, got %v", eff.Claude.Settings["permissions"].Plain)
	}
	if eff.Claude.Settings["keep"].Plain != "yes" {
		t.Errorf("workspace setting should be preserved, got %v", eff.Claude.Settings["keep"].Plain)
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
		Env: config.EnvConfig{Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"LOG_LEVEL": config.MaybeSecret{Plain: "info"}}}},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Env: config.EnvConfig{Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"LOG_LEVEL": config.MaybeSecret{Plain: "debug"}}}},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Env.Vars.Values["LOG_LEVEL"].Plain != "debug" {
		t.Errorf("repo env var should win, got %v", eff.Env.Vars.Values["LOG_LEVEL"].Plain)
	}
}

func TestMergeOverridesEnvVarsMerge(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Env: config.EnvConfig{Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"LOG_LEVEL": config.MaybeSecret{Plain: "info"}, "MODE": config.MaybeSecret{Plain: "prod"}}}},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Env: config.EnvConfig{Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"DEBUG": config.MaybeSecret{Plain: "true"}}}},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Env.Vars.Values["LOG_LEVEL"].Plain != "info" {
		t.Errorf("workspace var should be preserved, got %v", eff.Env.Vars.Values["LOG_LEVEL"].Plain)
	}
	if eff.Env.Vars.Values["MODE"].Plain != "prod" {
		t.Errorf("workspace var should be preserved, got %v", eff.Env.Vars.Values["MODE"].Plain)
	}
	if eff.Env.Vars.Values["DEBUG"].Plain != "true" {
		t.Errorf("repo var should be added, got %v", eff.Env.Vars.Values["DEBUG"].Plain)
	}
}

func TestMergeOverridesHooksExtend(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Hooks: config.HooksConfig{"pre_tool_use": {{Scripts: []string{"ws-gate.sh"}}}},
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeOverride{
					Hooks: config.HooksConfig{
						"pre_tool_use": {{Scripts: []string{"repo-gate.sh"}}},
						"stop":         {{Scripts: []string{"repo-stop.sh"}}},
					},
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	// pre_tool_use should be extended (concatenated).
	preToolUse := eff.Claude.Hooks["pre_tool_use"]
	if len(preToolUse) != 2 {
		t.Fatalf("expected 2 hook entries, got %d: %v", len(preToolUse), preToolUse)
	}
	if preToolUse[0].Scripts[0] != "ws-gate.sh" || preToolUse[1].Scripts[0] != "repo-gate.sh" {
		t.Errorf("expected [ws-gate.sh repo-gate.sh], got %v", preToolUse)
	}

	// stop should be a new hook key from repo.
	stop := eff.Claude.Hooks["stop"]
	if len(stop) != 1 || stop[0].Scripts[0] != "repo-stop.sh" {
		t.Errorf("expected [{scripts: [repo-stop.sh]}], got %v", stop)
	}
}

func TestMergeOverridesNilWorkspaceFields(t *testing.T) {
	ws := &config.WorkspaceConfig{
		// All nil/zero workspace-level hooks/settings/env.
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeOverride{
					Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "ask"}},
					Hooks:    config.HooksConfig{"stop": {{Scripts: []string{"stop.sh"}}}},
				},
				Env: config.EnvConfig{Files: []string{"repo.env"}},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Claude.Settings["permissions"].Plain != "ask" {
		t.Errorf("expected permissions=ask, got %v", eff.Claude.Settings["permissions"].Plain)
	}
	stop := eff.Claude.Hooks["stop"]
	if len(stop) != 1 || stop[0].Scripts[0] != "stop.sh" {
		t.Errorf("expected [{scripts: [stop.sh]}], got %v", stop)
	}
	if len(eff.Env.Files) != 1 || eff.Env.Files[0] != "repo.env" {
		t.Errorf("expected [repo.env], got %v", eff.Env.Files)
	}
}

func TestMergeOverridesClaudeEnvVarsWin(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Env: config.ClaudeEnvConfig{
				Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"GH_TOKEN": config.MaybeSecret{Plain: "ws_token"}, "API_TOKEN": config.MaybeSecret{Plain: "ws_api"}}},
			},
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeOverride{
					Env: config.ClaudeEnvConfig{
						Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"GH_TOKEN": config.MaybeSecret{Plain: "repo_token"}}},
					},
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Claude.Env.Vars.Values["GH_TOKEN"].Plain != "repo_token" {
		t.Errorf("expected GH_TOKEN=repo_token, got %v", eff.Claude.Env.Vars.Values["GH_TOKEN"].Plain)
	}
	if eff.Claude.Env.Vars.Values["API_TOKEN"].Plain != "ws_api" {
		t.Errorf("expected API_TOKEN=ws_api, got %v", eff.Claude.Env.Vars.Values["API_TOKEN"].Plain)
	}
}

func TestMergeOverridesClaudeEnvPromoteUnion(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Env: config.ClaudeEnvConfig{
				Promote: []string{"GH_TOKEN", "API_KEY"},
			},
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeOverride{
					Env: config.ClaudeEnvConfig{
						Promote: []string{"API_KEY", "REPO_SECRET"},
					},
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	want := []string{"GH_TOKEN", "API_KEY", "REPO_SECRET"}
	if len(eff.Claude.Env.Promote) != len(want) {
		t.Fatalf("expected promote %v, got %v", want, eff.Claude.Env.Promote)
	}
	for i, k := range want {
		if eff.Claude.Env.Promote[i] != k {
			t.Errorf("promote[%d] = %q, want %q", i, eff.Claude.Env.Promote[i], k)
		}
	}
}

func TestMergeOverridesClaudeEnvNilWorkspace(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeOverride{
					Env: config.ClaudeEnvConfig{
						Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"GH_TOKEN": config.MaybeSecret{Plain: "repo_only"}}},
						Promote: []string{"OTHER"},
					},
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Claude.Env.Vars.Values["GH_TOKEN"].Plain != "repo_only" {
		t.Errorf("expected GH_TOKEN=repo_only, got %v", eff.Claude.Env.Vars.Values["GH_TOKEN"].Plain)
	}
	if len(eff.Claude.Env.Promote) != 1 || eff.Claude.Env.Promote[0] != "OTHER" {
		t.Errorf("expected promote [OTHER], got %v", eff.Claude.Env.Promote)
	}
}

func TestWarnUnknownRepos(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"known":   {Scope: "tactical"},
			"unknown": {Claude: &config.ClaudeOverride{Enabled: boolPtr(false)}},
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
	wsHooks := config.HooksConfig{"pre_tool_use": {{Scripts: []string{"ws.sh"}}}}
	ws := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Hooks: wsHooks,
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeOverride{
					Hooks: config.HooksConfig{"pre_tool_use": {{Scripts: []string{"repo.sh"}}}},
				},
			},
		},
	}
	_ = MergeOverrides(ws, "myrepo")

	// The workspace hooks should not be modified.
	original := ws.Claude.Hooks["pre_tool_use"]
	if len(original) != 1 || original[0].Scripts[0] != "ws.sh" {
		t.Errorf("workspace hooks were mutated: %v", ws.Claude.Hooks["pre_tool_use"])
	}
}

func TestMergeOverridesEnvMutationSafety(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Env: config.EnvConfig{
			Files: []string{"ws.env"},
			Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"A": config.MaybeSecret{Plain: "1"}}},
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Env: config.EnvConfig{
					Files: []string{"repo.env"},
					Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"B": config.MaybeSecret{Plain: "2"}}},
				},
			},
		},
	}
	_ = MergeOverrides(ws, "myrepo")

	if len(ws.Env.Files) != 1 || ws.Env.Files[0] != "ws.env" {
		t.Errorf("workspace env files were mutated: %v", ws.Env.Files)
	}
	if len(ws.Env.Vars.Values) != 1 || ws.Env.Vars.Values["A"].Plain != "1" {
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

func TestMergeOverridesFilesOverride(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Files: map[string]string{
			"ext/design.md": ".claude/ext/",
			"ext/plan.md":   ".claude/ext/",
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Files: map[string]string{
					"ext/design.md":    ".claude/custom/",
					"ext/extra.md":     ".claude/ext/",
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Files["ext/design.md"] != ".claude/custom/" {
		t.Errorf("expected override, got %v", eff.Files["ext/design.md"])
	}
	if eff.Files["ext/plan.md"] != ".claude/ext/" {
		t.Errorf("expected workspace default, got %v", eff.Files["ext/plan.md"])
	}
	if eff.Files["ext/extra.md"] != ".claude/ext/" {
		t.Errorf("expected additive, got %v", eff.Files["ext/extra.md"])
	}
}

func TestMergeOverridesFilesRemoval(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Files: map[string]string{
			"ext/design.md": ".claude/ext/",
			"ext/plan.md":   ".claude/ext/",
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Files: map[string]string{
					"ext/design.md": "",
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if _, ok := eff.Files["ext/design.md"]; ok {
		t.Error("expected design.md to be removed")
	}
	if eff.Files["ext/plan.md"] != ".claude/ext/" {
		t.Errorf("expected plan.md unchanged, got %v", eff.Files["ext/plan.md"])
	}
}

// --- ResolveGlobalOverride tests ---

func TestResolveGlobalOverrideNoWorkspace(t *testing.T) {
	g := &config.GlobalConfigOverride{
		Global: config.GlobalOverride{
			Env: config.EnvConfig{Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"LANG": config.MaybeSecret{Plain: "en"}}}},
		},
	}
	result := ResolveGlobalOverride(g, "nonexistent")
	if result.Env.Vars.Values["LANG"].Plain != "en" {
		t.Errorf("expected global LANG=en, got %q", result.Env.Vars.Values["LANG"].Plain)
	}
}

func TestResolveGlobalOverrideNil(t *testing.T) {
	result := ResolveGlobalOverride(nil, "anything")
	if result.Claude != nil || len(result.Env.Files) > 0 || len(result.Files) > 0 {
		t.Error("expected zero value for nil input")
	}
}

func TestResolveGlobalOverrideWorkspaceVarsWin(t *testing.T) {
	g := &config.GlobalConfigOverride{
		Global: config.GlobalOverride{
			Env: config.EnvConfig{Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"TOKEN": config.MaybeSecret{Plain: "global"}}}},
		},
		Workspaces: map[string]config.GlobalOverride{
			"my-ws": {
				Env: config.EnvConfig{Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"TOKEN": config.MaybeSecret{Plain: "ws-specific"}}}},
			},
		},
	}
	result := ResolveGlobalOverride(g, "my-ws")
	if result.Env.Vars.Values["TOKEN"].Plain != "ws-specific" {
		t.Errorf("workspace TOKEN should win, got %q", result.Env.Vars.Values["TOKEN"].Plain)
	}
}

// --- MergeGlobalOverride tests ---

// mustMerge wraps MergeGlobalOverride for tests that expect success.
// After Issue 4, MergeGlobalOverride returns (*WorkspaceConfig, error)
// so it can surface vault.ErrTeamOnlyLocked; success-path tests use
// this helper to keep their assertions uncluttered.
func mustMerge(t *testing.T, ws *config.WorkspaceConfig, g config.GlobalOverride, globalConfigDir string) *config.WorkspaceConfig {
	t.Helper()
	merged, err := MergeGlobalOverride(ws, g, globalConfigDir)
	if err != nil {
		t.Fatalf("MergeGlobalOverride: %v", err)
	}
	return merged
}

func TestMergeGlobalOverrideHooksAppend(t *testing.T) {
	globalDir := "/global/config"
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Claude: config.ClaudeConfig{
			Hooks: config.HooksConfig{
				"pre_tool_use": {
					{Scripts: []string{"ws-hook.sh"}},
				},
			},
		},
	}
	g := config.GlobalOverride{
		Claude: &config.ClaudeOverride{
			Hooks: config.HooksConfig{
				"pre_tool_use": {
					{Scripts: []string{"global/gate.sh"}},
				},
			},
		},
	}
	merged := mustMerge(t, ws, g, globalDir)

	entries := merged.Claude.Hooks["pre_tool_use"]
	if len(entries) != 2 {
		t.Fatalf("expected 2 hook entries, got %d", len(entries))
	}
	// Workspace hook first.
	if entries[0].Scripts[0] != "ws-hook.sh" {
		t.Errorf("first hook should be workspace, got %q", entries[0].Scripts[0])
	}
	// Global hook appended, script resolved to absolute path.
	wantAbs := "/global/config/global/gate.sh"
	if entries[1].Scripts[0] != wantAbs {
		t.Errorf("second hook script = %q, want %q", entries[1].Scripts[0], wantAbs)
	}
}

func TestMergeGlobalOverrideSettingsGlobalWins(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Claude: config.ClaudeConfig{
			Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "ask"}},
		},
	}
	g := config.GlobalOverride{
		Claude: &config.ClaudeOverride{
			Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "bypass"}},
		},
	}
	merged := mustMerge(t, ws, g, "/global")
	if merged.Claude.Settings["permissions"].Plain != "bypass" {
		t.Errorf("global settings should win, got %q", merged.Claude.Settings["permissions"].Plain)
	}
}

func TestMergeGlobalOverrideEnvPromoteUnion(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Claude: config.ClaudeConfig{
			Env: config.ClaudeEnvConfig{Promote: []string{"A", "B"}},
		},
	}
	g := config.GlobalOverride{
		Claude: &config.ClaudeOverride{
			Env: config.ClaudeEnvConfig{Promote: []string{"B", "C"}},
		},
	}
	merged := mustMerge(t, ws, g, "/global")
	promote := merged.Claude.Env.Promote
	seen := make(map[string]bool)
	for _, k := range promote {
		seen[k] = true
	}
	for _, want := range []string{"A", "B", "C"} {
		if !seen[want] {
			t.Errorf("promote should contain %q", want)
		}
	}
}

func TestMergeGlobalOverrideEnvVarsGlobalWins(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Claude: config.ClaudeConfig{
			Env: config.ClaudeEnvConfig{Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"LANG": config.MaybeSecret{Plain: "ws"}}}},
		},
	}
	g := config.GlobalOverride{
		Claude: &config.ClaudeOverride{
			Env: config.ClaudeEnvConfig{Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"LANG": config.MaybeSecret{Plain: "global"}}}},
		},
	}
	merged := mustMerge(t, ws, g, "/global")
	if merged.Claude.Env.Vars.Values["LANG"].Plain != "global" {
		t.Errorf("global env var should win, got %q", merged.Claude.Env.Vars.Values["LANG"].Plain)
	}
}

func TestMergeGlobalOverridePluginsUnion(t *testing.T) {
	wsPlugins := []string{"plugA", "plugB"}
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Claude:    config.ClaudeConfig{Plugins: &wsPlugins},
	}
	globalPlugins := []string{"plugB", "plugC"}
	g := config.GlobalOverride{
		Claude: &config.ClaudeOverride{Plugins: &globalPlugins},
	}
	merged := mustMerge(t, ws, g, "/global")

	if merged.Claude.Plugins == nil {
		t.Fatal("merged plugins should not be nil")
	}
	pluginSet := make(map[string]bool)
	for _, p := range *merged.Claude.Plugins {
		pluginSet[p] = true
	}
	for _, want := range []string{"plugA", "plugB", "plugC"} {
		if !pluginSet[want] {
			t.Errorf("merged plugins should contain %q", want)
		}
	}
	if len(*merged.Claude.Plugins) != 3 {
		t.Errorf("expected 3 unique plugins, got %d", len(*merged.Claude.Plugins))
	}
}

func TestMergeGlobalOverrideEnvFilesAppend(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Env:       config.EnvConfig{Files: []string{"ws.env"}},
	}
	g := config.GlobalOverride{
		Env: config.EnvConfig{Files: []string{"global.env"}},
	}
	merged := mustMerge(t, ws, g, "/global")
	if len(merged.Env.Files) != 2 || merged.Env.Files[0] != "ws.env" || merged.Env.Files[1] != "global.env" {
		t.Errorf("env files should be [ws.env, global.env], got %v", merged.Env.Files)
	}
}

func TestMergeGlobalOverrideFilesGlobalWins(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Files:     map[string]string{"key.env": "dest/ws/"},
	}
	g := config.GlobalOverride{
		Files: map[string]string{"key.env": "dest/global/"},
	}
	merged := mustMerge(t, ws, g, "/global")
	if merged.Files["key.env"] != "dest/global/" {
		t.Errorf("global files should win, got %q", merged.Files["key.env"])
	}
}

func TestMergeGlobalOverrideFilesEmptyStringSuppresses(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Files:     map[string]string{"key.env": "dest/"},
	}
	g := config.GlobalOverride{
		Files: map[string]string{"key.env": ""},
	}
	merged := mustMerge(t, ws, g, "/global")
	if _, ok := merged.Files["key.env"]; ok {
		t.Error("empty global value should suppress workspace mapping")
	}
}

func TestMergeGlobalOverrideDoesNotMutateInput(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Env:       config.EnvConfig{Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"X": config.MaybeSecret{Plain: "original"}}}},
	}
	g := config.GlobalOverride{
		Env: config.EnvConfig{Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"X": config.MaybeSecret{Plain: "global"}}}},
	}
	_ = mustMerge(t, ws, g, "/global")
	if ws.Env.Vars.Values["X"].Plain != "original" {
		t.Error("MergeGlobalOverride should not mutate the input ws")
	}
}

// TestMergeGlobalOverrideEnvSecretsGlobalWins covers the Issue 4
// fix where MergeGlobalOverride merges Env.Secrets.Values. Before the
// fix, values in a personal overlay's env.secrets were silently
// dropped during merge, defeating the resolver's auto-wrap contract
// for overlay-supplied plaintext.
func TestMergeGlobalOverrideEnvSecretsGlobalWins(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"TOKEN": {Plain: "ws-secret"}}},
		},
	}
	g := config.GlobalOverride{
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"TOKEN": {Plain: "overlay-secret"}}},
		},
	}
	merged := mustMerge(t, ws, g, "/global")
	if merged.Env.Secrets.Values["TOKEN"].Plain != "overlay-secret" {
		t.Errorf("overlay env.secrets should win, got %q", merged.Env.Secrets.Values["TOKEN"].Plain)
	}
}

// TestMergeGlobalOverrideClaudeEnvSecretsGlobalWins covers the same
// fix for the claude.env.secrets branch.
func TestMergeGlobalOverrideClaudeEnvSecretsGlobalWins(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Claude: config.ClaudeConfig{
			Env: config.ClaudeEnvConfig{Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"K": {Plain: "ws"}}}},
		},
	}
	g := config.GlobalOverride{
		Claude: &config.ClaudeOverride{
			Env: config.ClaudeEnvConfig{Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"K": {Plain: "overlay"}}}},
		},
	}
	merged := mustMerge(t, ws, g, "/global")
	if merged.Claude.Env.Secrets.Values["K"].Plain != "overlay" {
		t.Errorf("overlay claude.env.secrets should win, got %q", merged.Claude.Env.Secrets.Values["K"].Plain)
	}
}

// TestMergeGlobalOverrideTeamOnlyBlocksOverride: a personal overlay
// that writes over a team-declared key listed in [vault].team_only is
// rejected with ErrTeamOnlyLocked.
func TestMergeGlobalOverrideTeamOnlyBlocksOverride(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Vault: &config.VaultRegistry{
			TeamOnly: []string{"LOCKED"},
		},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"LOCKED": {Plain: "team-secret"}}},
		},
	}
	g := config.GlobalOverride{
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"LOCKED": {Plain: "overlay-attempt"}}},
		},
	}
	_, err := MergeGlobalOverride(ws, g, "/global")
	if err == nil {
		t.Fatal("expected ErrTeamOnlyLocked, got nil")
	}
	if !errors.Is(err, vault.ErrTeamOnlyLocked) {
		t.Errorf("expected ErrTeamOnlyLocked, got %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "LOCKED") {
		t.Errorf("error must name the locked key, got %q", msg)
	}
}

// TestMergeGlobalOverrideTeamOnlyAllowsOverlayAdd: a personal overlay
// that adds a key NOT previously declared by the team is allowed even
// if the key name appears in team_only (team_only protects team-
// declared keys; new overlay additions are fine). Realistically this
// only matters when team_only lists a key that the team has NOT yet
// populated -- the list is aspirational.
func TestMergeGlobalOverrideTeamOnlyAllowsOverlayAdd(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Vault: &config.VaultRegistry{
			TeamOnly: []string{"LOCKED_BUT_UNSET"},
		},
		// No team value for LOCKED_BUT_UNSET; team_only in that case
		// does not trigger because there is nothing to protect.
	}
	g := config.GlobalOverride{
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"LOCKED_BUT_UNSET": {Plain: "overlay"}}},
		},
	}
	merged, err := MergeGlobalOverride(ws, g, "/global")
	if err != nil {
		t.Fatalf("expected overlay add to succeed, got %v", err)
	}
	if merged.Env.Secrets.Values["LOCKED_BUT_UNSET"].Plain != "overlay" {
		t.Errorf("overlay-added value should be present, got %q", merged.Env.Secrets.Values["LOCKED_BUT_UNSET"].Plain)
	}
}

// TestMergeGlobalOverrideTeamOnlyBlocksClaudeSettings: team_only also
// applies to settings and claude env.
func TestMergeGlobalOverrideTeamOnlyBlocksClaudeSettings(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Vault: &config.VaultRegistry{TeamOnly: []string{"permissions"}},
		Claude: config.ClaudeConfig{
			Settings: config.SettingsConfig{"permissions": {Plain: "bypass"}},
		},
	}
	g := config.GlobalOverride{
		Claude: &config.ClaudeOverride{
			Settings: config.SettingsConfig{"permissions": {Plain: "ask"}},
		},
	}
	_, err := MergeGlobalOverride(ws, g, "/global")
	if err == nil {
		t.Fatal("expected ErrTeamOnlyLocked for settings override")
	}
	if !errors.Is(err, vault.ErrTeamOnlyLocked) {
		t.Errorf("expected ErrTeamOnlyLocked, got %v", err)
	}
}

// TestMergeOverridesEnvSecretsWin: per-repo env.secrets values win
// over workspace-level env.secrets values.
func TestMergeOverridesEnvSecretsWin(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"K": {Plain: "ws"}}},
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Env: config.EnvConfig{
					Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"K": {Plain: "repo"}}},
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")
	if eff.Env.Secrets.Values["K"].Plain != "repo" {
		t.Errorf("per-repo env.secrets should win, got %q", eff.Env.Secrets.Values["K"].Plain)
	}
}

// TestMergeInstanceOverridesEnvSecretsWin: instance-level env.secrets
// values win over workspace-level env.secrets values.
func TestMergeInstanceOverridesEnvSecretsWin(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"K": {Plain: "ws"}}},
		},
		Instance: config.InstanceConfig{
			Env: config.EnvConfig{
				Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"K": {Plain: "inst"}}},
			},
		},
	}
	eff := MergeInstanceOverrides(ws)
	if eff.Env.Secrets.Values["K"].Plain != "inst" {
		t.Errorf("instance env.secrets should win, got %q", eff.Env.Secrets.Values["K"].Plain)
	}
}
