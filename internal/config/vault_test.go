package config

import (
	"strings"
	"testing"
)

// TestParseAnonymousVaultProvider exercises the singular
// [vault.provider] shape that the PRD calls "anonymous". The registry
// must surface it via Provider (non-nil) and leave Providers empty.
func TestParseAnonymousVaultProvider(t *testing.T) {
	input := `
[workspace]
name = "test-ws"

[vault.provider]
kind = "fake"
path = "fixtures/vault"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	v := result.Config.Vault
	if v == nil {
		t.Fatal("cfg.Vault should not be nil")
	}
	if v.Provider == nil {
		t.Fatal("cfg.Vault.Provider should be non-nil for [vault.provider]")
	}
	if v.Provider.Kind != "fake" {
		t.Errorf("Provider.Kind = %q, want fake", v.Provider.Kind)
	}
	if got := v.Provider.Config["path"]; got != "fixtures/vault" {
		t.Errorf("Provider.Config[path] = %v, want fixtures/vault", got)
	}
	if len(v.Providers) != 0 {
		t.Errorf("Providers should be empty when anonymous form is used, got %v", v.Providers)
	}
}

// TestParseSingleNamedVaultProvider covers a single [vault.providers.<name>]
// entry. Named form is what the named-registry code path tests build on.
func TestParseSingleNamedVaultProvider(t *testing.T) {
	input := `
[workspace]
name = "test-ws"

[vault.providers.team]
kind = "infisical"
project_id = "abc123"
env = "prod"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	v := result.Config.Vault
	if v == nil {
		t.Fatal("cfg.Vault should not be nil")
	}
	if v.Provider != nil {
		t.Errorf("Provider should be nil for named shape, got %+v", v.Provider)
	}
	team, ok := v.Providers["team"]
	if !ok {
		t.Fatal("providers[team] missing")
	}
	if team.Kind != "infisical" {
		t.Errorf("Kind = %q, want infisical", team.Kind)
	}
	if got := team.Config["project_id"]; got != "abc123" {
		t.Errorf("project_id = %v, want abc123", got)
	}
	if got := team.Config["env"]; got != "prod" {
		t.Errorf("env = %v, want prod", got)
	}
}

// TestParseMultipleNamedVaultProviders verifies any count of named
// providers parse cleanly.
func TestParseMultipleNamedVaultProviders(t *testing.T) {
	input := `
[workspace]
name = "test-ws"

[vault.providers.team]
kind = "infisical"
project_id = "abc"

[vault.providers.personal]
kind = "sops"
key_path = "keys/personal.age"

[vault.providers.shared]
kind = "fake"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	v := result.Config.Vault
	if len(v.Providers) != 3 {
		t.Fatalf("Providers count = %d, want 3", len(v.Providers))
	}
	kinds := map[string]string{
		"team":     "infisical",
		"personal": "sops",
		"shared":   "fake",
	}
	for name, kind := range kinds {
		if v.Providers[name].Kind != kind {
			t.Errorf("providers.%s.Kind = %q, want %q", name, v.Providers[name].Kind, kind)
		}
	}
}

// TestParseRejectsMixedVaultShapes is the core shape guard: declaring
// both the anonymous and the named form is not allowed.
func TestParseRejectsMixedVaultShapes(t *testing.T) {
	input := `
[workspace]
name = "test-ws"

[vault.provider]
kind = "fake"

[vault.providers.extra]
kind = "sops"
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error when both [vault.provider] and [vault.providers.*] are set, got nil")
	}
	if !strings.Contains(err.Error(), "[vault.provider]") || !strings.Contains(err.Error(), "[vault.providers.*]") {
		t.Errorf("error should mention both shapes; got: %v", err)
	}
}

func TestParseRejectsProviderWithoutKind(t *testing.T) {
	input := `
[workspace]
name = "test-ws"

[vault.providers.team]
project_id = "abc"
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error when provider has no kind")
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Errorf("error should mention kind; got %v", err)
	}
}

func TestParseVaultTeamOnly(t *testing.T) {
	input := `
[workspace]
name = "test-ws"

[vault.providers.team]
kind = "fake"

[vault]
team_only = ["GITHUB_TOKEN", "SENTRY_DSN"]
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := result.Config.Vault.TeamOnly; len(got) != 2 || got[0] != "GITHUB_TOKEN" || got[1] != "SENTRY_DSN" {
		t.Errorf("TeamOnly = %v, want [GITHUB_TOKEN SENTRY_DSN]", got)
	}
}

