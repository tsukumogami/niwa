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

// TestResolveWorkspaceAllowMissingDowngradesWithWarning: when
// opts.AllowMissing is true, a non-optional miss downgrades to empty
// but emits a stderr warning naming the key and provider.
func TestResolveWorkspaceAllowMissingDowngradesWithWarning(t *testing.T) {
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
		Registry:     newFakeRegistry(t),
		AllowMissing: true,
		Stderr:       &stderr,
	})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	if out.Env.Secrets.Values["MISSING"].IsSecret() {
		t.Error("expected downgrade to empty")
	}
	if !strings.Contains(stderr.String(), "MISSING") {
		t.Errorf("expected warning to name the key, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "--allow-missing-secrets") {
		t.Errorf("expected warning to reference the flag, got %q", stderr.String())
	}
}

// TestResolveWorkspaceMissingErrorsByDefault: without ?required=false
// and without AllowMissing, a missing key returns an error wrapping
// vault.ErrKeyNotFound with R9 remediation in the message.
func TestResolveWorkspaceMissingErrorsByDefault(t *testing.T) {
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
	_, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		Registry: newFakeRegistry(t),
	})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	if !errors.Is(err, vault.ErrKeyNotFound) {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"MISSING", "--allow-missing-secrets", "?required=false"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing remediation phrase %q: %s", want, msg)
		}
	}
}

// TestResolveWorkspaceUnknownProviderAtResolveTime: a vault:// URI
// that names a provider not present in the bundle at resolve time
// fails with ErrKeyNotFound and an actionable message. This is
// distinct from the parse-time same-file check (which covers the
// simpler typo case).
//
// We set up this failure by pre-building a bundle from a different
// (empty) VaultRegistry via TeamBundle, decoupling the parse-time
// check from the resolver.
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
	// An empty bundle -- "team-vault" isn't in it.
	emptyBundle, err := vault.NewRegistry().Build(context.Background(), nil)
	if err != nil {
		t.Fatalf("Build empty bundle: %v", err)
	}

	_, err = resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		TeamBundle: emptyBundle,
	})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !errors.Is(err, vault.ErrKeyNotFound) {
		t.Errorf("expected ErrKeyNotFound wrap, got %v", err)
	}
	if !strings.Contains(err.Error(), "team-vault") {
		t.Errorf("expected provider name in error, got %q", err.Error())
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
// anonymous singular provider; the empty-string name collides.
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
	if err == nil {
		t.Fatal("expected collision error")
	}
	if !errors.Is(err, vault.ErrProviderNameCollision) {
		t.Errorf("expected ErrProviderNameCollision, got %v", err)
	}
	if !strings.Contains(err.Error(), "anonymous") {
		t.Errorf("expected message to mention anonymous, got %q", err.Error())
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
// ErrProviderUnreachable, the resolver wraps the error (always,
// regardless of AllowMissing -- AllowMissing targets ErrKeyNotFound
// only).
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
	_, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
		Registry:     newFakeRegistry(t),
		AllowMissing: true, // should not help -- unreachable != missing
	})
	if err == nil {
		t.Fatal("expected error for provider unreachable")
	}
	if !errors.Is(err, vault.ErrProviderUnreachable) {
		t.Errorf("expected ErrProviderUnreachable, got %v", err)
	}
}
