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

// TestPickCredentialSyncSpec_Anonymous selects the
// [global.vault.provider] (anonymous) when From == "".
func TestPickCredentialSyncSpec_Anonymous(t *testing.T) {
	g := config.GlobalOverride{
		Vault: &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{
				Kind:   "infisical",
				Config: map[string]any{"project": "uuid-anon"},
			},
		},
	}
	mi := &config.MachineIdentitiesConfig{From: ""}

	spec, err := pickCredentialSyncSpec(g, mi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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

// TestPickCredentialSyncSpec_DoesNotMutateInput confirms the
// "router, not parser" contract: pickCredentialSyncSpec MUST NOT
// write back to the GlobalOverride's vault provider Config map.
// A regression here would mean an opt-in apply silently mutates
// the user's parsed config in memory — a hidden side effect that
// would break the v3-snapshot-then-reload pattern.
func TestPickCredentialSyncSpec_DoesNotMutateInput(t *testing.T) {
	originalConfig := map[string]any{"project": "uuid-personal"}
	g := config.GlobalOverride{
		Vault: &config.VaultRegistry{
			Providers: map[string]config.VaultProviderConfig{
				"personal": {
					Kind:   "infisical",
					Config: originalConfig,
				},
			},
		},
	}
	mi := &config.MachineIdentitiesConfig{From: "personal"}

	spec, err := pickCredentialSyncSpec(g, mi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Returned spec.Config should carry "name" → "personal" (the
	// vault factory expects it).
	if got, _ := spec.Config["name"].(string); got != "personal" {
		t.Errorf("spec.Config[name] = %q, want %q", got, "personal")
	}
	// Original config map should NOT have gained a "name" entry.
	if _, has := originalConfig["name"]; has {
		t.Errorf("pickCredentialSyncSpec must not mutate the input Config; got name=%v", originalConfig["name"])
	}
	// And the original map should still have just one key (project).
	if len(originalConfig) != 1 {
		t.Errorf("input Config map size = %d, want 1 (was mutated)", len(originalConfig))
	}
}

// TestPickCredentialSyncSpec_Named selects [global.vault.providers.<n>]
// when From is non-empty.
func TestPickCredentialSyncSpec_Named(t *testing.T) {
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
	mi := &config.MachineIdentitiesConfig{From: "personal"}

	spec, err := pickCredentialSyncSpec(g, mi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Name != "personal" {
		t.Errorf("Name = %q, want personal", spec.Name)
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
	mi := &config.MachineIdentitiesConfig{From: ""}

	syncSpec, err := pickCredentialSyncSpec(g, mi)
	if err != nil {
		t.Fatalf("pickCredentialSyncSpec returned error: %v", err)
	}
	bundle, prov, err := openCredentialSyncProvider(context.Background(), syncSpec)
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
