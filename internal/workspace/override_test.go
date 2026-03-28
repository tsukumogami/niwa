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
			"myrepo": {Claude: boolPtr(true)},
		},
	}
	if !ClaudeEnabled(ws, "myrepo") {
		t.Error("claude = true should return true")
	}
}

func TestClaudeEnabledFalse(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"myrepo": {Claude: boolPtr(false)},
		},
	}
	if ClaudeEnabled(ws, "myrepo") {
		t.Error("claude = false should return false")
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
		Hooks:    map[string]any{"pre_tool_use": []any{"a.sh"}},
		Settings: map[string]any{"permissions": "bypass"},
		Env:      map[string]any{"files": []any{"ws.env"}},
	}
	eff := MergeOverrides(ws, "unknown-repo")

	// Should return copies of workspace values.
	if len(eff.Hooks) != 1 {
		t.Errorf("expected 1 hook key, got %d", len(eff.Hooks))
	}
	if eff.Settings["permissions"] != "bypass" {
		t.Errorf("expected permissions=bypass, got %v", eff.Settings["permissions"])
	}
}

func TestMergeOverridesSettingsWin(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Settings: map[string]any{"permissions": "bypass", "keep": "yes"},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Settings: map[string]any{"permissions": "ask"},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Settings["permissions"] != "ask" {
		t.Errorf("repo setting should win, got %v", eff.Settings["permissions"])
	}
	if eff.Settings["keep"] != "yes" {
		t.Errorf("workspace setting should be preserved, got %v", eff.Settings["keep"])
	}
}

func TestMergeOverridesEnvFilesAppend(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Env: map[string]any{"files": []any{"ws.env"}},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Env: map[string]any{"files": []any{"repo.env"}},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	files, ok := eff.Env["files"].([]any)
	if !ok {
		t.Fatalf("expected files to be []any, got %T", eff.Env["files"])
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
	if files[0] != "ws.env" || files[1] != "repo.env" {
		t.Errorf("expected [ws.env repo.env], got %v", files)
	}
}

func TestMergeOverridesEnvNonFilesWin(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Env: map[string]any{"mode": "development"},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Env: map[string]any{"mode": "production"},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Env["mode"] != "production" {
		t.Errorf("repo env non-files key should win, got %v", eff.Env["mode"])
	}
}

func TestMergeOverridesHooksExtend(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Hooks: map[string]any{"pre_tool_use": []any{"ws-gate.sh"}},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Hooks: map[string]any{
					"pre_tool_use": []any{"repo-gate.sh"},
					"stop":         []any{"repo-stop.sh"},
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	// pre_tool_use should be extended (concatenated).
	preToolUse, ok := eff.Hooks["pre_tool_use"].([]any)
	if !ok {
		t.Fatalf("expected pre_tool_use to be []any, got %T", eff.Hooks["pre_tool_use"])
	}
	if len(preToolUse) != 2 {
		t.Fatalf("expected 2 hooks, got %d: %v", len(preToolUse), preToolUse)
	}
	if preToolUse[0] != "ws-gate.sh" || preToolUse[1] != "repo-gate.sh" {
		t.Errorf("expected [ws-gate.sh repo-gate.sh], got %v", preToolUse)
	}

	// stop should be a new hook key from repo.
	stop, ok := eff.Hooks["stop"].([]any)
	if !ok {
		t.Fatalf("expected stop to be []any, got %T", eff.Hooks["stop"])
	}
	if len(stop) != 1 || stop[0] != "repo-stop.sh" {
		t.Errorf("expected [repo-stop.sh], got %v", stop)
	}
}

func TestMergeOverridesNilWorkspaceFields(t *testing.T) {
	ws := &config.WorkspaceConfig{
		// All nil workspace-level hooks/settings/env.
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Settings: map[string]any{"permissions": "ask"},
				Hooks:    map[string]any{"stop": []any{"stop.sh"}},
				Env:      map[string]any{"files": []any{"repo.env"}},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Settings["permissions"] != "ask" {
		t.Errorf("expected permissions=ask, got %v", eff.Settings["permissions"])
	}
	stop, ok := eff.Hooks["stop"].([]any)
	if !ok || len(stop) != 1 {
		t.Errorf("expected [stop.sh], got %v", eff.Hooks["stop"])
	}
	files, ok := eff.Env["files"].([]any)
	if !ok || len(files) != 1 {
		t.Errorf("expected [repo.env], got %v", eff.Env["files"])
	}
}

func TestWarnUnknownRepos(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"known":   {Scope: "tactical"},
			"unknown": {Claude: boolPtr(false)},
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
	wsHooks := map[string]any{"pre_tool_use": []any{"ws.sh"}}
	ws := &config.WorkspaceConfig{
		Hooks: wsHooks,
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Hooks: map[string]any{"pre_tool_use": []any{"repo.sh"}},
			},
		},
	}
	_ = MergeOverrides(ws, "myrepo")

	// The workspace hooks should not be modified.
	original, ok := ws.Hooks["pre_tool_use"].([]any)
	if !ok || len(original) != 1 || original[0] != "ws.sh" {
		t.Errorf("workspace hooks were mutated: %v", ws.Hooks["pre_tool_use"])
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
