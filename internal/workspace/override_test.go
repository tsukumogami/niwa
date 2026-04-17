package workspace

import (
	"errors"
	"os"
	"path/filepath"
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
						Vars:    config.EnvVarsTable{Values: map[string]config.MaybeSecret{"GH_TOKEN": config.MaybeSecret{Plain: "repo_only"}}},
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
			Vars:  config.EnvVarsTable{Values: map[string]config.MaybeSecret{"A": config.MaybeSecret{Plain: "1"}}},
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Env: config.EnvConfig{
					Files: []string{"repo.env"},
					Vars:  config.EnvVarsTable{Values: map[string]config.MaybeSecret{"B": config.MaybeSecret{Plain: "2"}}},
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
					"ext/design.md": ".claude/custom/",
					"ext/extra.md":  ".claude/ext/",
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
		Vault:     &config.VaultRegistry{TeamOnly: []string{"permissions"}},
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

// --- MergeWorkspaceOverlay tests ---

func baseWS() *config.WorkspaceConfig {
	return &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Sources: []config.SourceConfig{
			{Org: "baseorg", Repos: []string{"repo-a"}},
		},
		Groups: map[string]config.GroupConfig{
			"base-group": {Visibility: "public", Repos: []string{"repo-a"}},
		},
		Repos: map[string]config.RepoOverride{
			"repo-a": {Branch: "main"},
		},
		Claude: config.ClaudeConfig{
			Hooks:    config.HooksConfig{"pre_tool_use": {{Scripts: []string{"base.sh"}}}},
			Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "bypass"}},
			Content: config.ContentConfig{
				Repos: map[string]config.RepoContentEntry{
					"repo-a": {Source: "repos/repo-a.md"},
				},
			},
		},
		Env: config.EnvConfig{
			Files: []string{"base.env"},
			Vars:  config.EnvVarsTable{Values: map[string]config.MaybeSecret{"LOG": {Plain: "info"}}},
		},
		Files: map[string]string{"base-src": "base-dest"},
	}
}

func emptyOverlay() *config.WorkspaceOverlay {
	return &config.WorkspaceOverlay{}
}

// TestMergeWorkspaceOverlay_DoesNotMutate verifies the base WorkspaceConfig is
// not mutated by the merge.
func TestMergeWorkspaceOverlay_DoesNotMutate(t *testing.T) {
	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Sources: []config.OverlaySourceConfig{{Org: "neworg", Repos: []string{"repo-b"}}},
	}
	_, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ws.Sources) != 1 {
		t.Error("base ws.Sources was mutated")
	}
}

// TestMergeWorkspaceOverlay_SourcesAppended verifies overlay sources are appended.
func TestMergeWorkspaceOverlay_SourcesAppended(t *testing.T) {
	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Sources: []config.OverlaySourceConfig{{Org: "neworg", Repos: []string{"repo-b"}}},
	}
	merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(merged.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(merged.Sources))
	}
	if merged.Sources[1].Org != "neworg" {
		t.Errorf("expected overlay source org=neworg, got %q", merged.Sources[1].Org)
	}
}

// TestMergeWorkspaceOverlay_DuplicateOrgReturnsError verifies that adding an
// overlay source whose org already exists in the base returns an error.
func TestMergeWorkspaceOverlay_DuplicateOrgReturnsError(t *testing.T) {
	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Sources: []config.OverlaySourceConfig{{Org: "baseorg", Repos: []string{"repo-x"}}},
	}
	_, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err == nil {
		t.Fatal("expected error for duplicate org, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error %q does not mention already exists", err.Error())
	}
}