func TestParseWorkspaceVaultScope(t *testing.T) {
	input := `
[workspace]
name = "tsukumogami"
vault_scope = "custom-scope"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := result.Config.Workspace.VaultScope; got != "custom-scope" {
		t.Errorf("VaultScope = %q, want custom-scope", got)
	}
}

// TestParseEnvVarsAndSecretsSplit covers the R33 sibling-split with
// values in both tables.
func TestParseEnvVarsAndSecretsSplit(t *testing.T) {
	input := `
[workspace]
name = "test-ws"

[env.vars]
LOG_LEVEL = "debug"
MODE = "prod"

[env.secrets]
GITHUB_TOKEN = "vault://team/GITHUB_TOKEN"
API_KEY = "literal-fallback"

[vault.providers.team]
kind = "fake"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	env := result.Config.Env
	if got := env.Vars.Values["LOG_LEVEL"].Plain; got != "debug" {
		t.Errorf("env.vars.LOG_LEVEL.Plain = %q, want debug", got)
	}
	if got := env.Vars.Values["MODE"].Plain; got != "prod" {
		t.Errorf("env.vars.MODE.Plain = %q, want prod", got)
	}
	if got := env.Secrets.Values["GITHUB_TOKEN"].Plain; got != "vault://team/GITHUB_TOKEN" {
		t.Errorf("env.secrets.GITHUB_TOKEN.Plain = %q, want vault://team/GITHUB_TOKEN", got)
	}
	if env.Secrets.Values["GITHUB_TOKEN"].IsSecret() {
		t.Errorf("parser must not promote vault:// to IsSecret; that's the resolver's job")
	}
	if got := env.Secrets.Values["API_KEY"].Plain; got != "literal-fallback" {
		t.Errorf("env.secrets.API_KEY.Plain = %q, want literal-fallback", got)
	}
}

// TestParseEnvVarsSubtables covers all six requirement sub-tables.
func TestParseEnvVarsSubtables(t *testing.T) {
	input := `
[workspace]
name = "test-ws"

[env.vars.required]
LOG_LEVEL = "logging threshold"

[env.vars.recommended]
CACHE_DIR = "override default cache"

[env.vars.optional]
DEBUG_FEATURE = "enables experimental feature"

[env.secrets.required]
GITHUB_TOKEN = "required for gh CLI"

[env.secrets.recommended]
SENTRY_DSN = "error reporting"

[env.secrets.optional]
DATADOG_API_KEY = "APM metrics"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	env := result.Config.Env
	if env.Vars.Required["LOG_LEVEL"] != "logging threshold" {
		t.Errorf("env.vars.required.LOG_LEVEL = %q", env.Vars.Required["LOG_LEVEL"])
	}
	if env.Vars.Recommended["CACHE_DIR"] != "override default cache" {
		t.Errorf("env.vars.recommended.CACHE_DIR = %q", env.Vars.Recommended["CACHE_DIR"])
	}
	if env.Vars.Optional["DEBUG_FEATURE"] != "enables experimental feature" {
		t.Errorf("env.vars.optional.DEBUG_FEATURE = %q", env.Vars.Optional["DEBUG_FEATURE"])
	}
	if env.Secrets.Required["GITHUB_TOKEN"] != "required for gh CLI" {
		t.Errorf("env.secrets.required.GITHUB_TOKEN = %q", env.Secrets.Required["GITHUB_TOKEN"])
	}
	if env.Secrets.Recommended["SENTRY_DSN"] != "error reporting" {
		t.Errorf("env.secrets.recommended.SENTRY_DSN = %q", env.Secrets.Recommended["SENTRY_DSN"])
	}
	if env.Secrets.Optional["DATADOG_API_KEY"] != "APM metrics" {
		t.Errorf("env.secrets.optional.DATADOG_API_KEY = %q", env.Secrets.Optional["DATADOG_API_KEY"])
	}
}

// TestParseClaudeEnvSubtables covers the [claude.env] counterparts.
func TestParseClaudeEnvSubtables(t *testing.T) {
	input := `
[workspace]
name = "test-ws"

[claude.env]
promote = ["GH_TOKEN"]

[claude.env.vars]
CLAUDE_ONLY = "override"

[claude.env.vars.required]
CLAUDE_API_KEY = "authenticate claude code"

[claude.env.secrets]
ANTHROPIC_API_KEY = "vault://team/ANTHROPIC_API_KEY"

[claude.env.secrets.required]
ANTHROPIC_API_KEY = "needed for claude code"

[vault.providers.team]
kind = "fake"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ce := result.Config.Claude.Env
	if len(ce.Promote) != 1 || ce.Promote[0] != "GH_TOKEN" {
		t.Errorf("claude.env.promote = %v, want [GH_TOKEN]", ce.Promote)
	}
	if ce.Vars.Values["CLAUDE_ONLY"].Plain != "override" {
		t.Errorf("claude.env.vars.CLAUDE_ONLY.Plain = %q", ce.Vars.Values["CLAUDE_ONLY"].Plain)
	}
	if ce.Vars.Required["CLAUDE_API_KEY"] != "authenticate claude code" {
		t.Errorf("claude.env.vars.required.CLAUDE_API_KEY = %q", ce.Vars.Required["CLAUDE_API_KEY"])
	}
	if ce.Secrets.Values["ANTHROPIC_API_KEY"].Plain != "vault://team/ANTHROPIC_API_KEY" {
		t.Errorf("claude.env.secrets.ANTHROPIC_API_KEY.Plain = %q", ce.Secrets.Values["ANTHROPIC_API_KEY"].Plain)
	}
	if ce.Secrets.Required["ANTHROPIC_API_KEY"] != "needed for claude code" {
		t.Errorf("claude.env.secrets.required.ANTHROPIC_API_KEY = %q", ce.Secrets.Required["ANTHROPIC_API_KEY"])
	}
}

