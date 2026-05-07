package workspace

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/fake"
)

// registerFakeMu protects the per-test register/unregister cycle
// against concurrent test executions, but the bigger reason for the
// register-then-unregister pattern is that other tests in this
// package (e.g., sources_test.go) also call DefaultRegistry.Register
// and expect it to succeed — so this test must not leak the fake
// registration past its own scope.
var registerFakeMu sync.Mutex

func ensureFakeRegistered(t *testing.T) {
	t.Helper()
	registerFakeMu.Lock()
	if err := vault.DefaultRegistry.Register(fake.NewFactory()); err != nil {
		registerFakeMu.Unlock()
		t.Fatalf("register fake backend: %v", err)
	}
	t.Cleanup(func() {
		_ = vault.DefaultRegistry.Unregister("fake")
		registerFakeMu.Unlock()
	})
}

// TestPickCredentialSyncSpec_AnonymousImplicitOptIn confirms that an
// anonymous [global.vault.provider] in the personal overlay is
// implicitly the credential-sync source: pickCredentialSyncSpec
// returns a non-nil spec with Name == "" and Kind/Config copied from
// the declaration.
func TestPickCredentialSyncSpec_AnonymousImplicitOptIn(t *testing.T) {
	g := config.GlobalOverride{
		Vault: &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{
				Kind:   "infisical",
				Config: map[string]any{"project": "uuid-anon"},
			},
		},
	}

	spec := pickCredentialSyncSpec(g)
	if spec == nil {
		t.Fatal("expected non-nil spec for anonymous provider")
	}
	if spec.Name != "" {
		t.Errorf("Name = %q, want empty", spec.Name)
	}
	if spec.Kind != "infisical" {
		t.Errorf("Kind = %q, want infisical", spec.Kind)
	}
	if got, _ := spec.Config["project"].(string); got != "uuid-anon" {
		t.Errorf("Config[project] = %q, want uuid-anon", got)
	}
}

// TestPickCredentialSyncSpec_NoVaultReturnsNil confirms that the
// router returns nil when the personal overlay declares no [vault]
// at all. Apply-time gating (apply.go Step 0.4) MUST treat nil as
// "feature disabled" and skip provider open + R9 validation.
func TestPickCredentialSyncSpec_NoVaultReturnsNil(t *testing.T) {
	g := config.GlobalOverride{}
	if spec := pickCredentialSyncSpec(g); spec != nil {
		t.Errorf("expected nil spec when Vault is unset, got %+v", spec)
	}
}

// TestPickCredentialSyncSpec_NamedOnlyReturnsNil confirms that named
// providers under [global.vault.providers.<name>] do NOT implicitly
// opt into credential sync. Only the anonymous [global.vault.provider]
// is treated as the credential-sync source; named providers stay
// URI-resolution-only.
func TestPickCredentialSyncSpec_NamedOnlyReturnsNil(t *testing.T) {
	g := config.GlobalOverride{
		Vault: &config.VaultRegistry{
			Providers: map[string]config.VaultProviderConfig{
				"personal": {
					Kind:   "infisical",
					Config: map[string]any{"project": "uuid-personal"},
				},
			},
		},
	}
	if spec := pickCredentialSyncSpec(g); spec != nil {
		t.Errorf("named-only providers must NOT trigger credential sync; got %+v", spec)
	}
}

// TestValidateCredentialSyncBootstrapPreOverlay_FileConflict covers
// PRD AC-25: a credential-sync (kind, project) that matches a local
// file entry triggers the chicken-and-egg error.
func TestValidateCredentialSyncBootstrapPreOverlay_FileConflict(t *testing.T) {
	file := []ProviderAuthEntry{
		{
			Kind: "infisical",
			Config: map[string]any{
				"project":       "uuid-X",
				"client_id":     "cid",
				"client_secret": "csec",
			},
		},
	}
	syncSpec := vault.ProviderSpec{
		Name:   "personal",
		Kind:   "infisical",
		Config: vault.ProviderConfig{"project": "uuid-X"},
	}

	err := validateCredentialSyncBootstrapPreOverlay(file, nil, syncSpec)
	if err == nil {
		t.Fatal("expected R9 error for file/syncSpec collision, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"chicken-and-egg", "kind=`infisical`", "uuid-X", "Personal vault provider"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q. Got: %v", want, err)
		}
	}
}

