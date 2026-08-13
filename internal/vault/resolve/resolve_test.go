package resolve_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/secret/reveal"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/fake"
	"github.com/tsukumogami/niwa/internal/vault/resolve"
)

// newFakeRegistry returns a fresh vault.Registry with the fake
// backend registered. Tests must NOT mutate vault.DefaultRegistry
// directly; using NewRegistry keeps each test self-contained.
func newFakeRegistry(t *testing.T) *vault.Registry {
	t.Helper()
	reg := vault.NewRegistry()
	if err := reg.Register(fake.NewFactory()); err != nil {
		t.Fatalf("register fake factory: %v", err)
	}
	return reg
}

// TestResolveWorkspacePassthroughNilVault confirms that a workspace
// config without a [vault] block passes through the resolver
// unchanged. The resolver must be a safe no-op in that case so
// existing workspaces keep working without any vault setup.
func TestResolveWorkspacePassthroughNilVault(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Env: config.EnvConfig{
			Vars: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"LOG_LEVEL": {Plain: "debug"},
				},
			},
		},
	}

	out, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
	})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	if out.Env.Vars.Values["LOG_LEVEL"].Plain != "debug" {
		t.Errorf("LOG_LEVEL plain = %q, want %q", out.Env.Vars.Values["LOG_LEVEL"].Plain, "debug")
	}
	if out.Env.Vars.Values["LOG_LEVEL"].IsSecret() {
		t.Error("LOG_LEVEL should not be a secret")
	}
}

// TestResolveWorkspaceResolvesVaultURI confirms that a MaybeSecret
// whose Plain is a vault:// URI gets replaced by a populated Secret.
func TestResolveWorkspaceResolvesVaultURI(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Vault: &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{
				Kind: "fake",
				Config: map[string]any{
					"values": map[string]string{
						"GH_TOKEN": "not-a-real-token-but-long-enough",
					},
				},
			},
		},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"GH_TOKEN": {Plain: "vault://GH_TOKEN"},
				},
			},
		},
	}

	out, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
	})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}

	got := out.Env.Secrets.Values["GH_TOKEN"]
	if !got.IsSecret() {
		t.Fatal("expected resolved secret, got plain")
	}
	if got.Plain != "" {
		t.Errorf("expected Plain cleared after resolve, got %q", got.Plain)
	}
	if string(reveal.UnsafeReveal(got.Secret)) != "not-a-real-token-but-long-enough" {
		t.Errorf("plaintext mismatch: got %q", reveal.UnsafeReveal(got.Secret))
	}
	if got.Token.Token == "" {
		t.Error("expected VersionToken to be populated")
	}
}

// TestResolveWorkspaceDoesNotMutateInput locks in the "returns a NEW
// *WorkspaceConfig -- never mutate the input" invariant.
func TestResolveWorkspaceDoesNotMutateInput(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Vault: &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{
				Kind:   "fake",
				Config: map[string]any{"values": map[string]string{"K": "vvvvvvvvvvvvvv"}},
			},
		},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"K": {Plain: "vault://K"},
				},
			},
		},
	}
	_, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
	})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	if cfg.Env.Secrets.Values["K"].Plain != "vault://K" {
		t.Errorf("input was mutated: Plain = %q", cfg.Env.Secrets.Values["K"].Plain)
	}
	if cfg.Env.Secrets.Values["K"].IsSecret() {
		t.Error("input was mutated: became a secret")
	}
}

// TestResolveWorkspaceAutoWrapsPlaintextInSecretsTable exercises
// Decision 1's auto-wrap rule: plaintext values written into *.secrets
// must still be wrapped in secret.Value so downstream redaction
// applies even when no vault is configured.
func TestResolveWorkspaceAutoWrapsPlaintextInSecretsTable(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"API_KEY": {Plain: "literal-plaintext-secret-value"},
				},
			},
			Vars: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"NON_SECRET": {Plain: "literal-plaintext-non-secret"},
				},
			},
		},
	}
	out, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	gotSecret := out.Env.Secrets.Values["API_KEY"]
	if !gotSecret.IsSecret() {
		t.Errorf("secrets table value should be auto-wrapped: IsSecret=false")
	}
	if gotSecret.Plain != "" {
		t.Errorf("auto-wrapped value must clear Plain, got %q", gotSecret.Plain)
	}
	if string(reveal.UnsafeReveal(gotSecret.Secret)) != "literal-plaintext-secret-value" {
		t.Errorf("plaintext not preserved in Secret bytes")
	}

	gotVar := out.Env.Vars.Values["NON_SECRET"]
	if gotVar.IsSecret() {
		t.Errorf("vars table plaintext must not be auto-wrapped")
	}
	if gotVar.Plain != "literal-plaintext-non-secret" {
		t.Errorf("vars plain mutated: %q", gotVar.Plain)
	}
}

