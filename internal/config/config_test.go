package config

import (
	"testing"
)

const minimalConfig = `
[workspace]
name = "test-ws"

[[sources]]
org = "myorg"

[groups.public]
visibility = "public"

[groups.private]
visibility = "private"

[content.workspace]
source = "workspace.md"
`

const fullConfig = `
[workspace]
name = "tsuku"
default_branch = "main"
content_dir = "claude"

[[sources]]
org = "tsukumogami"

[[sources]]
org = "large-org"
max_repos = 30
repos = ["repo-a", "repo-b"]

[groups.public]
visibility = "public"

[groups.private]
visibility = "private"

[groups.infra]
repos = ["terraform-modules", "deploy-scripts"]

[repos.".github"]
claude = false

[repos.vision]
scope = "strategic"

[content.workspace]
source = "workspace.md"

[content.groups.public]
source = "public.md"

[content.repos.tsuku]
source = "repos/tsuku.md"

  [content.repos.tsuku.subdirs]
  recipes = "repos/tsuku-recipes.md"

[hooks]
pre_tool_use = ["hooks/gate-online.sh"]
stop = ["hooks/workflow-continue.sh"]

[settings]
permissions = "bypass"

[env]
files = ["env/workspace.env"]

[channels.telegram]
plugin = "telegram@claude-plugins-official"
`

func TestParseMinimalConfig(t *testing.T) {
	cfg, err := Parse([]byte(minimalConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Workspace.Name != "test-ws" {
		t.Errorf("workspace.name = %q, want %q", cfg.Workspace.Name, "test-ws")
	}

	if len(cfg.Sources) != 1 {
		t.Fatalf("sources count = %d, want 1", len(cfg.Sources))
	}
	if cfg.Sources[0].Org != "myorg" {
		t.Errorf("source org = %q, want %q", cfg.Sources[0].Org, "myorg")
	}

	if len(cfg.Groups) != 2 {
		t.Fatalf("groups count = %d, want 2", len(cfg.Groups))
	}
	if cfg.Groups["public"].Visibility != "public" {
		t.Errorf("groups.public.visibility = %q, want %q", cfg.Groups["public"].Visibility, "public")
	}
	if cfg.Groups["private"].Visibility != "private" {
		t.Errorf("groups.private.visibility = %q, want %q", cfg.Groups["private"].Visibility, "private")
	}

	if cfg.Content.Workspace.Source != "workspace.md" {
		t.Errorf("content.workspace.source = %q, want %q", cfg.Content.Workspace.Source, "workspace.md")
	}
}

func TestParseFullConfig(t *testing.T) {
	cfg, err := Parse([]byte(fullConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Workspace.Name != "tsuku" {
		t.Errorf("workspace.name = %q, want %q", cfg.Workspace.Name, "tsuku")
	}
	if cfg.Workspace.DefaultBranch != "main" {
		t.Errorf("default_branch = %q, want %q", cfg.Workspace.DefaultBranch, "main")
	}
	if cfg.Workspace.ContentDir != "claude" {
		t.Errorf("content_dir = %q, want %q", cfg.Workspace.ContentDir, "claude")
	}

	// Sources
	if len(cfg.Sources) != 2 {
		t.Fatalf("sources count = %d, want 2", len(cfg.Sources))
	}
	if cfg.Sources[1].MaxRepos != 30 {
		t.Errorf("source[1].max_repos = %d, want 30", cfg.Sources[1].MaxRepos)
	}
	if len(cfg.Sources[1].Repos) != 2 {
		t.Errorf("source[1].repos count = %d, want 2", len(cfg.Sources[1].Repos))
	}

	// Groups with explicit repos
	infra, ok := cfg.Groups["infra"]
	if !ok {
		t.Fatal("groups.infra missing")
	}
	if len(infra.Repos) != 2 {
		t.Errorf("groups.infra.repos count = %d, want 2", len(infra.Repos))
	}

	// Repo overrides
	ghRepo, ok := cfg.Repos[".github"]
	if !ok {
		t.Fatal("repos[.github] missing")
	}
	if ghRepo.Claude == nil || *ghRepo.Claude != false {
		t.Error("repos[.github].claude should be false")
	}

	vision, ok := cfg.Repos["vision"]
	if !ok {
		t.Fatal("repos[vision] missing")
	}
	if vision.Scope != "strategic" {
		t.Errorf("repos[vision].scope = %q, want %q", vision.Scope, "strategic")
	}

	// Content
	if cfg.Content.Repos["tsuku"].Source != "repos/tsuku.md" {
		t.Errorf("content.repos.tsuku.source = %q, want %q", cfg.Content.Repos["tsuku"].Source, "repos/tsuku.md")
	}
	if cfg.Content.Repos["tsuku"].Subdirs["recipes"] != "repos/tsuku-recipes.md" {
		t.Errorf("content.repos.tsuku.subdirs.recipes = %q, want %q",
			cfg.Content.Repos["tsuku"].Subdirs["recipes"], "repos/tsuku-recipes.md")
	}

	// Placeholder sections parse without error
	if cfg.Hooks == nil {
		t.Error("hooks should not be nil")
	}
	if cfg.Settings == nil {
		t.Error("settings should not be nil")
	}
	if cfg.Env == nil {
		t.Error("env should not be nil")
	}
	if cfg.Channels == nil {
		t.Error("channels should not be nil")
	}
}

func TestParseValidation(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:    "missing workspace name",
			input:   `[workspace]`,
			wantErr: "workspace.name is required",
		},
		{
			name: "invalid workspace name",
			input: `[workspace]
name = "bad/name"`,
			wantErr: "contains invalid characters",
		},
		{
			name: "invalid group name",
			input: `[workspace]
name = "ok"
[groups."../../etc"]
visibility = "public"`,
			wantErr: "contains invalid characters",
		},
		{
			name: "missing source org",
			input: `[workspace]
name = "ok"
[[sources]]
repos = ["a"]`,
			wantErr: "source org is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