// TestMergeWorkspaceOverlay_GroupsBaseWins verifies base wins on group key collision.
func TestMergeWorkspaceOverlay_GroupsBaseWins(t *testing.T) {
	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Groups: map[string]config.GroupConfig{
			"base-group":    {Visibility: "private"}, // collision — base should win
			"overlay-group": {Visibility: "public"},  // new — should be added
		},
	}
	merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.Groups["base-group"].Visibility != "public" {
		t.Errorf("base-group visibility should be public (base wins), got %q", merged.Groups["base-group"].Visibility)
	}
	if _, ok := merged.Groups["overlay-group"]; !ok {
		t.Error("overlay-group was not added")
	}
}

// TestMergeWorkspaceOverlay_ReposBaseWins verifies base wins on repo key collision.
func TestMergeWorkspaceOverlay_ReposBaseWins(t *testing.T) {
	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Repos: map[string]config.RepoOverride{
			"repo-a": {Branch: "overlay-branch"}, // collision — base wins
			"repo-b": {Branch: "feature"},        // new — added
		},
	}
	merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.Repos["repo-a"].Branch != "main" {
		t.Errorf("repo-a branch should be main (base wins), got %q", merged.Repos["repo-a"].Branch)
	}
	if merged.Repos["repo-b"].Branch != "feature" {
		t.Errorf("repo-b branch should be feature (added), got %q", merged.Repos["repo-b"].Branch)
	}
}

// TestMergeWorkspaceOverlay_SettingsBaseWins verifies base wins on settings key collision.
func TestMergeWorkspaceOverlay_SettingsBaseWins(t *testing.T) {
	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Claude: config.OverlayClaudeConfig{
			Settings: config.SettingsConfig{
				"permissions": config.MaybeSecret{Plain: "ask"}, // collision — base wins
				"new-setting": config.MaybeSecret{Plain: "val"}, // new — added
			},
		},
	}
	merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.Claude.Settings["permissions"].Plain != "bypass" {
		t.Errorf("permissions should be bypass (base wins), got %q", merged.Claude.Settings["permissions"].Plain)
	}
	if merged.Claude.Settings["new-setting"].Plain != "val" {
		t.Errorf("new-setting should be val (added), got %q", merged.Claude.Settings["new-setting"].Plain)
	}
}

// TestMergeWorkspaceOverlay_EnvVarsBaseWins verifies base wins on env var collision.
func TestMergeWorkspaceOverlay_EnvVarsBaseWins(t *testing.T) {
	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Env: config.EnvConfig{
			Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
				"LOG":     {Plain: "debug"},   // collision — base wins
				"NEW_VAR": {Plain: "overlay"}, // new — added
			}},
		},
	}
	merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.Env.Vars.Values["LOG"].Plain != "info" {
		t.Errorf("LOG should be info (base wins), got %q", merged.Env.Vars.Values["LOG"].Plain)
	}
	if merged.Env.Vars.Values["NEW_VAR"].Plain != "overlay" {
		t.Errorf("NEW_VAR should be overlay (added), got %q", merged.Env.Vars.Values["NEW_VAR"].Plain)
	}
}

// TestMergeWorkspaceOverlay_EnvFilesAppended verifies overlay env files are
// appended after base env files.
func TestMergeWorkspaceOverlay_EnvFilesAppended(t *testing.T) {
	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Env: config.EnvConfig{Files: []string{"overlay.env"}},
	}
	merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(merged.Env.Files) != 2 {
		t.Fatalf("expected 2 env files, got %d: %v", len(merged.Env.Files), merged.Env.Files)
	}
	if merged.Env.Files[0] != "base.env" || merged.Env.Files[1] != "overlay.env" {
		t.Errorf("expected [base.env overlay.env], got %v", merged.Env.Files)
	}
}

// TestMergeWorkspaceOverlay_FilesBaseWins verifies base wins on files key collision.
func TestMergeWorkspaceOverlay_FilesBaseWins(t *testing.T) {
	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Files: map[string]string{
			"base-src":    "overlay-dest",  // collision — base wins
			"overlay-src": "overlay-dest2", // new — added
		},
	}
	merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.Files["base-src"] != "base-dest" {
		t.Errorf("base-src should be base-dest (base wins), got %q", merged.Files["base-src"])
	}
	if merged.Files["overlay-src"] != "overlay-dest2" {
		t.Errorf("overlay-src should be overlay-dest2 (added), got %q", merged.Files["overlay-src"])
	}
}