// TestResolveWorkspaceOptionalDowngradesSilently: a missing ?required=false
// ref downgrades to empty without error and without stderr output.
func TestResolveWorkspaceOptionalDowngradesSilently(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Vault: &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{
				Kind:   "fake",
				Config: map[string]any{"values": map[string]string{}},
			},
		},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"OPT": {Plain: "vault://OPT?required=false"},
				},
			},
		},
	}
	var stderr bytes.Buffer
	out, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
		Stderr:   &stderr,
	})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	got := out.Env.Secrets.Values["OPT"]
	if got.IsSecret() {
		t.Error("optional miss should become empty, not secret")
	}
	if got.Plain != "" {
		t.Errorf("optional miss should clear Plain, got %q", got.Plain)
	}
	if stderr.Len() != 0 {
		t.Errorf("optional miss must not log anything, got %q", stderr.String())
	}
}

// TestResolveWorkspaceMissingKeyIsSilent: a non-optional miss is
// recorded on the value, not announced. The resolver has no idea yet
// whether the shortfall matters, so it says nothing; reporting happens
// once, post-merge, where the whole picture is available.
//
// This replaces a test that asserted a stderr warning naming
// --allow-missing-secrets. That flag no longer routes around anything,
// so a warning telling the reader to pass it would be wrong twice.
func TestResolveWorkspaceMissingKeyIsSilent(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Vault: &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{
				Kind:   "fake",
				Config: map[string]any{"values": map[string]string{}},
			},
		},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"MISSING": {Plain: "vault://MISSING"},
				},
			},
		},
	}
	var stderr bytes.Buffer
	out, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
		Stderr:   &stderr,
	})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	if out.Env.Secrets.Values["MISSING"].IsSecret() {
		t.Error("expected an empty value, not a secret")
	}
	if !out.Env.Secrets.Values["MISSING"].IsUnresolved() {
		t.Error("expected the miss to be marked")
	}
	if stderr.Len() != 0 {
		t.Errorf("resolver must not write diagnostics, got %q", stderr.String())
	}
}

// TestResolveWorkspaceMissingMarksByDefault: a key a reachable provider
// does not hold is marked rather than erroring, and the mark carries
// the declared level, description, and provider kind so a report can be
// assembled from it without re-reading the config.
//
// This inverts the old default, which failed resolution outright.
// Fatality now lives post-merge, where a required key on a reachable
// provider is still fatal but nothing else is.
func TestResolveWorkspaceMissingMarksByDefault(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Vault: &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{
				Kind:   "fake",
				Config: map[string]any{"values": map[string]string{}},
			},
		},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"MISSING": {Plain: "vault://MISSING"},
				},
				Required: map[string]string{
					"MISSING": "token the pipeline needs",
				},
			},
		},
	}
	out, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
	})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	got := out.Env.Secrets.Values["MISSING"]
	if !got.IsUnresolved() {
		t.Fatal("expected a mark on the missing key")
	}
	if got.Unresolved.Cause != config.CauseKeyNotFound {
		t.Errorf("Cause = %q, want %q", got.Unresolved.Cause, config.CauseKeyNotFound)
	}
	if got.Unresolved.Level != config.LevelRequired {
		t.Errorf("Level = %q, want %q", got.Unresolved.Level, config.LevelRequired)
	}
	if got.Unresolved.Description != "token the pipeline needs" {
		t.Errorf("Description = %q, want the required-table text", got.Unresolved.Description)
	}
	if got.Unresolved.ProviderKind != "fake" {
		t.Errorf("ProviderKind = %q, want fake", got.Unresolved.ProviderKind)
	}
	// The mark is metadata about an absence, never part of the value.
	if got.String() != "" {
		t.Errorf("String() = %q, want the zero rendering", got.String())
	}
	text, err := got.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if len(text) != 0 {
		t.Errorf("MarshalText() = %q, want the zero rendering", text)
	}
}