// TestParseV06BackwardsCompat proves an old workspace.toml with no
// [vault] block and a flat [env.vars] map of strings still parses
// cleanly under the new schema.
func TestParseV06BackwardsCompat(t *testing.T) {
	input := `
[workspace]
name = "legacy-ws"

[[sources]]
org = "myorg"

[env]
files = ["env/workspace.env"]

[env.vars]
LOG_LEVEL = "info"
MODE = "prod"

[claude.env]
vars = { EXTRA_FLAG = "claude-only" }
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cfg := result.Config
	if cfg.Vault != nil {
		t.Errorf("cfg.Vault should be nil when no [vault] declared")
	}
	if cfg.Env.Vars.Values["LOG_LEVEL"].Plain != "info" {
		t.Errorf("env.vars.LOG_LEVEL = %q, want info", cfg.Env.Vars.Values["LOG_LEVEL"].Plain)
	}
	if cfg.Claude.Env.Vars.Values["EXTRA_FLAG"].Plain != "claude-only" {
		t.Errorf("claude.env.vars.EXTRA_FLAG = %q, want claude-only", cfg.Claude.Env.Vars.Values["EXTRA_FLAG"].Plain)
	}
	// Unknown-fields warnings must not mention vault or env.secrets.
	for _, w := range result.Warnings {
		if strings.Contains(w, "vault") || strings.Contains(w, "secrets") {
			t.Errorf("legacy config produced unexpected vault/secrets warning: %s", w)
		}
	}
}

// TestParseRejectsVaultURIInContent covers the [claude.content.*]
// branch of the R3 deny list.
func TestParseRejectsVaultURIInContent(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{
			name: "workspace source",
			input: `[workspace]
name = "ws"
[claude.content.workspace]
source = "vault://secret-md"
`,
		},
		{
			name: "group source",
			input: `[workspace]
name = "ws"
[claude.content.groups.public]
source = "vault://secret-md"
`,
		},
		{
			name: "repo source",
			input: `[workspace]
name = "ws"
[claude.content.repos.foo]
source = "vault://secret-md"
`,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input))
			if err == nil {
				t.Fatal("expected error for vault:// in content source")
			}
			if !strings.Contains(err.Error(), "vault://") {
				t.Errorf("error should mention vault://; got %v", err)
			}
		})
	}
}

func TestParseRejectsVaultURIInEnvFiles(t *testing.T) {
	input := `
[workspace]
name = "ws"

[env]
files = ["vault://secret-env-file"]
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for vault:// in env.files")
	}
	if !strings.Contains(err.Error(), "env.files") {
		t.Errorf("error should mention env.files; got %v", err)
	}
}

