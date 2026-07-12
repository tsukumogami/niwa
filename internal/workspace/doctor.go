// PRD R11 (DESIGN-niwa-onboard.md Phase 7): the onboard wizard's
// wizard-end check must reuse the exact credential-sync read topology
// `niwa apply`'s injectProviderTokens uses (Step 0.4 / providerauth.go),
// so the wizard and a later apply can never disagree about whether a
// credential resolves. CheckProviderAuth is the exported seam
// internal/onboard needs for that reuse: pickCredentialSyncSpec,
// openCredentialSyncProvider, vaultCredLoader, and parseProviderAuthBody
// (via CredentialPool.Lookup) are all unexported, so onboard -- a
// different package -- cannot drive them directly.
package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/tsukumogami/niwa/internal/config"
)

// ErrProviderAuthMissingEntry is CheckProviderAuth's failure when
// neither the local credential file nor the credential-sync vault has
// an entry for the checked (kind, project) pair. For the wizard-end
// check specifically -- verifying the pair the individual runner just
// wrote -- this silent CLI-session fallback (which is a perfectly
// normal, successful outcome for an *unrelated* pair at apply time) is
// itself the failure: the write should have landed a resolvable vault
// entry for exactly this pair.
var ErrProviderAuthMissingEntry = errors.New("no local-file or vault entry resolves this pair")

// ProviderAuthCheckResult is CheckProviderAuth's outcome for one
// (kind, project) pair.
type ProviderAuthCheckResult struct {
	Kind    string
	Project string
	// Source is the pool's Source classification for this pair
	// (SourceVault, SourceLocalFile, or SourceCLISession when no entry
	// resolved). Empty only when Err reflects a lookup-level failure
	// that never reached classification (a vault-unreachable error,
	// which still sets Source via the pool's own bookkeeping -- see
	// CredentialPool.Lookup).
	Source Source
	// Err is nil on a successful resolution. Non-nil names the nature
	// of the failure -- ErrProviderAuthMissingEntry, or whatever
	// parseProviderAuthBody / lookupVault produced (malformed body,
	// missing field, unsupported schema version, vault unreachable) --
	// never collapsed into a generic "check failed" message (PRD
	// AC-18b).
	Err error
}

// CheckProviderAuth runs the PRD R11 doctor-depth check for a single
// (kind, project) pair: the credential-sync vault provider declared in
// globalOverride's [global.vault.provider] is opened once
// (pickCredentialSyncSpec + openCredentialSyncProvider, the same
// helpers apply.go's Step 0.4 uses), joined into a CredentialPool with
// the eager local-file layer (~/.config/niwa/provider-auth.toml, via
// the existing LoadProviderAuth), and (kind, project) is looked up
// through it exactly as injectProviderTokens would.
//
// Self-exclusion (R9): the pool's vaultCredLoader carries
// SelfKind/SelfProject populated from the credential-sync provider's
// own spec, so lookupVault's existing dynamic self-guard applies here
// too -- this function does not re-implement that guard, it inherits
// it structurally, the same way apply.go's Step 0.4 wiring does.
// Calling this function with (kind, project) equal to the
// credential-sync provider's own pair is treated as a caller bug (a
// setup-level error, not a ProviderAuthCheckResult): R9's
// isSelfReferential guard in the individual runner already refuses to
// write that pair before this check would ever run against it.
//
// Because the pool is keyed by (kind, project) alone -- not by which
// of the three vault-registry sources (workspace overlay, team config,
// personal global overlay) declared the provider -- this one lookup
// is exactly what a real apply would produce for this pair regardless
// of which registry declares it. A blind sweep of every pair declared
// across all three registries was deliberately rejected: most
// declared pairs legitimately resolve via the CLI-session fallback
// (SourceCLISession) at apply time, which is success, not failure: only
// the specific pair the wizard just wrote needs to prove it resolves
// through the vault path.
//
// Returns a setup-level error (not a ProviderAuthCheckResult) only
// when the check itself cannot run: globalOverride is nil, the
// personal overlay declares no credential-sync provider, the provider
// can't be opened, or the self-referential caller-bug guard above
// fires. A per-pair resolution/parse/missing-entry failure is always
// reported IN the returned result, never via the error return.
func CheckProviderAuth(ctx context.Context, globalOverride *config.GlobalConfigOverride, kind, project string) (ProviderAuthCheckResult, error) {
	if globalOverride == nil {
		return ProviderAuthCheckResult{}, errors.New("workspace: CheckProviderAuth requires the personal-overlay global config override")
	}

	syncSpec := pickCredentialSyncSpec(globalOverride.Global)
	if syncSpec == nil {
		return ProviderAuthCheckResult{}, errors.New("workspace: personal overlay declares no [global.vault.provider]; nothing to verify")
	}

	syncProject, _ := syncSpec.Config["project"].(string)
	if kind == syncSpec.Kind && project == syncProject {
		return ProviderAuthCheckResult{}, fmt.Errorf(
			"workspace: CheckProviderAuth called with the credential-sync provider's own (kind=%q, project=%q); R9 already refuses to write this pair, so verifying it here is a caller error",
			kind, project,
		)
	}

	syncBundle, syncProvider, err := openCredentialSyncProvider(ctx, *syncSpec)
	if err != nil {
		return ProviderAuthCheckResult{}, err
	}
	defer syncBundle.CloseAll()

	var fileEntries []ProviderAuthEntry
	if dir, dirErr := NiwaConfigDir(); dirErr == nil {
		entries, loadErr := LoadProviderAuth(dir)
		if loadErr != nil {
			return ProviderAuthCheckResult{}, loadErr
		}
		fileEntries = entries
	}

	loader := &vaultCredLoader{
		Provider:     syncProvider,
		ProviderName: syncSpec.Name,
		PathPrefix:   CredentialSyncPathPrefix,
		SelfKind:     syncSpec.Kind,
		SelfProject:  syncProject,
	}
	pool := NewCredentialPool(fileEntries, loader)

	entry, rec, lookupErr := pool.Lookup(ctx, kind, project)
	result := ProviderAuthCheckResult{Kind: kind, Project: project, Source: rec.Source}
	if lookupErr != nil {
		result.Err = lookupErr
		return result, nil
	}
	if entry == nil {
		result.Err = fmt.Errorf("%w: (kind=%s, project=%s)", ErrProviderAuthMissingEntry, kind, project)
		return result, nil
	}
	return result, nil
}
