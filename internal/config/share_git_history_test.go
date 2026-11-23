package config

import (
	"fmt"
	"testing"
)

func TestShareGitHistory_Parses(t *testing.T) {
	body := `
[workspace]
name = "ws"
share_git_history = false

[[sources]]
org = "acme"

[repos.app]
share_git_history = true
`
	res, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ws := res.Config.Workspace.ShareGitHistory
	if ws == nil || *ws {
		t.Fatalf("WorkspaceMeta.ShareGitHistory = %s, want false", pointerState(ws))
	}
	repo := res.Config.Repos["app"].ShareGitHistory
	if repo == nil || !*repo {
		t.Fatalf("RepoOverride.ShareGitHistory = %s, want true", pointerState(repo))
	}
}

func TestShareGitHistory_AbsentParsesAsNil(t *testing.T) {
	body := `
[workspace]
name = "ws"

[[sources]]
org = "acme"

[repos.app]
branch = "main"
`
	res, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := res.Config.Workspace.ShareGitHistory; got != nil {
		t.Fatalf("WorkspaceMeta.ShareGitHistory = %s, want nil", pointerState(got))
	}
	if got := res.Config.Repos["app"].ShareGitHistory; got != nil {
		t.Fatalf("RepoOverride.ShareGitHistory = %s, want nil", pointerState(got))
	}
}

func TestShareGitHistoryEnabled_AllNineCombinations(t *testing.T) {
	cases := []struct {
		repo, ws *bool
		want     bool
	}{
		{nil, nil, true},
		{nil, boolPtr(true), true},
		{nil, boolPtr(false), false},
		{boolPtr(true), nil, true},
		{boolPtr(true), boolPtr(true), true},
		{boolPtr(true), boolPtr(false), true}, // the repo's on beats the workspace's off
		{boolPtr(false), nil, false},
		{boolPtr(false), boolPtr(true), false}, // the repo's off beats the workspace's on
		{boolPtr(false), boolPtr(false), false},
	}
	if len(cases) != 9 {
		t.Fatalf("%d cases, want all nine combinations of unset, true and false", len(cases))
	}
	for _, tc := range cases {
		name := fmt.Sprintf("repo=%s/workspace=%s", pointerState(tc.repo), pointerState(tc.ws))
		t.Run(name, func(t *testing.T) {
			cfg := &WorkspaceConfig{
				Workspace: WorkspaceMeta{Name: "ws", ShareGitHistory: tc.ws},
				Repos:     map[string]RepoOverride{"app": {ShareGitHistory: tc.repo}},
			}
			if got := cfg.ShareGitHistoryEnabled("app"); got != tc.want {
				t.Fatalf("ShareGitHistoryEnabled(app) = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestShareGitHistoryEnabled_UnnamedRepoAndNilConfig(t *testing.T) {
	cfg := &WorkspaceConfig{
		Workspace: WorkspaceMeta{Name: "ws", ShareGitHistory: boolPtr(false)},
		Repos:     map[string]RepoOverride{"app": {ShareGitHistory: boolPtr(true)}},
	}
	if cfg.ShareGitHistoryEnabled("other") {
		t.Fatal("a repo the config doesn't name should take the workspace's false")
	}
	var none *WorkspaceConfig
	if !none.ShareGitHistoryEnabled("app") {
		t.Fatal("a nil config sets nothing, so the cascade's default, true, applies")
	}
}