// TestParseRejectsVaultURIInProviderConfig covers the
// [vault.provider*] branch: a provider's backend-specific fields must
// not be themselves vault:// references (bootstrap hazard).
func TestParseRejectsVaultURIInProviderConfig(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{
			name: "anonymous provider",
			input: `[workspace]
name = "ws"
[vault.provider]
kind = "sops"
key_path = "vault://other/key"
`,
		},
		{
			name: "named provider",
			input: `[workspace]
name = "ws"
[vault.providers.team]
kind = "infisical"
project_id = "vault://bootstrap/project"
`,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input))
			if err == nil {
				t.Fatal("expected error for vault:// in provider config")
			}
			if !strings.Contains(err.Error(), "vault://") {
				t.Errorf("error should mention vault://; got %v", err)
			}
		})
	}
}

// TestParseRejectsUndeclaredProviderRef checks the same-file provider-
// name validation: vault://name/key where name isn't declared in the
// same file.
func TestParseRejectsUndeclaredProviderRef(t *testing.T) {
	input := `
[workspace]
name = "ws"

[env.secrets]
GITHUB_TOKEN = "vault://nonexistent/GITHUB_TOKEN"
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for undeclared provider ref")
	}
	if !strings.Contains(err.Error(), "vault://nonexistent/GITHUB_TOKEN") {
		t.Errorf("error should mention the offending URI; got %v", err)
	}
}

// TestParseRejectsAnonymousRefWithNamedProvider exercises the
// anonymous-URI-against-named-provider mismatch: vault://key where
// the config uses [vault.providers.<name>] instead. The error must
// name what's actually declared so the user can pick between the two
// fix paths (switch URI form, or switch registry shape) without
// re-reading the PRD.
func TestParseRejectsAnonymousRefWithNamedProvider(t *testing.T) {
	input := `
[workspace]
name = "ws"

[vault.providers.team]
kind = "fake"

[vault.providers.shared]
kind = "fake"

[env.secrets]
GITHUB_TOKEN = "vault://GITHUB_TOKEN"
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for anonymous-URI with named provider only")
	}
	msg := err.Error()
	// The error must tell the user: (a) the URI form they used
	// (anonymous), (b) the registry shape declared (named), and (c)
	// both fix paths. Assert on substrings rather than the full
	// sentence so phrasing can evolve without test churn.
	wantSubs := []string{
		"anonymous form",
		"named providers",
		"shared, team", // sorted alphabetically for stable output
		"vault://<name>/<key>",
		"[vault.provider]",
		`"vault://GITHUB_TOKEN"`,
	}
	for _, want := range wantSubs {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got: %v", want, err)
		}
	}
}

// TestParseRejectsNamedRefWithAnonymousProvider is the symmetric
// mismatch: vault://name/key against a file declaring
// [vault.provider] (anonymous). The error must identify the shape
// (anonymous) and offer both fix paths (switch URI form, or switch
// registry shape by adopting the URI's name).
func TestParseRejectsNamedRefWithAnonymousProvider(t *testing.T) {
	input := `
[workspace]
name = "ws"

[vault.provider]
kind = "fake"

[env.secrets]
GITHUB_TOKEN = "vault://team/GITHUB_TOKEN"
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for named-URI with anonymous provider")
	}
	msg := err.Error()
	wantSubs := []string{
		"named form",
		"anonymous [vault.provider]",
		"vault://<key>",
		"[vault.providers.team]",
		`"vault://team/GITHUB_TOKEN"`,
	}
	for _, want := range wantSubs {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got: %v", want, err)
		}
	}
}

// TestParseAcceptsAnonymousRefWithAnonymousProvider proves the happy
// path for the singular shape.
func TestParseAcceptsAnonymousRefWithAnonymousProvider(t *testing.T) {
	input := `
[workspace]
name = "ws"

[vault.provider]
kind = "fake"

[env.secrets]
GITHUB_TOKEN = "vault://GITHUB_TOKEN"
`
	_, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

func TestParseRejectsVaultURIInWorkspaceName(t *testing.T) {
	input := `
[workspace]
name = "vault://something"
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for vault:// in workspace.name")
	}
}

func TestParseRejectsVaultURIInSourceOrg(t *testing.T) {
	input := `
[workspace]
name = "ws"

[[sources]]
org = "vault://secret-org"
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for vault:// in sources.org")
	}
}

func TestParseRejectsVaultURIInRepoURL(t *testing.T) {
	input := `
[workspace]
name = "ws"

[repos.myrepo]
url = "vault://team/clone-url"
group = "public"
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for vault:// in repos.url")
	}
}