// TestResolveWorkspaceClientNotInstalledMarksSeparately: an absent
// client binary is marked distinctly from any other unreachability,
// because the remedy differs -- install the client, versus repair
// credentials or connectivity.
func TestResolveWorkspaceClientNotInstalledMarksSeparately(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Vault: &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{
				Kind:   "fake",
				Config: map[string]any{"no_client": true},
			},
		},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"K": {Plain: "vault://K"},
				},
			},
		},
	}
	out, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
	})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	got := out.Env.Secrets.Values["K"]
	if !got.IsUnresolved() {
		t.Fatal("expected a mark for the absent client")
	}
	if got.Unresolved.Cause != config.CauseClientNotInstalled {
		t.Errorf("Cause = %q, want %q", got.Unresolved.Cause, config.CauseClientNotInstalled)
	}
}

// TestResolveWorkspaceCarriesMarkThroughDeepCopy: the resolver's
// deep-copy path moves an existing mark into the output untouched, and
// does not re-resolve an already-marked value.
//
// The second half is what protects the workspace-overlay layer: it is
// resolved against its own provider bundle and then merged into the
// base config, which the pipeline resolves again as a whole. Without
// the guard, the second pass would overwrite a correct mark with one
// derived from the wrong bundle.
func TestResolveWorkspaceCarriesMarkThroughDeepCopy(t *testing.T) {
	mark := &config.Unresolved{
		Cause:        config.CauseProviderUnreachable,
		Level:        config.LevelRecommended,
		Description:  "set by an earlier layer",
		ProviderKind: "infisical",
	}
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"K": {Unresolved: mark},
				},
			},
		},
	}
	out, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
	})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	got := out.Env.Secrets.Values["K"]
	if !got.IsUnresolved() {
		t.Fatal("deep copy dropped the mark")
	}
	if *got.Unresolved != *mark {
		t.Errorf("mark changed across the copy: got %+v, want %+v", *got.Unresolved, *mark)
	}
}

// TestResolveGlobalOverrideCarriesMarkThroughDeepCopy is the same
// assertion for the personal-overlay deep-copy path, which is a
// separate function over a separate struct shape.
func TestResolveGlobalOverrideCarriesMarkThroughDeepCopy(t *testing.T) {
	mark := &config.Unresolved{
		Cause:       config.CauseUndeclaredProvider,
		Level:       config.LevelOptional,
		Description: "set by an earlier layer",
	}
	gco := &config.GlobalConfigOverride{
		Global: config.GlobalOverride{
			Env: config.EnvConfig{
				Secrets: config.EnvVarsTable{
					Values: map[string]config.MaybeSecret{
						"K": {Unresolved: mark},
					},
				},
			},
		},
	}
	out, err := resolve.ResolveGlobalOverride(context.Background(), gco, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
	})
	if err != nil {
		t.Fatalf("ResolveGlobalOverride: %v", err)
	}
	got := out.Global.Env.Secrets.Values["K"]
	if !got.IsUnresolved() {
		t.Fatal("deep copy dropped the mark")
	}
	if *got.Unresolved != *mark {
		t.Errorf("mark changed across the copy: got %+v, want %+v", *got.Unresolved, *mark)
	}
}