// TestMergeWorkspaceOverlay_HookResolutionToAbsolute verifies hook scripts are
// resolved to absolute paths within overlayDir.
func TestMergeWorkspaceOverlay_HookResolutionToAbsolute(t *testing.T) {
	overlayDir := t.TempDir()
	// Create the hook script file so containment check can resolve it.
	hookPath := filepath.Join(overlayDir, "hooks", "my-hook.sh")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Claude: config.OverlayClaudeConfig{
			Hooks: config.HooksConfig{
				"pre_tool_use": {{Scripts: []string{"hooks/my-hook.sh"}}},
			},
		},
	}
	merged, err := MergeWorkspaceOverlay(ws, overlay, overlayDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := merged.Claude.Hooks["pre_tool_use"]
	// Base has 1 entry (base.sh), overlay appends one more.
	if len(entries) != 2 {
		t.Fatalf("expected 2 hook entries, got %d", len(entries))
	}
	overlayEntry := entries[1]
	if len(overlayEntry.Scripts) != 1 {
		t.Fatalf("expected 1 script in overlay entry, got %d", len(overlayEntry.Scripts))
	}
	want := filepath.Join(overlayDir, "hooks", "my-hook.sh")
	if overlayEntry.Scripts[0] != want {
		t.Errorf("hook script = %q, want %q", overlayEntry.Scripts[0], want)
	}
}

// TestMergeWorkspaceOverlay_HookSymlinkEscape verifies that a hook script path
// that resolves outside overlayDir via a symlink returns an error.
func TestMergeWorkspaceOverlay_HookSymlinkEscape(t *testing.T) {
	overlayDir := t.TempDir()
	// Create a symlink inside overlayDir that points outside.
	outside := t.TempDir()
	linkPath := filepath.Join(overlayDir, "hooks")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skip("cannot create symlink:", err)
	}

	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Claude: config.OverlayClaudeConfig{
			Hooks: config.HooksConfig{
				"pre_tool_use": {{Scripts: []string{"hooks/evil.sh"}}},
			},
		},
	}
	_, err := MergeWorkspaceOverlay(ws, overlay, overlayDir)
	if err == nil {
		t.Fatal("expected error for symlink escaping overlayDir, got nil")
	}
}

// TestMergeWorkspaceOverlay_ContentSourceAddsNewEntry verifies that a content
// overlay entry with source= adds a new entry for a repo not in base.
func TestMergeWorkspaceOverlay_ContentSourceAddsNewEntry(t *testing.T) {
	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Claude: config.OverlayClaudeConfig{
			Content: config.OverlayContentConfig{
				Repos: map[string]config.OverlayContentRepoConfig{
					"repo-new": {Source: "repos/repo-new.md"},
				},
			},
		},
	}
	merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entry, ok := merged.Claude.Content.Repos["repo-new"]
	if !ok {
		t.Fatal("repo-new not found in merged content repos")
	}
	if entry.Source != "repos/repo-new.md" {
		t.Errorf("repo-new source = %q, want repos/repo-new.md", entry.Source)
	}
	if entry.OverlaySource != "" {
		t.Errorf("repo-new OverlaySource should be empty, got %q", entry.OverlaySource)
	}
}

