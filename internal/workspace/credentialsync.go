package workspace

import (
	"context"
	"fmt"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/vault"
)

// pickCredentialSyncSpec returns the ProviderSpec for the credential-
// sync vault provider declared in the personal overlay, or nil when
// the overlay declares no anonymous [global.vault.provider]. The spec
// is synthesized from the [global.vault.provider] declaration the user
// has already authored — this function is a router, not a parser.
//
// Resolution rule:
//   - g.Vault == nil OR g.Vault.Provider == nil → returns nil. The
//     overlay either declares no vault at all, or declares only named
//     providers (used for URI resolution but not for credential-sync
//     bootstrap).
//   - g.Vault.Provider != nil → the anonymous provider is the
//     credential-sync source. Spec.Name == "".
//
// Named providers under [global.vault.providers.<name>] participate in
// vault:// URI resolution but never serve as the credential-sync
// source. Treating an anonymous provider as the implicit opt-in keeps
// the personal-overlay shape simple: declaring the provider once is
// the user's intent to both resolve URIs from it AND bootstrap
// credentials from it.
func pickCredentialSyncSpec(g config.GlobalOverride) *vault.ProviderSpec {
	if g.Vault == nil || g.Vault.Provider == nil {
		return nil
	}
	return &vault.ProviderSpec{
		Name:   "",
		Kind:   g.Vault.Provider.Kind,
		Config: vault.ProviderConfig(g.Vault.Provider.Config),
		Source: "global overlay",
	}
}

// openCredentialSyncProvider opens the personal-overlay credential-
// sync vault provider via the standard registry pipeline and returns
// the bundle (so the caller can defer Bundle.CloseAll) plus the
// opened provider. The caller passes the syncSpec it already
// resolved via pickCredentialSyncSpec so we don't compute it twice.
//
// Critical security boundary (PRD R9): this function deliberately
// does NOT call injectProviderTokens against the spec. The credential
// pool's machine-identity entries cannot be used to authenticate the
// vault that supplies them — that would be the chicken-and-egg cycle
// R9 forbids. By skipping token injection here, spec.Config["token"]
// stays unset, and the backend's factory.Open falls through to its
// CLI-session auth path (Infisical: the active `infisical login`
// session). This is the structural enforcement; the explicit
// validateCredentialSyncBootstrap{Pre,Post}Overlay calls are belt
// alongside.
func openCredentialSyncProvider(ctx context.Context, syncSpec vault.ProviderSpec) (*vault.Bundle, vault.Provider, error) {
	bundle, err := vault.DefaultRegistry.Build(ctx, []vault.ProviderSpec{syncSpec})
	if err != nil {
		return nil, nil, fmt.Errorf("opening credential-sync vault provider: %w", err)
	}
	prov, err := bundle.Get(syncSpec.Name)
	if err != nil {
		// Build succeeded but Get failed — close the bundle to
		// release the just-opened provider, then surface the error.
		_ = bundle.CloseAll()
		return nil, nil, fmt.Errorf("retrieving credential-sync vault provider from bundle: %w", err)
	}
	return bundle, prov, nil
}

// vaultProviderConfigMatchesKindProject returns true when the given
// VaultProviderConfig collides with the syncSpec under R9's
// (kind, project) identity rule. The rule is gated on kind to match
// MatchProviderAuth (providerauth.go:102): only kinds that identify
// providers by project participate in the project-comparison check.
//
// Today's only such kind is "infisical". Future backends that
// identify providers by some other key shape (e.g., a sops keyfile
// path) must extend this switch alongside MatchProviderAuth — the
// two helpers walk in lockstep and any divergence would let a
// chicken-and-egg cycle slip past R9.
func vaultProviderConfigMatchesKindProject(p config.VaultProviderConfig, syncSpec vault.ProviderSpec) bool {
	if p.Kind != syncSpec.Kind {
		return false
	}
	switch p.Kind {
	case "infisical":
		syncProject, _ := syncSpec.Config["project"].(string)
		pProject, _ := p.Config["project"].(string)
		return syncProject != "" && pProject == syncProject
	default:
		// Unknown kinds: refuse to assume a matching key shape. The
		// alternative (blindly comparing Config["project"]) would
		// false-positive on backends that don't use "project" at all
		// or that store an unrelated value at that key. Future
		// backends MUST extend this switch.
		return false
	}
}