// TestResolveWorkspaceUnknownProviderAtResolveTime: a vault:// URI
// that names a provider not present in the bundle at resolve time is
// marked as an undeclared provider, NOT as key-not-found. The
// distinction matters because key-not-found is the one cause that keeps
// a required key fatal, and nothing here established that a reachable
// backend lacks the key -- no backend was found to ask.
//
// This is distinct from the parse-time same-file check (which covers
// the simpler typo case).
//
// We set up this failure by pre-building a bundle with one named
// provider ("other") and referencing a different named provider
// ("team-vault") in the config. The resolver sees HasNamedProviders
// on the bundle and parses the URI in named mode; Get("team-vault")
// fails, surfacing the unknown-provider error at resolve time.
func TestResolveWorkspaceUnknownProviderAtResolveTime(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"K": {Plain: "vault://team-vault/K"},
				},
			},
		},
	}
	// Build a bundle with one named provider ("other") so the bundle
	// reports HasNamedProviders=true — this forces ParseNamed at
	// resolve time. The referenced provider "team-vault" is still
	// absent, so Get fails.
	reg := newFakeRegistry(t)
	bundle, err := reg.Build(context.Background(), []vault.ProviderSpec{
		{Name: "other", Kind: fake.Kind, Config: vault.ProviderConfig{}, Source: "test"},
	})
	if err != nil {
		t.Fatalf("Build bundle: %v", err)
	}

	out, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		TeamBundle: bundle,
	})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	got := out.Env.Secrets.Values["K"]
	if !got.IsUnresolved() {
		t.Fatal("expected a mark for the undeclared provider")
	}
	if got.Unresolved.Cause != config.CauseUndeclaredProvider {
		t.Errorf("Cause = %q, want %q", got.Unresolved.Cause, config.CauseUndeclaredProvider)
	}
	if got.Unresolved.ProviderKind != "" {
		t.Errorf("ProviderKind = %q, want empty: no provider was ever reached", got.Unresolved.ProviderKind)
	}
}

// TestResolveWorkspaceRegistersOnRedactor verifies the secret value
// is added to the ctx-scoped Redactor so subsequent log lines are
// scrubbed automatically.
func TestResolveWorkspaceRegistersOnRedactor(t *testing.T) {
	const tokenValue = "zzzzzzzzzz-sensitive-fragment"
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Vault: &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{
				Kind:   "fake",
				Config: map[string]any{"values": map[string]string{"K": tokenValue}},
			},
		},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{"K": {Plain: "vault://K"}},
			},
		},
	}
	redactor := secret.NewRedactor()
	ctx := secret.WithRedactor(context.Background(), redactor)
	if _, err := resolve.ResolveWorkspace(ctx, cfg, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
	}); err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	if scrubbed := redactor.Scrub("prefix " + tokenValue + " suffix"); strings.Contains(scrubbed, tokenValue) {
		t.Errorf("redactor did not scrub secret: %q", scrubbed)
	}
}

// TestResolveWorkspaceWalksRepoAndInstance confirms per-repo and
// instance-level MaybeSecret slots are visited.
func TestResolveWorkspaceWalksRepoAndInstance(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Vault: &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{
				Kind: "fake",
				Config: map[string]any{"values": map[string]string{
					"REPO_KEY": "repo-value-sufficient-length",
					"INST_KEY": "inst-value-sufficient-length",
				}},
			},
		},
		Repos: map[string]config.RepoOverride{
			"r1": {
				Env: config.EnvConfig{
					Secrets: config.EnvVarsTable{
						Values: map[string]config.MaybeSecret{"X": {Plain: "vault://REPO_KEY"}},
					},
				},
			},
		},
		Instance: config.InstanceConfig{
			Env: config.EnvConfig{
				Secrets: config.EnvVarsTable{
					Values: map[string]config.MaybeSecret{"Y": {Plain: "vault://INST_KEY"}},
				},
			},
		},
	}
	out, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
	})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	if !out.Repos["r1"].Env.Secrets.Values["X"].IsSecret() {
		t.Error("repos.r1.env.secrets.X should be resolved")
	}
	if !out.Instance.Env.Secrets.Values["Y"].IsSecret() {
		t.Error("instance.env.secrets.Y should be resolved")
	}
}

// TestResolveGlobalOverrideBasic exercises the personal-overlay
// resolution path against the flat [global] block.
func TestResolveGlobalOverrideBasic(t *testing.T) {
	gco := &config.GlobalConfigOverride{
		Global: config.GlobalOverride{
			Vault: &config.VaultRegistry{
				Provider: &config.VaultProviderConfig{
					Kind:   "fake",
					Config: map[string]any{"values": map[string]string{"K": "personal-vvvvvvvvvvvvvv"}},
				},
			},
			Env: config.EnvConfig{
				Secrets: config.EnvVarsTable{
					Values: map[string]config.MaybeSecret{"K": {Plain: "vault://K"}},
				},
			},
		},
	}
	out, err := resolve.ResolveGlobalOverride(context.Background(), gco, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
	})
	if err != nil {
		t.Fatalf("ResolveGlobalOverride: %v", err)
	}
	if !out.Global.Env.Secrets.Values["K"].IsSecret() {
		t.Error("global.env.secrets.K should be resolved")
	}
}