// TestParseGlobalOverrideVault ensures the personal overlay also
// accepts anon-or-named providers via GlobalOverride.Vault.
func TestParseGlobalOverrideVault(t *testing.T) {
	input := `
[global.vault.providers.personal]
kind = "sops"
key_path = "keys/personal.age"

[workspaces.tsukumogami.vault.provider]
kind = "infisical"
project_id = "my-project"

[workspaces.tsukumogami.env.secrets]
GITHUB_TOKEN = "vault://my-token"
`
	cfg, err := ParseGlobalConfigOverride([]byte(input))
	if err != nil {
		t.Fatalf("ParseGlobalConfigOverride: %v", err)
	}
	if cfg.Global.Vault == nil {
		t.Fatal("Global.Vault should be non-nil")
	}
	if cfg.Global.Vault.Providers["personal"].Kind != "sops" {
		t.Errorf("Global.Vault.Providers[personal].Kind = %q, want sops",
			cfg.Global.Vault.Providers["personal"].Kind)
	}
	ws := cfg.Workspaces["tsukumogami"]
	if ws.Vault == nil {
		t.Fatal("workspaces.tsukumogami.Vault should be non-nil")
	}
	if ws.Vault.Provider == nil || ws.Vault.Provider.Kind != "infisical" {
		t.Errorf("workspaces.tsukumogami anon provider = %+v, want kind=infisical", ws.Vault.Provider)
	}
	if got := ws.Env.Secrets.Values["GITHUB_TOKEN"].Plain; got != "vault://my-token" {
		t.Errorf("workspaces.tsukumogami.env.secrets.GITHUB_TOKEN = %q, want vault://my-token", got)
	}
}

func TestParseGlobalOverrideVaultRejectsMixedShapes(t *testing.T) {
	input := `
[global.vault.provider]
kind = "sops"

[global.vault.providers.extra]
kind = "infisical"
`
	_, err := ParseGlobalConfigOverride([]byte(input))
	if err == nil {
		t.Fatal("expected error for mixed shapes in global overlay")
	}
}

// TestParseSettingsAcceptsVaultRef proves [claude.settings] values may
// be vault:// URIs when the referenced provider exists in-file.
func TestParseSettingsAcceptsVaultRef(t *testing.T) {
	input := `
[workspace]
name = "ws"

[vault.providers.team]
kind = "fake"

[claude.settings]
permissions = "bypass"
custom = "vault://team/some-setting"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := result.Config.Claude.Settings["custom"].Plain; got != "vault://team/some-setting" {
		t.Errorf("claude.settings.custom.Plain = %q", got)
	}
	if got := result.Config.Claude.Settings["permissions"].Plain; got != "bypass" {
		t.Errorf("claude.settings.permissions.Plain = %q", got)
	}
}

// TestParseFilesKeyAcceptsVaultRef covers that [files] source keys may
// be vault:// URIs when the named provider exists.
func TestParseFilesKeyAcceptsVaultRef(t *testing.T) {
	input := `
[workspace]
name = "ws"

[vault.providers.team]
kind = "fake"

[files]
"vault://team/file-contents" = ".config/target"
`
	_, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

// TestVaultRegistryKnownProviderNames covers the small helper used by
// same-file-reference validation.
func TestVaultRegistryKnownProviderNames(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		var v *VaultRegistry
		got := v.KnownProviderNames()
		if len(got) != 0 {
			t.Errorf("nil registry should have no known names, got %v", got)
		}
	})
	t.Run("anonymous", func(t *testing.T) {
		v := &VaultRegistry{Provider: &VaultProviderConfig{Kind: "fake"}}
		got := v.KnownProviderNames()
		if !got[""] || len(got) != 1 {
			t.Errorf("anonymous registry should only know \"\", got %v", got)
		}
	})
	t.Run("named", func(t *testing.T) {
		v := &VaultRegistry{Providers: map[string]VaultProviderConfig{
			"team":     {Kind: "infisical"},
			"personal": {Kind: "sops"},
		}}
		got := v.KnownProviderNames()
		if !got["team"] || !got["personal"] || got[""] || len(got) != 2 {
			t.Errorf("named registry known names = %v, want {team,personal}", got)
		}
	})
}