// TestChickenAndEggError_InfisicalHint confirms the diagnostic
// includes the `infisical login` hint when kind == "infisical".
func TestChickenAndEggError_InfisicalHint(t *testing.T) {
	err := chickenAndEggError("infisical", "uuid-Y")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "`infisical login` for Infisical") {
		t.Errorf("Infisical hint missing. Got: %v", err)
	}
}

// TestValidateCredentialSyncBootstrapPreOverlay_GlobalVaultConflict
// covers the case where a SECOND named provider in the global
// overlay's [global.vault.providers.*] declares the same
// (kind, project) as the credential-sync provider. The syncSpec
// itself MUST NOT trigger the check (it's necessarily declared
// in [global.vault.*]).
func TestValidateCredentialSyncBootstrapPreOverlay_GlobalVaultConflict(t *testing.T) {
	syncSpec := vault.ProviderSpec{
		Name:   "personal",
		Kind:   "infisical",
		Config: vault.ProviderConfig{"project": "uuid-X"},
	}
	globalVault := &config.VaultRegistry{
		Providers: map[string]config.VaultProviderConfig{
			"personal": {
				Kind:   "infisical",
				Config: map[string]any{"project": "uuid-X"},
			},
			"sibling": {
				Kind:   "infisical",
				Config: map[string]any{"project": "uuid-X"}, // collides!
			},
		},
	}

	err := validateCredentialSyncBootstrapPreOverlay(nil, globalVault, syncSpec)
	if err == nil {
		t.Fatal("expected R9 error for sibling/syncSpec collision in global overlay, got nil")
	}
	if !strings.Contains(err.Error(), "uuid-X") {
		t.Errorf("error message should mention the project. Got: %v", err)
	}
}

// TestValidateCredentialSyncBootstrapPreOverlay_SyncSpecSelfMatchAllowed
// confirms that the syncSpec itself, declared in
// [global.vault.providers.<from>], does NOT trigger the R9 check.
// Otherwise EVERY opt-in would fail.
func TestValidateCredentialSyncBootstrapPreOverlay_SyncSpecSelfMatchAllowed(t *testing.T) {
	syncSpec := vault.ProviderSpec{
		Name:   "personal",
		Kind:   "infisical",
		Config: vault.ProviderConfig{"project": "uuid-personal"},
	}
	globalVault := &config.VaultRegistry{
		Providers: map[string]config.VaultProviderConfig{
			"personal": {
				Kind:   "infisical",
				Config: map[string]any{"project": "uuid-personal"},
			},
		},
	}

	err := validateCredentialSyncBootstrapPreOverlay(nil, globalVault, syncSpec)
	if err != nil {
		t.Fatalf("syncSpec self-match must NOT trigger R9, got: %v", err)
	}
}

// TestValidateCredentialSyncBootstrapPreOverlay_AnonymousSelfAllowed
// is the anonymous-provider analog of self-match-allowed: the
// anonymous syncSpec (Name == "") must not trigger R9 against the
// [global.vault.provider] declaration that IS itself.
func TestValidateCredentialSyncBootstrapPreOverlay_AnonymousSelfAllowed(t *testing.T) {
	syncSpec := vault.ProviderSpec{
		Name:   "",
		Kind:   "infisical",
		Config: vault.ProviderConfig{"project": "uuid-anon"},
	}
	globalVault := &config.VaultRegistry{
		Provider: &config.VaultProviderConfig{
			Kind:   "infisical",
			Config: map[string]any{"project": "uuid-anon"},
		},
	}

	err := validateCredentialSyncBootstrapPreOverlay(nil, globalVault, syncSpec)
	if err != nil {
		t.Fatalf("anonymous syncSpec self-match must NOT trigger R9, got: %v", err)
	}
}

// TestValidateCredentialSyncBootstrapPostOverlay_OverlayConflict
// covers the post-overlay R9 stage: a workspace overlay vault spec
// with the same (kind, project) as the credential-sync provider
// triggers the chicken-and-egg error.
func TestValidateCredentialSyncBootstrapPostOverlay_OverlayConflict(t *testing.T) {
	syncSpec := vault.ProviderSpec{
		Name:   "personal",
		Kind:   "infisical",
		Config: vault.ProviderConfig{"project": "uuid-X"},
	}
	overlay := &config.VaultRegistry{
		Provider: &config.VaultProviderConfig{
			Kind:   "infisical",
			Config: map[string]any{"project": "uuid-X"},
		},
	}

	err := validateCredentialSyncBootstrapPostOverlay(overlay, syncSpec)
	if err == nil {
		t.Fatal("expected R9 error for overlay/syncSpec collision, got nil")
	}
	if !strings.Contains(err.Error(), "chicken-and-egg") {
		t.Errorf("error should reference chicken-and-egg. Got: %v", err)
	}
}