// TestResolveGlobalOverridePerWorkspaceBlock: the file-local bundle
// from gco.Global.Vault also resolves refs in per-workspace blocks.
func TestResolveGlobalOverridePerWorkspaceBlock(t *testing.T) {
	gco := &config.GlobalConfigOverride{
		Global: config.GlobalOverride{
			Vault: &config.VaultRegistry{
				Provider: &config.VaultProviderConfig{
					Kind:   "fake",
					Config: map[string]any{"values": map[string]string{"WS_KEY": "ws-vvvvvvvvvvvvvvv"}},
				},
			},
		},
		Workspaces: map[string]config.GlobalOverride{
			"my-ws": {
				Env: config.EnvConfig{
					Secrets: config.EnvVarsTable{
						Values: map[string]config.MaybeSecret{"X": {Plain: "vault://WS_KEY"}},
					},
				},
			},
		},
	}
	out, err := resolve.ResolveGlobalOverride(context.Background(), gco, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
	})
	if err != nil {
		t.Fatalf("ResolveGlobalOverride: %v", err)
	}
	if !out.Workspaces["my-ws"].Env.Secrets.Values["X"].IsSecret() {
		t.Error("workspaces.my-ws.env.secrets.X should be resolved")
	}
}

// TestCheckProviderNameCollisionEmpty: no collisions between empty
// bundles returns nil.
func TestCheckProviderNameCollisionEmpty(t *testing.T) {
	team, _ := vault.NewRegistry().Build(context.Background(), nil)
	personal, _ := vault.NewRegistry().Build(context.Background(), nil)
	if err := resolve.CheckProviderNameCollision(team, personal); err != nil {
		t.Errorf("no collisions expected, got %v", err)
	}
}

// TestCheckProviderNameCollisionAnonymous: both sides declare the
// anonymous singular provider. Anonymous providers are file-scoped per
// D-9: each file's [vault.provider] resolves its own URIs independently
// before merge. R12 applies only to NAMED providers.
func TestCheckProviderNameCollisionAnonymous(t *testing.T) {
	reg := newFakeRegistry(t)
	team, err := reg.Build(context.Background(), []vault.ProviderSpec{
		{Name: "", Kind: "fake", Config: vault.ProviderConfig{}, Source: "ws"},
	})
	if err != nil {
		t.Fatalf("team Build: %v", err)
	}
	defer team.CloseAll()
	personal, err := reg.Build(context.Background(), []vault.ProviderSpec{
		{Name: "", Kind: "fake", Config: vault.ProviderConfig{}, Source: "overlay"},
	})
	if err != nil {
		t.Fatalf("personal Build: %v", err)
	}
	defer personal.CloseAll()

	err = resolve.CheckProviderNameCollision(team, personal)
	if err != nil {
		t.Fatalf("anonymous providers should NOT collide (file-scoped per D-9), got: %v", err)
	}
}

// TestCheckProviderNameCollisionNamed: both declare a provider named
// "team-vault"; names are listed in the error message.
func TestCheckProviderNameCollisionNamed(t *testing.T) {
	reg := newFakeRegistry(t)
	team, err := reg.Build(context.Background(), []vault.ProviderSpec{
		{Name: "team-vault", Kind: "fake", Config: vault.ProviderConfig{"name": "team-vault"}, Source: "ws"},
	})
	if err != nil {
		t.Fatalf("team Build: %v", err)
	}
	defer team.CloseAll()
	personal, err := reg.Build(context.Background(), []vault.ProviderSpec{
		{Name: "team-vault", Kind: "fake", Config: vault.ProviderConfig{"name": "team-vault"}, Source: "overlay"},
	})
	if err != nil {
		t.Fatalf("personal Build: %v", err)
	}
	defer personal.CloseAll()

	err = resolve.CheckProviderNameCollision(team, personal)
	if err == nil {
		t.Fatal("expected collision error")
	}
	if !errors.Is(err, vault.ErrProviderNameCollision) {
		t.Errorf("expected ErrProviderNameCollision, got %v", err)
	}
	if !strings.Contains(err.Error(), "team-vault") {
		t.Errorf("expected name 'team-vault' in error, got %q", err.Error())
	}
}

