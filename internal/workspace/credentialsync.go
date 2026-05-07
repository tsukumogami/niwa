package workspace

import (
	"context"
	"fmt"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/vault"
)

// pickCredentialSyncSpec returns the ProviderSpec for the credential-
// sync vault provider declared in the personal overlay. The spec is
// synthesized from the [global.vault.*] declaration the user has
// already authored — this function is a router, not a parser.
//
// Resolution rule (PRD R1, R2):
//   - mi.From == "":   use the anonymous [global.vault.provider].
//                      Spec.Name == "".
//   - mi.From == "n":  use [global.vault.providers.n]. Spec.Name == "n".
//
// R2 (unknown-provider name) is enforced at parse time by
// internal/config.validatePersonalOverlayMachineIdentities, so when
// mi.From != "" the named provider is guaranteed to exist. The
// defensive errors below cover paths where the GlobalOverride has
// been mutated post-parse (which shouldn't happen in production).
func pickCredentialSyncSpec(g config.GlobalOverride, mi *config.MachineIdentitiesConfig) (vault.ProviderSpec, error) {
	if mi == nil {
		return vault.ProviderSpec{}, fmt.Errorf("internal error: pickCredentialSyncSpec called with nil MachineIdentitiesConfig")
	}
	if g.Vault == nil {
		return vault.ProviderSpec{}, fmt.Errorf("internal error: pickCredentialSyncSpec called with nil Vault registry")
	}
	if mi.From == "" {
		if g.Vault.Provider == nil {
			return vault.ProviderSpec{}, fmt.Errorf("internal error: pickCredentialSyncSpec called with anonymous from but no [global.vault.provider]")
		}
		return vault.ProviderSpec{
			Name:   "",
			Kind:   g.Vault.Provider.Kind,
			Config: vault.ProviderConfig(g.Vault.Provider.Config),
			Source: "global overlay",
		}, nil
	}
	p, ok := g.Vault.Providers[mi.From]
	if !ok {
		return vault.ProviderSpec{}, fmt.Errorf("internal error: pickCredentialSyncSpec named provider %q missing from [global.vault.providers] (R2 should have caught this at parse time)", mi.From)
	}
	cfg := vault.ProviderConfig(p.Config)
	if cfg == nil {
		cfg = vault.ProviderConfig{}
	}
	if _, has := cfg["name"]; !has {
		cfg["name"] = mi.From
	}
	return vault.ProviderSpec{
		Name:   mi.From,
		Kind:   p.Kind,
		Config: cfg,
		Source: "global overlay",
	}, nil
}

// openCredentialSyncProvider opens the personal-overlay credential-
// sync vault provider via the standard registry pipeline and returns
// the bundle (so the caller can defer Bundle.CloseAll), the opened
// provider, and the spec used to open it.
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
func openCredentialSyncProvider(ctx context.Context, g config.GlobalOverride, mi *config.MachineIdentitiesConfig) (*vault.Bundle, vault.Provider, vault.ProviderSpec, error) {
	spec, err := pickCredentialSyncSpec(g, mi)
	if err != nil {
		return nil, nil, vault.ProviderSpec{}, err
	}
	bundle, err := vault.DefaultRegistry.Build(ctx, []vault.ProviderSpec{spec})
	if err != nil {
		return nil, nil, spec, fmt.Errorf("opening credential-sync vault provider: %w", err)
	}
	prov, err := bundle.Get(spec.Name)
	if err != nil {
		// Build succeeded but Get failed — close the bundle to
		// release the just-opened provider, then surface the error.
		_ = bundle.CloseAll()
		return nil, nil, spec, fmt.Errorf("retrieving credential-sync vault provider from bundle: %w", err)
	}
	return bundle, prov, spec, nil
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
	// Stage 1a: scan the local credential file.
	for _, entry := range file {
		if entry.Kind != syncSpec.Kind {
			continue
		}
		entryProject, _ := entry.Config["project"].(string)
		if entryProject == syncProject {
			return chickenAndEggError(syncSpec.Kind, syncProject)
		}
	}
	// Stage 1b: scan the global override's own vault specs, but
	// SKIP the syncSpec itself (it's necessarily declared there).
	if globalVault != nil {
		if globalVault.Provider != nil && syncSpec.Name == "" {
			// The anonymous syncSpec IS Provider — skip.
		} else if globalVault.Provider != nil {
			project, _ := globalVault.Provider.Config["project"].(string)
			if globalVault.Provider.Kind == syncSpec.Kind && project == syncProject {
				return chickenAndEggError(syncSpec.Kind, syncProject)
			}
		}
		for name, p := range globalVault.Providers {
			if name == syncSpec.Name {
				continue // the named syncSpec itself
			}
			project, _ := p.Config["project"].(string)
			if p.Kind == syncSpec.Kind && project == syncProject {
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
		project, _ := overlayVault.Provider.Config["project"].(string)
		if overlayVault.Provider.Kind == syncSpec.Kind && project == syncProject {
			return chickenAndEggError(syncSpec.Kind, syncProject)
		}
	}
	for _, p := range overlayVault.Providers {
		project, _ := p.Config["project"].(string)
		if p.Kind == syncSpec.Kind && project == syncProject {
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