// validateCredentialSyncBootstrapPreOverlay runs the first stage of
// the PRD R9 chicken-and-egg check: the credential-sync provider's
// (kind, project) MUST NOT match any local-file provider-auth entry,
// and MUST NOT match any vault spec declared in the personal
// overlay's own [global.vault.*] registry (excluding the syncSpec
// itself, which is necessarily declared in [global.vault.*]).
//
// Why two stages? The workspace overlay's vault specs aren't parsed
// until Step 0.5/0.6 of the apply pipeline, so we run the
// "everything-but-overlay" check now and the "overlay only" check
// after the parse. Both stages produce the same R9 diagnostic.
//
// This stage's input set: the file entries plus globalVault's specs.
func validateCredentialSyncBootstrapPreOverlay(file []ProviderAuthEntry, globalVault *config.VaultRegistry, syncSpec vault.ProviderSpec) error {
	syncProject, _ := syncSpec.Config["project"].(string)
	// Stage 1a: scan the local credential file. ProviderAuthEntry's
	// Config carries the same "project" key shape as a vault spec,
	// so we can build a synthetic VaultProviderConfig and reuse the
	// shared predicate.
	for _, entry := range file {
		synthetic := config.VaultProviderConfig{Kind: entry.Kind, Config: entry.Config}
		if vaultProviderConfigMatchesKindProject(synthetic, syncSpec) {
			return chickenAndEggError(syncSpec.Kind, syncProject)
		}
	}
	// Stage 1b: scan the global override's own vault specs, but
	// SKIP the syncSpec itself (it's necessarily declared there).
	if globalVault != nil {
		// Anonymous syncSpec self-match: globalVault.Provider IS the
		// syncSpec when syncSpec.Name == "", so skip the anonymous
		// slot in that case.
		if globalVault.Provider != nil && syncSpec.Name != "" {
			if vaultProviderConfigMatchesKindProject(*globalVault.Provider, syncSpec) {
				return chickenAndEggError(syncSpec.Kind, syncProject)
			}
		}
		for name, p := range globalVault.Providers {
			if name == syncSpec.Name {
				continue // the named syncSpec itself
			}
			if vaultProviderConfigMatchesKindProject(p, syncSpec) {
				return chickenAndEggError(syncSpec.Kind, syncProject)
			}
		}
	}
	return nil
}

// validateCredentialSyncBootstrapPostOverlay runs the second stage
// of the PRD R9 chicken-and-egg check, against the workspace
// overlay's vault specs (the layer not visible at Step 0.4). The
// overlay can never legitimately declare the same (kind, project)
// as the credential-sync provider — that would mean the workspace's
// runtime credentials and the bootstrap credentials are sourced
// from the same vault instance, which is the cycle R9 forbids.
//
// The credential-sync provider has been opened by the time this
// runs but no Resolve calls have been made; on failure, the caller's
// existing defer Bundle.CloseAll() releases it cleanly.
func validateCredentialSyncBootstrapPostOverlay(overlayVault *config.VaultRegistry, syncSpec vault.ProviderSpec) error {
	if overlayVault == nil {
		return nil
	}
	syncProject, _ := syncSpec.Config["project"].(string)
	if overlayVault.Provider != nil {
		if vaultProviderConfigMatchesKindProject(*overlayVault.Provider, syncSpec) {
			return chickenAndEggError(syncSpec.Kind, syncProject)
		}
	}
	for _, p := range overlayVault.Providers {
		if vaultProviderConfigMatchesKindProject(p, syncSpec) {
			return chickenAndEggError(syncSpec.Kind, syncProject)
		}
	}
	return nil
}

// chickenAndEggError returns the verbatim PRD R9 diagnostic. The
// wording is fixed by the PRD; user-visible diagnostic. Note: the
// kind-specific CLI hint defaults to Infisical's `infisical login`
// today since that's the only registered backend; future backends
// can extend this with a switch on kind.
func chickenAndEggError(kind, project string) error {
	cliHint := fmt.Sprintf("`infisical login` for Infisical")
	if kind != "infisical" {
		cliHint = fmt.Sprintf("the %s CLI's session credentials", kind)
	}
	return fmt.Errorf(
		"Personal vault provider (kind=`%s`, project=`%s`) cannot be bootstrapped by an entry in the local credential pool — this would create a chicken-and-egg cycle. Authenticate the personal vault via CLI session (%s) instead.",
		kind, project, cliHint,
	)
}
