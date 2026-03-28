package config

import (
	"fmt"
	"strings"
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
vars = { LOG_LEVEL = "debug" }

[channels.telegram]
plugin = "telegram@claude-plugins-official"
`

func TestParseMinimalConfig(t *testing.T) {
	result, err := Parse([]byte(minimalConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := result.Config

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

func TestParseVersionField(t *testing.T) {
	input := `
[workspace]
name = "test"
version = "0.1"

[[sources]]
org = "myorg"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Config.Workspace.Version != "0.1" {
		t.Errorf("workspace.version = %q, want %q", result.Config.Workspace.Version, "0.1")
	}
}

func TestParseUnknownFieldsWarning(t *testing.T) {
	input := `
[workspace]
name = "test"
version = "0.1"
future_field = "something"

[[sources]]
org = "myorg"

[groups.public]
visibility = "public"
some_new_option = true
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected warnings for unknown fields, got none")
	}

	// Check that warnings mention the unknown fields.
	found := map[string]bool{"future_field": false, "some_new_option": false}
	for _, w := range result.Warnings {
		for key := range found {
			if strings.Contains(w, key) {
				found[key] = true
			}
		}
	}
	for key, ok := range found {
		if !ok {
			t.Errorf("expected warning mentioning %q, not found in: %v", key, result.Warnings)
		}
	}
}

func TestParseNoWarningsForKnownFields(t *testing.T) {
	result, err := Parse([]byte(minimalConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("expected no warnings for known fields, got: %v", result.Warnings)
	}
}

func TestParseFullConfig(t *testing.T) {
	result, err := Parse([]byte(fullConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := result.Config

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

	// Typed sections parse correctly
	if cfg.Hooks == nil {
		t.Error("hooks should not be nil")
	}
	if len(cfg.Hooks["pre_tool_use"]) != 1 || cfg.Hooks["pre_tool_use"][0] != "hooks/gate-online.sh" {
		t.Errorf("hooks.pre_tool_use = %v, want [hooks/gate-online.sh]", cfg.Hooks["pre_tool_use"])
	}
	if len(cfg.Hooks["stop"]) != 1 || cfg.Hooks["stop"][0] != "hooks/workflow-continue.sh" {
		t.Errorf("hooks.stop = %v, want [hooks/workflow-continue.sh]", cfg.Hooks["stop"])
	}
	if cfg.Settings == nil {
		t.Error("settings should not be nil")
	}
	if cfg.Settings["permissions"] != "bypass" {
		t.Errorf("settings.permissions = %v, want bypass", cfg.Settings["permissions"])
	}
	if len(cfg.Env.Files) != 1 || cfg.Env.Files[0] != "env/workspace.env" {
		t.Errorf("env.files = %v, want [env/workspace.env]", cfg.Env.Files)
	}
	if cfg.Env.Vars["LOG_LEVEL"] != "debug" {
		t.Errorf("env.vars.LOG_LEVEL = %v, want debug", cfg.Env.Vars["LOG_LEVEL"])
	}
	if cfg.Channels == nil {
		t.Error("channels should not be nil")
	}
}

func TestParseRepoOverrideAllFields(t *testing.T) {
	input := `
[workspace]
name = "test"

[[sources]]
org = "myorg"

[repos.myapp]
url = "git@gitlab.com:custom/myapp.git"
branch = "develop"
scope = "tactical"
claude = true

[repos.myapp.hooks]
pre_tool_use = ["repo-gate.sh"]

[repos.myapp.settings]
permissions = "ask"

[repos.myapp.env]
files = ["repo.env"]
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := result.Config

	repo, ok := cfg.Repos["myapp"]
	if !ok {
		t.Fatal("repos[myapp] missing")
	}
	if repo.URL != "git@gitlab.com:custom/myapp.git" {
		t.Errorf("url = %q, want gitlab URL", repo.URL)
	}
	if repo.Branch != "develop" {
		t.Errorf("branch = %q, want develop", repo.Branch)
	}
	if repo.Scope != "tactical" {
		t.Errorf("scope = %q, want tactical", repo.Scope)
	}
	if repo.Claude == nil || *repo.Claude != true {
		t.Error("claude should be true")
	}
	if repo.Hooks == nil {
		t.Fatal("hooks should not be nil")
	}
	if len(repo.Hooks["pre_tool_use"]) != 1 || repo.Hooks["pre_tool_use"][0] != "repo-gate.sh" {
		t.Errorf("hooks.pre_tool_use = %v, want [repo-gate.sh]", repo.Hooks["pre_tool_use"])
	}
	if repo.Settings == nil {
		t.Fatal("settings should not be nil")
	}
	if repo.Settings["permissions"] != "ask" {
		t.Errorf("settings.permissions = %v, want ask", repo.Settings["permissions"])
	}
	if len(repo.Env.Files) != 1 || repo.Env.Files[0] != "repo.env" {
		t.Errorf("env.files = %v, want [repo.env]", repo.Env.Files)
	}
}