// TestValidateCredentialSyncBootstrapPostOverlay_NilIsNoOp confirms
// the nil-overlay case (workspace has no [vault] block) is a clean
// no-op — most workspaces today don't declare a vault, and we
// shouldn't fail them.
func TestValidateCredentialSyncBootstrapPostOverlay_NilIsNoOp(t *testing.T) {
	syncSpec := vault.ProviderSpec{
		Name:   "personal",
		Kind:   "infisical",
		Config: vault.ProviderConfig{"project": "uuid-X"},
	}
	if err := validateCredentialSyncBootstrapPostOverlay(nil, syncSpec); err != nil {
		t.Errorf("nil overlay should be a no-op, got: %v", err)
	}
}

// TestValidateCredentialSyncBootstrap_AC27 covers the positive
// case: no overlap, validation passes.
func TestValidateCredentialSyncBootstrap_AC27(t *testing.T) {
	syncSpec := vault.ProviderSpec{
		Name:   "personal",
		Kind:   "infisical",
		Config: vault.ProviderConfig{"project": "uuid-personal"},
	}
	file := []ProviderAuthEntry{
		{
			Kind: "infisical",
			Config: map[string]any{
				"project":       "uuid-team", // different project
				"client_id":     "cid",
				"client_secret": "csec",
			},
		},
	}
	globalVault := &config.VaultRegistry{
		Providers: map[string]config.VaultProviderConfig{
			"personal": {
				Kind:   "infisical",
				Config: map[string]any{"project": "uuid-personal"},
			},
		},
	}
	overlay := &config.VaultRegistry{
		Provider: &config.VaultProviderConfig{
			Kind:   "infisical",
			Config: map[string]any{"project": "uuid-team"}, // different from syncSpec
		},
	}

	if err := validateCredentialSyncBootstrapPreOverlay(file, globalVault, syncSpec); err != nil {
		t.Errorf("AC-27 pre-overlay should pass, got: %v", err)
	}
	if err := validateCredentialSyncBootstrapPostOverlay(overlay, syncSpec); err != nil {
		t.Errorf("AC-27 post-overlay should pass, got: %v", err)
	}
}

// TestOpenCredentialSyncProvider_DoesNotInjectToken is the structural
// R9 invariant test: openCredentialSyncProvider must NOT set
// Config["token"] on the spec. The credential pool's machine-identity
// entries cannot be used to authenticate the vault that supplies
// them — that would be the chicken-and-egg cycle R9 forbids.
//
// The test uses the fake backend (registered in init() of
// internal/vault/fake/) which doesn't require a real token; by the
// time the provider is opened, spec.Config["token"] should still be
// absent. Future-proofs against any accidental injection step
// being added inside openCredentialSyncProvider.
func TestOpenCredentialSyncProvider_DoesNotInjectToken(t *testing.T) {
	ensureFakeRegistered(t)
	// This test uses the "fake" backend kind because real Infisical
	// would require network access. The structural R9 invariant
	// (no token injection) is kind-agnostic — what matters is that
	// openCredentialSyncProvider does not write Config["token"]
	// before handing back the provider.
	g := config.GlobalOverride{
		Vault: &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{
				Kind:   "fake",
				Config: map[string]any{"project": "uuid-anon"},
			},
		},
	}

	syncSpec := pickCredentialSyncSpec(g)
	if syncSpec == nil {
		t.Fatal("pickCredentialSyncSpec returned nil for an anonymous provider")
	}
	bundle, prov, err := openCredentialSyncProvider(context.Background(), *syncSpec)
	if err != nil {
		t.Fatalf("openCredentialSyncProvider returned error: %v", err)
	}
	if bundle == nil {
		t.Fatal("bundle is nil")
	}
	defer bundle.CloseAll()

	if prov == nil {
		t.Fatal("provider is nil")
	}
	if _, ok := syncSpec.Config["token"]; ok {
		t.Errorf("syncSpec.Config[token] must NOT be set by openCredentialSyncProvider — that would re-introduce the R9 cycle")
	}
}
