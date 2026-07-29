// Config-sourcing helpers for the CLI wiring layer (Issue 9's
// amendment: internal/cli/onboard.go builds Options.Team/Individual/
// Verify/Preconditions from real workspace configuration rather than
// leaving them nil). Kept in this package, not internal/cli, because
// they operate on the same config.VaultRegistry/GlobalConfigOverride
// shapes the rest of this package already imports.
package onboard

import (
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/vault"
)

// PickCredentialSyncSpec returns the ProviderSpec for the credential-
// sync vault provider declared in the personal overlay's
// [global.vault.provider] block, or nil when the overlay declares no
// anonymous provider (only named [global.vault.providers.<name>]
// entries, or no vault block at all).
//
// This deliberately duplicates internal/workspace/credentialsync.go's
// unexported pickCredentialSyncSpec rather than exporting that one:
// the codebase's established convention (see individual.go's
// isSelfReferential and credentialSyncKeyPrefix, team.go's
// defaultDestinationEnv) is to duplicate a small, stable predicate
// across this package boundary rather than widen workspace's exported
// surface for a single external caller. Any change to the resolution
// rule must be made in both places.
func PickCredentialSyncSpec(g config.GlobalOverride) *vault.ProviderSpec {
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
