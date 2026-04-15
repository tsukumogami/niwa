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

[claude.content.workspace]
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

[repos.".github".claude]
enabled = false

[repos.vision]
scope = "strategic"

[claude.content.workspace]
source = "workspace.md"

[claude.content.groups.public]
source = "public.md"

[claude.content.repos.tsuku]
source = "repos/tsuku.md"

  [claude.content.repos.tsuku.subdirs]
  recipes = "repos/tsuku-recipes.md"

[claude]
marketplaces = ["tsukumogami/shirabe", "repo:tools/.claude-plugin/marketplace.json"]
plugins = ["shirabe@shirabe", "tsukumogami@tsukumogami"]

[[claude.hooks.pre_tool_use]]
scripts = ["hooks/gate-online.sh"]

[[claude.hooks.stop]]
scripts = ["hooks/workflow-continue.sh"]

[claude.settings]
permissions = "bypass"

[claude.env]
promote = ["GH_TOKEN"]
vars = { EXTRA_FLAG = "claude-only" }

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

	if cfg.Claude.Content.Workspace.Source != "workspace.md" {
		t.Errorf("content.workspace.source = %q, want %q", cfg.Claude.Content.Workspace.Source, "workspace.md")
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
	if ghRepo.Claude == nil || ghRepo.Claude.Enabled == nil || *ghRepo.Claude.Enabled != false {
		t.Error("repos[.github].claude.enabled should be false")
	}

	vision, ok := cfg.Repos["vision"]
	if !ok {
		t.Fatal("repos[vision] missing")
	}
	if vision.Scope != "strategic" {
		t.Errorf("repos[vision].scope = %q, want %q", vision.Scope, "strategic")
	}

	// Content
	if cfg.Claude.Content.Repos["tsuku"].Source != "repos/tsuku.md" {
		t.Errorf("content.repos.tsuku.source = %q, want %q", cfg.Claude.Content.Repos["tsuku"].Source, "repos/tsuku.md")
	}
	if cfg.Claude.Content.Repos["tsuku"].Subdirs["recipes"] != "repos/tsuku-recipes.md" {
		t.Errorf("content.repos.tsuku.subdirs.recipes = %q, want %q",
			cfg.Claude.Content.Repos["tsuku"].Subdirs["recipes"], "repos/tsuku-recipes.md")
	}

	// Typed sections parse correctly
	// Marketplaces and plugins
	if len(cfg.Claude.Marketplaces) != 2 {
		t.Fatalf("claude.marketplaces count = %d, want 2", len(cfg.Claude.Marketplaces))
	}
	if cfg.Claude.Marketplaces[0] != "tsukumogami/shirabe" {
		t.Errorf("claude.marketplaces[0] = %q, want tsukumogami/shirabe", cfg.Claude.Marketplaces[0])
	}
	if cfg.Claude.Plugins == nil {
		t.Fatal("claude.plugins should not be nil")
	}
	if len(*cfg.Claude.Plugins) != 2 || (*cfg.Claude.Plugins)[0] != "shirabe@shirabe" {
		t.Errorf("claude.plugins = %v, want [shirabe@shirabe tsukumogami@tsukumogami]", *cfg.Claude.Plugins)
	}

	if cfg.Claude.Hooks == nil {
		t.Error("claude.hooks should not be nil")
	}
	if len(cfg.Claude.Hooks["pre_tool_use"]) != 1 || cfg.Claude.Hooks["pre_tool_use"][0].Scripts[0] != "hooks/gate-online.sh" {
		t.Errorf("claude.hooks.pre_tool_use = %v, want [{scripts: [hooks/gate-online.sh]}]", cfg.Claude.Hooks["pre_tool_use"])
	}
	if len(cfg.Claude.Hooks["stop"]) != 1 || cfg.Claude.Hooks["stop"][0].Scripts[0] != "hooks/workflow-continue.sh" {
		t.Errorf("claude.hooks.stop = %v, want [{scripts: [hooks/workflow-continue.sh]}]", cfg.Claude.Hooks["stop"])
	}
	if cfg.Claude.Settings == nil {
		t.Error("claude.settings should not be nil")
	}
	if cfg.Claude.Settings["permissions"] != "bypass" {
		t.Errorf("claude.settings.permissions = %v, want bypass", cfg.Claude.Settings["permissions"])
	}
	if len(cfg.Env.Files) != 1 || cfg.Env.Files[0] != "env/workspace.env" {
		t.Errorf("env.files = %v, want [env/workspace.env]", cfg.Env.Files)
	}
	if cfg.Env.Vars["LOG_LEVEL"] != "debug" {
		t.Errorf("env.vars.LOG_LEVEL = %v, want debug", cfg.Env.Vars["LOG_LEVEL"])
	}
	if len(cfg.Claude.Env.Promote) != 1 || cfg.Claude.Env.Promote[0] != "GH_TOKEN" {
		t.Errorf("claude.env.promote = %v, want [GH_TOKEN]", cfg.Claude.Env.Promote)
	}
	if cfg.Claude.Env.Vars["EXTRA_FLAG"] != "claude-only" {
		t.Errorf("claude.env.vars.EXTRA_FLAG = %v, want claude-only", cfg.Claude.Env.Vars["EXTRA_FLAG"])
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

[repos.myapp.claude]
enabled = true

[[repos.myapp.claude.hooks.pre_tool_use]]
scripts = ["repo-gate.sh"]

[repos.myapp.claude.settings]
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
	if repo.Claude == nil || repo.Claude.Enabled == nil || *repo.Claude.Enabled != true {
		t.Error("claude.enabled should be true")
	}
	if repo.Claude.Hooks == nil {
		t.Fatal("claude.hooks should not be nil")
	}
	if len(repo.Claude.Hooks["pre_tool_use"]) != 1 || repo.Claude.Hooks["pre_tool_use"][0].Scripts[0] != "repo-gate.sh" {
		t.Errorf("claude.hooks.pre_tool_use = %v, want [{scripts: [repo-gate.sh]}]", repo.Claude.Hooks["pre_tool_use"])
	}
	if repo.Claude.Settings == nil {
		t.Fatal("claude.settings should not be nil")
	}
	if repo.Claude.Settings["permissions"] != "ask" {
		t.Errorf("claude.settings.permissions = %v, want ask", repo.Claude.Settings["permissions"])
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
[claude.content.workspace]
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
[claude.content.repos.myrepo]
source = "repos/myrepo.md"
[claude.content.repos.myrepo.subdirs]
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
[claude.content.workspace]
source = "../../../etc/passwd"`,
			wantErr: `path traversal (..) is not allowed`,
		},
		{
			name: "content source with absolute path",
			input: `[workspace]
name = "ok"
[claude.content.workspace]
source = "/etc/passwd"`,
			wantErr: `absolute paths are not allowed`,
		},
		{
			name: "content group source with traversal",
			input: `[workspace]
name = "ok"
[claude.content.groups.public]
source = "foo/../../secret.md"`,
			wantErr: `path traversal (..) is not allowed`,
		},
		{
			name: "content repo source with traversal",
			input: `[workspace]
name = "ok"
[claude.content.repos.myrepo]
source = "../secret.md"`,
			wantErr: `path traversal (..) is not allowed`,
		},
		{
			name: "subdir source with traversal",
			input: `[workspace]
name = "ok"
[claude.content.repos.myrepo]
source = "repos/myrepo.md"
[claude.content.repos.myrepo.subdirs]
web = "../escape.md"`,
			wantErr: `path traversal (..) is not allowed`,
		},
		{
			name: "subdir key escapes repo",
			input: `[workspace]
name = "ok"
[claude.content.repos.myrepo]
source = "repos/myrepo.md"
[claude.content.repos.myrepo.subdirs]
"../../escape" = "valid-source.md"`,
			wantErr: `must resolve within the repo directory`,
		},
		{
			name: "subdir key absolute path",
			input: `[workspace]
name = "ok"
[claude.content.repos.myrepo]
source = "repos/myrepo.md"
[claude.content.repos.myrepo.subdirs]
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

func TestParseGlobalConfigOverrideRoundTrip(t *testing.T) {
	input := `
[global.claude.settings]
permissions = "bypass"

[[global.claude.hooks.pre_tool_use]]
scripts = ["hooks/gate.sh"]

[global.env]
files = ["shared.env"]
vars = { LANG = "en_US.UTF-8" }

[global.files]
"secrets/api.env" = "config/"

[workspaces.my-ws.env.vars]
MY_TOKEN = "abc"
`
	cfg, err := ParseGlobalConfigOverride([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Global.Claude == nil {
		t.Fatal("global.claude should not be nil")
	}
	if cfg.Global.Claude.Settings["permissions"] != "bypass" {
		t.Errorf("global.claude.settings.permissions = %q, want bypass", cfg.Global.Claude.Settings["permissions"])
	}
	hooks := cfg.Global.Claude.Hooks["pre_tool_use"]
	if len(hooks) == 0 || len(hooks[0].Scripts) == 0 || hooks[0].Scripts[0] != "hooks/gate.sh" {
		t.Errorf("global.claude.hooks.pre_tool_use[0].scripts[0] = %v, want hooks/gate.sh", hooks)
	}
	if len(cfg.Global.Env.Files) != 1 || cfg.Global.Env.Files[0] != "shared.env" {
		t.Errorf("global.env.files = %v, want [shared.env]", cfg.Global.Env.Files)
	}
	if cfg.Global.Env.Vars["LANG"] != "en_US.UTF-8" {
		t.Errorf("global.env.vars.LANG = %q, want en_US.UTF-8", cfg.Global.Env.Vars["LANG"])
	}
	if cfg.Global.Files["secrets/api.env"] != "config/" {
		t.Errorf("global.files[secrets/api.env] = %q, want config/", cfg.Global.Files["secrets/api.env"])
	}
	ws, ok := cfg.Workspaces["my-ws"]
	if !ok {
		t.Fatal("workspaces.my-ws missing")
	}
	if ws.Env.Vars["MY_TOKEN"] != "abc" {
		t.Errorf("workspaces.my-ws.env.vars.MY_TOKEN = %q, want abc", ws.Env.Vars["MY_TOKEN"])
	}
}

func TestParseGlobalConfigOverrideRejectsAbsoluteFileDest(t *testing.T) {
	input := `
[global.files]
"source.env" = "/etc/secrets"
`
	_, err := ParseGlobalConfigOverride([]byte(input))
	if err == nil {
		t.Fatal("expected error for absolute Files destination, got nil")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error %q should mention absolute", err.Error())
	}
}

func TestParseGlobalConfigOverrideRejectsTraversalFileDest(t *testing.T) {
	input := `
[global.files]
"source.env" = "../../etc/secrets"
`
	_, err := ParseGlobalConfigOverride([]byte(input))
	if err == nil {
		t.Fatal("expected error for .. Files destination, got nil")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("error %q should mention traversal", err.Error())
	}
}

func TestParseGlobalConfigOverrideRejectsAbsoluteEnvFilesSrc(t *testing.T) {
	input := `
[global.env]
files = ["/home/user/secrets.env"]
`
	_, err := ParseGlobalConfigOverride([]byte(input))
	if err == nil {
		t.Fatal("expected error for absolute Env.Files source, got nil")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error %q should mention absolute", err.Error())
	}
}

func TestParseGlobalConfigOverrideRejectsTraversalEnvFilesSrc(t *testing.T) {
	input := `
[global.env]
files = ["../secrets.env"]
`
	_, err := ParseGlobalConfigOverride([]byte(input))
	if err == nil {
		t.Fatal("expected error for .. Env.Files source, got nil")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("error %q should mention traversal", err.Error())
	}
}

// TestParseRejectsContentAtRepoOverride is the Issue 1 proof that the type
// split enforces workspace-scoped-only content at the type level: the TOML
// decoder surfaces [repos.<name>.claude.content] as an unknown-config-field
// warning because RepoOverride.Claude is *ClaudeOverride (narrower), which
// has no Content field.
func TestParseRejectsContentAtRepoOverride(t *testing.T) {
	input := `
[workspace]
name = "test-ws"

[repos.tsuku]
url = "https://github.com/example/tsuku"

[repos.tsuku.claude.content]
workspace = { source = "should-not-work.md" }
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "repos") && strings.Contains(w, "claude") && strings.Contains(w, "content") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an unknown-config-field warning for repos.tsuku.claude.content, got warnings: %v", result.Warnings)
	}
}

// TestParseAcceptsContentOnInstanceOverrideIsRejected proves the same for
// [instance.claude.content].
func TestParseRejectsContentAtInstanceOverride(t *testing.T) {
	input := `
[workspace]
name = "test-ws"

[instance.claude.content]
workspace = { source = "should-not-work.md" }
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "instance") && strings.Contains(w, "claude") && strings.Contains(w, "content") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an unknown-config-field warning for instance.claude.content, got warnings: %v", result.Warnings)
	}
}

// TestParseDeprecatedContentMigrates proves that a workspace.toml using the
// legacy [content] path still parses cleanly, emits exactly one deprecation
// warning, and its content ends up under cfg.Claude.Content so downstream
// consumers see the canonical location.
func TestParseDeprecatedContentMigrates(t *testing.T) {
	input := `
[workspace]
name = "test-ws"

[content.workspace]
source = "workspace.md"

[content.groups.public]
source = "groups/public.md"

[content.repos.myrepo]
source = "repos/myrepo.md"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// Canonical location must be populated.
	if result.Config.Claude.Content.Workspace.Source != "workspace.md" {
		t.Errorf("claude.content.workspace.source = %q, want %q",
			result.Config.Claude.Content.Workspace.Source, "workspace.md")
	}
	if result.Config.Claude.Content.Groups["public"].Source != "groups/public.md" {
		t.Errorf("claude.content.groups.public.source = %q, want %q",
			result.Config.Claude.Content.Groups["public"].Source, "groups/public.md")
	}
	if result.Config.Claude.Content.Repos["myrepo"].Source != "repos/myrepo.md" {
		t.Errorf("claude.content.repos.myrepo.source = %q, want %q",
			result.Config.Claude.Content.Repos["myrepo"].Source, "repos/myrepo.md")
	}

	// Legacy location must be cleared after migration.
	if !isContentConfigZero(result.Config.Content) {
		t.Errorf("expected cfg.Content to be zero after migration, got %+v", result.Config.Content)
	}

	// Exactly one deprecation warning must be emitted.
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "[content] is deprecated") && strings.Contains(w, "[claude.content]") {
			if found {
				t.Errorf("expected exactly one deprecation warning, got multiple: %v", result.Warnings)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("expected deprecation warning for [content], got warnings: %v", result.Warnings)
	}
}

// TestParseRejectsBothContentForms proves that using both [content] and
// [claude.content] in the same workspace.toml is a hard error.
func TestParseRejectsBothContentForms(t *testing.T) {
	input := `
[workspace]
name = "test-ws"

[content.workspace]
source = "old.md"

[claude.content.workspace]
source = "new.md"
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error when both [content] and [claude.content] are set, got nil")
	}
	if !strings.Contains(err.Error(), "[content]") || !strings.Contains(err.Error(), "[claude.content]") {
		t.Errorf("error %q should mention both [content] and [claude.content]", err.Error())
	}
}

// TestParseCanonicalContentHasNoWarning is the happy path: a workspace.toml
// that only uses [claude.content] should parse clean, with no deprecation
// warning and with content in the expected location.
func TestParseCanonicalContentHasNoWarning(t *testing.T) {
	input := `
[workspace]
name = "test-ws"

[claude.content.workspace]
source = "workspace.md"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if result.Config.Claude.Content.Workspace.Source != "workspace.md" {
		t.Errorf("claude.content.workspace.source = %q, want %q",
			result.Config.Claude.Content.Workspace.Source, "workspace.md")
	}
	for _, w := range result.Warnings {
		if strings.Contains(w, "deprecated") {
			t.Errorf("expected no deprecation warning for canonical form, got: %v", result.Warnings)
		}
	}
}
