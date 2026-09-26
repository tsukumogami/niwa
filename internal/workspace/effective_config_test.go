package workspace

import (
	"bytes"
	"context"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/secret/reveal"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/resolve"
)

// helperFakeProviderRegistry builds a one-key config.VaultRegistry pointing at
// the fake backend with backend-side values keyed by name. The fake backend's
// factory carries the values table inside its provider config under "values"
// (see internal/vault/fake) — this helper assembles that shape for tests.
func helperFakeProviderRegistry(values map[string]string) *config.VaultRegistry {
	return &config.VaultRegistry{
		Provider: &config.VaultProviderConfig{
			Kind: "fake",
			Config: map[string]any{
				"values": values,
			},
		},
	}
}

// TestResolveAndMergeEffectiveConfigResolvesTeamVaultRef confirms the helper
// runs ResolveWorkspace against the supplied team bundle and the resolved
// plaintext lands in the returned cfg's MaybeSecret.Secret slot. Without this
// call, MaybeSecret.Plain would still carry the literal "vault://API_TOKEN"
// URI -- which is exactly the worktree-path regression issue #162 fixes.
func TestResolveAndMergeEffectiveConfigResolvesTeamVaultRef(t *testing.T) {
	withFakeVaultBackend(t)

	const want = "resolved-token-value-xxxxx"
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Vault:     helperFakeProviderRegistry(map[string]string{"API_TOKEN": want}),
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"API_TOKEN": {Plain: "vault://API_TOKEN"},
				},
			},
		},
	}

	ctx := context.Background()
	teamBundle, err := resolve.BuildBundle(ctx, vault.DefaultRegistry, cfg.Vault, "test team")
	if err != nil {
		t.Fatalf("BuildBundle team: %v", err)
	}
	defer teamBundle.CloseAll()
	personalBundle, err := resolve.BuildBundle(ctx, vault.DefaultRegistry, nil, "test personal")
	if err != nil {
		t.Fatalf("BuildBundle personal: %v", err)
	}
	defer personalBundle.CloseAll()

	effective, policy, _, err := ResolveAndMergeEffectiveConfig(
		ctx, cfg, nil, teamBundle, personalBundle,
		EffectiveConfigOptions{Stderr: &bytes.Buffer{}},
	)
	if err != nil {
		t.Fatalf("ResolveAndMergeEffectiveConfig: %v", err)
	}
	if policy != nil {
		t.Errorf("policy should be nil when no global override is supplied; got %#v", policy)
	}
	got, ok := effective.Env.Secrets.Values["API_TOKEN"]
	if !ok {
		t.Fatal("API_TOKEN missing from resolved env.secrets")
	}
	if !got.IsSecret() {
		t.Errorf("API_TOKEN must be promoted to Secret after resolve; got Plain=%q", got.Plain)
	}
	if got.Plain != "" {
		t.Errorf("API_TOKEN must have empty Plain after resolve; got Plain=%q", got.Plain)
	}
	if string(reveal.UnsafeReveal(got.Secret)) != want {
		t.Errorf("API_TOKEN secret bytes = %q, want %q", reveal.UnsafeReveal(got.Secret), want)
	}
}

// TestResolveAndMergeEffectiveConfigMergesPersonalOverlayEnv confirms env keys
// declared in a personal global override reach the returned effective cfg.
// Without MergeGlobalOverride the worktree path silently drops every
// overlay-only env key -- the second half of issue #162's regression.
func TestResolveAndMergeEffectiveConfigMergesPersonalOverlayEnv(t *testing.T) {
	withFakeVaultBackend(t)

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
	}
	override := &config.GlobalConfigOverride{
		Global: config.GlobalOverride{
			Env: config.EnvConfig{
				Vars: config.EnvVarsTable{
					Values: map[string]config.MaybeSecret{
						"PERSONAL_KEY": {Plain: "personal-value"},
					},
				},
			},
		},
	}

	ctx := context.Background()
	teamBundle, err := resolve.BuildBundle(ctx, vault.DefaultRegistry, nil, "test team")
	if err != nil {
		t.Fatalf("BuildBundle team: %v", err)
	}
	defer teamBundle.CloseAll()
	personalBundle, err := resolve.BuildBundle(ctx, vault.DefaultRegistry, nil, "test personal")
	if err != nil {
		t.Fatalf("BuildBundle personal: %v", err)
	}
	defer personalBundle.CloseAll()

	effective, _, _, err := ResolveAndMergeEffectiveConfig(
		ctx, cfg, override, teamBundle, personalBundle,
		EffectiveConfigOptions{Stderr: &bytes.Buffer{}},
	)
	if err != nil {
		t.Fatalf("ResolveAndMergeEffectiveConfig: %v", err)
	}
	got, ok := effective.Env.Vars.Values["PERSONAL_KEY"]
	if !ok {
		t.Fatal("PERSONAL_KEY missing from merged env.vars")
	}
	if got.Plain != "personal-value" {
		t.Errorf("PERSONAL_KEY = %q, want personal-value", got.Plain)
	}
}