func TestValidNameAccepted(t *testing.T) {
	accepted := []string{
		"simple",
		"my-workspace",
		"my_workspace",
		"my.workspace",
		"CamelCase",
		"v1.2.3",
		".github",
		"a",
		"A-B_C.D",
	}
	for _, name := range accepted {
		t.Run(name, func(t *testing.T) {
			input := fmt.Sprintf("[workspace]\nname = %q\n", name)
			_, err := Parse([]byte(input))
			if err != nil {
				t.Errorf("name %q should be accepted, got error: %v", name, err)
			}
		})
	}
}

func TestValidNameRejected(t *testing.T) {
	rejected := []string{
		"has space",
		"has/slash",
		"has\\backslash",
		"has:colon",
		"",
	}
	for _, name := range rejected {
		t.Run(name, func(t *testing.T) {
			if name == "" {
				// Empty name is caught by the "required" check, not the regex.
				return
			}
			input := fmt.Sprintf("[workspace]\nname = %q\n", name)
			_, err := Parse([]byte(input))
			if err == nil {
				t.Errorf("name %q should be rejected", name)
			}
		})
	}
}

func TestValidateContentSourcePaths(t *testing.T) {
	// Valid content source paths should be accepted.
	validSources := []string{
		"workspace.md",
		"repos/myapp.md",
		"deep/nested/path.md",
		"file-with-dashes.md",
		"file_with_underscores.md",
	}
	for _, source := range validSources {
		t.Run("accepted_"+source, func(t *testing.T) {
			input := fmt.Sprintf(`[workspace]
name = "ok"
[content.workspace]
source = %q
`, source)
			_, err := Parse([]byte(input))
			if err != nil {
				t.Errorf("source %q should be accepted, got: %v", source, err)
			}
		})
	}
}

func TestValidateSubdirKeyAccepted(t *testing.T) {
	accepted := []string{"website", "docs/api", "nested/deep/path"}
	for _, subdir := range accepted {
		t.Run(subdir, func(t *testing.T) {
			input := fmt.Sprintf(`[workspace]
name = "ok"
[content.repos.myrepo]
source = "repos/myrepo.md"
[content.repos.myrepo.subdirs]
%q = "repos/sub.md"
`, subdir)
			_, err := Parse([]byte(input))
			if err != nil {
				t.Errorf("subdir key %q should be accepted, got: %v", subdir, err)
			}
		})
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
			name: "missing source org",
			input: `[workspace]
name = "ok"
[[sources]]
repos = ["a"]`,
			wantErr: "source org is required",
		},
		{
			name:    "invalid workspace name with spaces",
			input:   `[workspace]` + "\n" + `name = "bad name"`,
			wantErr: `workspace.name "bad name": must match`,
		},
		{
			name:    "invalid workspace name with slash",
			input:   `[workspace]` + "\n" + `name = "bad/name"`,
			wantErr: `workspace.name "bad/name": must match`,
		},
		{
			name: "invalid group name",
			input: `[workspace]
name = "ok"
[groups."bad group"]
visibility = "public"`,
			wantErr: `group name "bad group": must match`,
		},
		{
			name: "invalid repo override name",
			input: `[workspace]
name = "ok"
[repos."bad/repo"]
scope = "tactical"`,
			wantErr: `repo override name "bad/repo": must match`,
		},
		{
			name: "content source with path traversal",
			input: `[workspace]
name = "ok"
[content.workspace]
source = "../../../etc/passwd"`,
			wantErr: `path traversal (..) is not allowed`,
		},
		{
			name: "content source with absolute path",
			input: `[workspace]
name = "ok"
[content.workspace]
source = "/etc/passwd"`,
			wantErr: `absolute paths are not allowed`,
		},
		{
			name: "content group source with traversal",
			input: `[workspace]
name = "ok"
[content.groups.public]
source = "foo/../../secret.md"`,
			wantErr: `path traversal (..) is not allowed`,
		},
		{
			name: "content repo source with traversal",
			input: `[workspace]
name = "ok"
[content.repos.myrepo]
source = "../secret.md"`,
			wantErr: `path traversal (..) is not allowed`,
		},
		{
			name: "subdir source with traversal",
			input: `[workspace]
name = "ok"
[content.repos.myrepo]
source = "repos/myrepo.md"
[content.repos.myrepo.subdirs]
web = "../escape.md"`,
			wantErr: `path traversal (..) is not allowed`,
		},
		{
			name: "subdir key escapes repo",
			input: `[workspace]
name = "ok"
[content.repos.myrepo]
source = "repos/myrepo.md"
[content.repos.myrepo.subdirs]
"../../escape" = "valid-source.md"`,
			wantErr: `must resolve within the repo directory`,
		},
		{
			name: "subdir key absolute path",
			input: `[workspace]
name = "ok"
[content.repos.myrepo]
source = "repos/myrepo.md"
[content.repos.myrepo.subdirs]
"/etc" = "valid-source.md"`,
			wantErr: `absolute paths are not allowed`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}