// TestMergeWorkspaceOverlay_ContentSourceOnBaseRepoIsError verifies that a
// content overlay entry with source= on a repo already in the base config
// returns an error (R13). Overlays must use overlay= to append to base-config repos.
func TestMergeWorkspaceOverlay_ContentSourceOnBaseRepoIsError(t *testing.T) {
	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Claude: config.OverlayClaudeConfig{
			Content: config.OverlayContentConfig{
				Repos: map[string]config.OverlayContentRepoConfig{
					"repo-a": {Source: "repos/overlay-repo-a.md"}, // illegal: repo-a is in base
				},
			},
		},
	}
	_, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err == nil {
		t.Fatal("expected error when overlay uses source= on a base-config repo, got nil")
	}
	if !strings.Contains(err.Error(), "source=") || !strings.Contains(err.Error(), "repo-a") {
		t.Errorf("error message should mention source= and repo-a, got: %v", err)
	}
}

// TestMergeWorkspaceOverlay_ContentOverlaySetsOverlaySource verifies that a
// content overlay entry with overlay= sets OverlaySource on the existing base entry.
func TestMergeWorkspaceOverlay_ContentOverlaySetsOverlaySource(t *testing.T) {
	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Claude: config.OverlayClaudeConfig{
			Content: config.OverlayContentConfig{
				Repos: map[string]config.OverlayContentRepoConfig{
					"repo-a": {Overlay: "overlay/repo-a-extra.md"},
				},
			},
		},
	}
	merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entry := merged.Claude.Content.Repos["repo-a"]
	if entry.OverlaySource != "overlay/repo-a-extra.md" {
		t.Errorf("repo-a OverlaySource = %q, want overlay/repo-a-extra.md", entry.OverlaySource)
	}
	// Base source must be preserved.
	if entry.Source != "repos/repo-a.md" {
		t.Errorf("repo-a Source = %q, want repos/repo-a.md", entry.Source)
	}
}

// TestMergeWorkspaceOverlay_ContentOverlayMissingBaseReturnsError verifies that
// an overlay content entry with overlay= for a repo not in the base returns an error.
func TestMergeWorkspaceOverlay_ContentOverlayMissingBaseReturnsError(t *testing.T) {
	ws := baseWS()
	overlay := &config.WorkspaceOverlay{
		Claude: config.OverlayClaudeConfig{
			Content: config.OverlayContentConfig{
				Repos: map[string]config.OverlayContentRepoConfig{
					"no-such-repo": {Overlay: "overlay/no-such.md"},
				},
			},
		},
	}
	_, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err == nil {
		t.Fatal("expected error for overlay= on repo not in base, got nil")
	}
	if !strings.Contains(err.Error(), "no-such-repo") {
		t.Errorf("error %q does not mention no-such-repo", err.Error())
	}
}

// TestMergeWorkspaceOverlay_RepoOverrideDeepCopy verifies that mutating a hook
// list on the merged config's repo override does not corrupt the original
// WorkspaceConfig. This guards against the shallow-copy bug where
// RepoOverride.Claude pointer was shared between the original and merged config.
func TestMergeWorkspaceOverlay_RepoOverrideDeepCopy(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Repos: map[string]config.RepoOverride{
			"repo-a": {
				Claude: &config.ClaudeOverride{
					Hooks: config.HooksConfig{
						"pre_tool_use": {{Scripts: []string{"original.sh"}}},
					},
				},
			},
		},
	}
	overlay := &config.WorkspaceOverlay{}

	merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mutate the hook list on the merged result's repo override.
	mergedOverride := merged.Repos["repo-a"]
	mergedOverride.Claude.Hooks["pre_tool_use"] = append(
		mergedOverride.Claude.Hooks["pre_tool_use"],
		config.HookEntry{Scripts: []string{"injected.sh"}},
	)
	merged.Repos["repo-a"] = mergedOverride

	// The original ws must be unchanged.
	origHooks := ws.Repos["repo-a"].Claude.Hooks["pre_tool_use"]
	if len(origHooks) != 1 {
		t.Errorf("original ws repo-a hooks mutated: got %d entries, want 1", len(origHooks))
	}
	if origHooks[0].Scripts[0] != "original.sh" {
		t.Errorf("original ws repo-a hook script = %q, want original.sh", origHooks[0].Scripts[0])
	}
}