// TestResolveAndMergeEffectiveConfigMarksMissing confirms a missing
// vault key comes back through the helper as a marked empty value
// rather than an error, and that nothing is written to the diagnostic
// sink on the way. Both halves matter: the mark is what downstream
// reporting reads, and the silence is what keeps the report a single
// consolidated one rather than a scattering of warnings.
//
// This replaces a test that set an AllowMissingSecrets flag to get the
// same tolerance. Tolerance is now the default, so there is no flag to
// thread.
func TestResolveAndMergeEffectiveConfigMarksMissing(t *testing.T) {
	withFakeVaultBackend(t)

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		// Provider exists, value table is empty -- the key is missing.
		Vault: helperFakeProviderRegistry(map[string]string{}),
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"MISSING": {Plain: "vault://MISSING"},
				},
			},
		},
	}

	ctx := context.Background()
	teamBundle, err := resolve.BuildBundle(ctx, vault.DefaultRegistry, cfg.Vault, "test team")
	if err != nil {
		t.Fatalf("BuildBundle team: %v", err)
	}
	defer teamBundle.CloseAll()
	personalBundle, err := resolve.BuildBundle(ctx, vault.DefaultRegistry, nil, "test personal")
	if err != nil {
		t.Fatalf("BuildBundle personal: %v", err)
	}
	defer personalBundle.CloseAll()

	var stderr bytes.Buffer
	merged, _, _, err := ResolveAndMergeEffectiveConfig(
		ctx, cfg, nil, teamBundle, personalBundle,
		EffectiveConfigOptions{Stderr: &stderr},
	)
	if err != nil {
		t.Fatalf("a missing key must not fail the resolve, got error: %v", err)
	}
	got := merged.Env.Secrets.Values["MISSING"]
	if !got.IsUnresolved() {
		t.Fatal("expected the missing key to come back marked")
	}
	if got.Unresolved.Cause != config.CauseKeyNotFound {
		t.Errorf("Cause = %q, want %q", got.Unresolved.Cause, config.CauseKeyNotFound)
	}
	if stderr.Len() != 0 {
		t.Errorf("the resolve path must stay silent; got:\n%s", stderr.String())
	}
}

// TestResolveAndMergeEffectiveConfigNilOverridePassthrough confirms the
// helper short-circuits when no personal overlay is registered: the team
// resolve still runs but no merge happens and the returned policy is nil.
func TestResolveAndMergeEffectiveConfigNilOverridePassthrough(t *testing.T) {
	withFakeVaultBackend(t)

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Env: config.EnvConfig{
			Vars: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"TEAM_KEY": {Plain: "team-value"},
				},
			},
		},
	}

	ctx := context.Background()
	teamBundle, err := resolve.BuildBundle(ctx, vault.DefaultRegistry, nil, "test team")
	if err != nil {
		t.Fatalf("BuildBundle team: %v", err)
	}
	defer teamBundle.CloseAll()
	personalBundle, err := resolve.BuildBundle(ctx, vault.DefaultRegistry, nil, "test personal")
	if err != nil {
		t.Fatalf("BuildBundle personal: %v", err)
	}
	defer personalBundle.CloseAll()

	effective, policy, _, err := ResolveAndMergeEffectiveConfig(
		ctx, cfg, nil, teamBundle, personalBundle,
		EffectiveConfigOptions{},
	)
	if err != nil {
		t.Fatalf("ResolveAndMergeEffectiveConfig: %v", err)
	}
	if policy != nil {
		t.Errorf("policy must be nil when override is nil; got %#v", policy)
	}
	if got := effective.Env.Vars.Values["TEAM_KEY"].Plain; got != "team-value" {
		t.Errorf("TEAM_KEY = %q, want team-value", got)
	}
}