// TestCheckProviderNameCollisionPersonalAdditive: personal declares
// a new provider that team does not, so there is no collision.
func TestCheckProviderNameCollisionPersonalAdditive(t *testing.T) {
	reg := newFakeRegistry(t)
	team, err := reg.Build(context.Background(), []vault.ProviderSpec{
		{Name: "team-vault", Kind: "fake", Config: vault.ProviderConfig{"name": "team-vault"}, Source: "ws"},
	})
	if err != nil {
		t.Fatalf("team Build: %v", err)
	}
	defer team.CloseAll()
	personal, err := reg.Build(context.Background(), []vault.ProviderSpec{
		{Name: "personal-vault", Kind: "fake", Config: vault.ProviderConfig{"name": "personal-vault"}, Source: "overlay"},
	})
	if err != nil {
		t.Fatalf("personal Build: %v", err)
	}
	defer personal.CloseAll()

	if err := resolve.CheckProviderNameCollision(team, personal); err != nil {
		t.Errorf("no collision expected when personal adds a new provider, got %v", err)
	}
}

// TestBuildBundleNilRegistry returns an empty Bundle for a nil
// config.VaultRegistry. This is the passthrough case: a workspace
// without a [vault] block should still produce a valid empty bundle.
func TestBuildBundleNilRegistry(t *testing.T) {
	b, err := resolve.BuildBundle(context.Background(), newFakeRegistry(t), nil, "workspace")
	if err != nil {
		t.Fatalf("BuildBundle nil: %v", err)
	}
	if b == nil {
		t.Fatal("expected non-nil bundle")
	}
	if len(b.Names()) != 0 {
		t.Errorf("expected empty bundle, got names %v", b.Names())
	}
	if err := b.CloseAll(); err != nil {
		t.Errorf("CloseAll: %v", err)
	}
}

// TestBuildBundleNamed opens a bundle with a named provider and
// verifies the name appears in Bundle.Names().
func TestBuildBundleNamed(t *testing.T) {
	vr := &config.VaultRegistry{
		Providers: map[string]config.VaultProviderConfig{
			"team": {
				Kind: "fake",
				Config: map[string]any{
					"values": map[string]string{"K": "not-a-real-value-xxxxxx"},
				},
			},
		},
	}
	b, err := resolve.BuildBundle(context.Background(), newFakeRegistry(t), vr, "ws")
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}
	defer b.CloseAll()
	names := b.Names()
	if len(names) != 1 || names[0] != "team" {
		t.Errorf("expected names=[team], got %v", names)
	}
}

// TestResolveWorkspaceInvalidVaultURI: a malformed vault:// URI in a
// MaybeSecret value surfaces as a parse error (not a provider error),
// naming the TOML location.
func TestResolveWorkspaceInvalidVaultURI(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Vault: &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{Kind: "fake", Config: map[string]any{}},
		},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"BAD": {Plain: "vault://"}, // empty key
				},
			},
		},
	}
	_, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
	})
	if err == nil {
		t.Fatal("expected parse error for malformed vault URI")
	}
	if !strings.Contains(err.Error(), "env.secrets.BAD") {
		t.Errorf("expected TOML location in error, got %q", err.Error())
	}
}

// TestResolveWorkspaceProviderUnreachable: when the provider returns
// ErrProviderUnreachable, the resolver marks the value with that cause
// and carries on. A backend that cannot be contacted is a property of
// the host, not a defect in the configuration.
func TestResolveWorkspaceProviderUnreachable(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Vault: &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{
				Kind: "fake",
				Config: map[string]any{
					"fail_open": true, // fake returns ErrProviderUnreachable on unknown keys
				},
			},
		},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"K": {Plain: "vault://K"},
				},
			},
		},
	}
	out, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
	})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	got := out.Env.Secrets.Values["K"]
	if !got.IsUnresolved() {
		t.Fatal("expected a mark for the unreachable provider")
	}
	if got.Unresolved.Cause != config.CauseProviderUnreachable {
		t.Errorf("Cause = %q, want %q", got.Unresolved.Cause, config.CauseProviderUnreachable)
	}
	if got.Unresolved.ProviderKind != "fake" {
		t.Errorf("ProviderKind = %q, want fake", got.Unresolved.ProviderKind)
	}
}
