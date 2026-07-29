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
// an entry for the checked (kind, project) pair. For the target pair
// specifically -- the one the individual runner just wrote -- this
// silent CLI-session fallback (which is a perfectly normal, successful
// outcome for an *unrelated* pair at apply time) is itself the
// failure: the write should have landed a resolvable vault entry for
// exactly this pair.
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
	// of the failure -- ErrProviderAuthMissingEntry (Target only, see
	// CheckProviderAuthResult), or whatever parseProviderAuthBody /
	// lookupVault produced (malformed body, missing field, unsupported
	// schema version, vault unreachable) -- never collapsed into a
	// generic "check failed" message (PRD AC-18b).
	Err error
}

// CheckProviderAuthResult is CheckProviderAuth's full outcome.
type CheckProviderAuthResult struct {
	// Target is the check for the specific (kind, project) pair the
	// caller asked about -- the individual runner's own just-stored
	// pair. Unlike a pair discovered only via OtherPairs below, a
	// missing entry here (falling through to CLI-session, no local-
	// file or vault entry at all) IS a failure: this pair was just
	// written, and the write should have landed a resolvable vault
	// entry.
	Target ProviderAuthCheckResult
	// OtherFailures holds any additional (kind, project) pairs
	// discovered across the three vault-registry sources (team config,
	// workspace overlay, personal global overlay) whose resolved body
	// failed to parse (malformed, missing field, unsupported schema
	// version). Unlike Target, a swept pair that simply falls through
	// to CLI-session (no local-file or vault entry at all) is NOT
	// included here, and neither is a vault-unreachable condition on
	// it (isSoftenable, PRD R13.1) -- both are the normal,
	// successful-or-tolerated outcomes apply.go's injectProviderTokens
	// already accepts for any pair it isn't specifically responsible
	// for proving. Only a hard, non-softenable parse failure is
	// reported here; see checkSweptPair for the exact rule.
	OtherFailures []ProviderAuthCheckResult
}

// kindProjectPair is the enumeration unit CheckProviderAuth sweeps
// across the three vault-registry sources.
type kindProjectPair struct {
	Kind    string
	Project string
}

// pairsFromRegistry extracts every (kind, project) pair declared in a
// VaultRegistry (the anonymous [vault.provider] and every named
// [vault.providers.<name>]). Mirrors injectProviderTokens's own
// anonymous+named walk (providerauth.go) so the enumeration visits
// exactly the specs apply.go's three injectProviderTokens call sites
// would. A provider whose Config carries no "project" string
// contributes nothing -- only kinds that identify by project
// participate in this pool (matching
// vaultProviderConfigMatchesKindProject's own gating).
func pairsFromRegistry(vr *config.VaultRegistry) []kindProjectPair {
	if vr == nil {
		return nil
	}
	var out []kindProjectPair
	if vr.Provider != nil {
		if project, ok := vr.Provider.Config["project"].(string); ok && project != "" {
			out = append(out, kindProjectPair{Kind: vr.Provider.Kind, Project: project})
		}
	}
	for _, p := range vr.Providers {
		if project, ok := p.Config["project"].(string); ok && project != "" {
			out = append(out, kindProjectPair{Kind: p.Kind, Project: project})
		}
	}
	return out
}

// CheckProviderAuth runs the PRD R11 doctor-depth check: the
// credential-sync vault provider declared in globalOverride's
// [global.vault.provider] is opened once (pickCredentialSyncSpec +
// openCredentialSyncProvider, the same helpers apply.go's Step 0.4
// uses), joined into a CredentialPool with the eager local-file layer
// (~/.config/niwa/provider-auth.toml, via the existing
// LoadProviderAuth), and (kind, project) -- the pair the individual
// runner just stored a credential for -- is looked up through it
// exactly as injectProviderTokens would.
//
// The pool is then swept across the three vault-registry sources
// (teamVault, overlayVault, and globalOverride.Global.Vault itself --
// workspace overlay, team config, and personal global overlay,
// matching apply.go's three injectProviderTokens call sites exactly),
// applying the SAME graceful semantics injectProviderTokens/isSoftenable
// already use in production (see checkSweptPair): a pair with no
// local-file or vault entry at all silently falls through to
// CLI-session (not a failure -- most declared pairs legitimately
// resolve this way), a vault-unreachable condition on it is likewise
// tolerated (isSoftenable, PRD R13.1 -- a transient blip on a pair
// this function isn't specifically responsible for proving), and only
// a pair whose vault entry resolved but failed to parse (malformed
// body, missing field, unsupported schema version) is reported as a
// failure. Either teamVault or overlayVault may be nil (or both) when
// the caller doesn't have that registry loaded; the sweep simply
// contributes nothing for a nil registry, and the target-pair check
// below is unaffected.
//
// Self-exclusion (R9): the pool's vaultCredLoader carries
// SelfKind/SelfProject populated from the credential-sync provider's
// own spec, so lookupVault's existing dynamic self-guard applies to
// both the target check and the sweep -- this function does not
// re-implement that guard, it inherits it structurally, the same way
// apply.go's Step 0.4 wiring does. Calling this function with the
// target (kind, project) equal to the credential-sync provider's own
// pair is treated as a caller bug (a setup-level error, not a
// ProviderAuthCheckResult): R9's isSelfReferential guard in the
// individual runner already refuses to write that pair before this
// check would ever run against it.
//
// Returns a setup-level error (not a CheckProviderAuthResult) only
// when the check itself cannot run: globalOverride is nil, the
// personal overlay declares no credential-sync provider, the provider
// can't be opened, or the self-referential caller-bug guard above
// fires. A per-pair resolution/parse/missing-entry failure is always
// reported IN the returned result, never via the error return.
func CheckProviderAuth(ctx context.Context, globalOverride *config.GlobalConfigOverride, teamVault, overlayVault *config.VaultRegistry, kind, project string) (CheckProviderAuthResult, error) {
	if globalOverride == nil {
		return CheckProviderAuthResult{}, errors.New("workspace: CheckProviderAuth requires the personal-overlay global config override")
	}

	syncSpec := pickCredentialSyncSpec(globalOverride.Global)
	if syncSpec == nil {
		return CheckProviderAuthResult{}, errors.New("workspace: personal overlay declares no [global.vault.provider]; nothing to verify")
	}

	syncProject, _ := syncSpec.Config["project"].(string)
	if kind == syncSpec.Kind && project == syncProject {
		return CheckProviderAuthResult{}, fmt.Errorf(
			"workspace: CheckProviderAuth called with the credential-sync provider's own (kind=%q, project=%q); R9 already refuses to write this pair, so verifying it here is a caller error",
			kind, project,
		)
	}

	syncBundle, syncProvider, err := openCredentialSyncProvider(ctx, *syncSpec)
	if err != nil {
		return CheckProviderAuthResult{}, err
	}
	defer syncBundle.CloseAll()

	var fileEntries []ProviderAuthEntry
	if dir, dirErr := NiwaConfigDir(); dirErr == nil {
		entries, loadErr := LoadProviderAuth(dir)
		if loadErr != nil {
			return CheckProviderAuthResult{}, loadErr
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

	result := CheckProviderAuthResult{
		Target: checkTargetPair(ctx, pool, kind, project),
	}

	// Sweep the three vault-registry sources. seen dedupes across
	// sources (the same pair can legitimately be declared in more than
	// one registry) and excludes the target pair (already checked
	// above, under stricter semantics) and the credential-sync
	// provider's own pair (R9 self-exclusion; the pool's dynamic guard
	// would refuse this Lookup anyway, but skipping it here avoids
	// relying on that alone).
	seen := map[kindProjectPair]bool{
		{Kind: kind, Project: project}:              true,
		{Kind: syncSpec.Kind, Project: syncProject}: true,
	}
	for _, vr := range []*config.VaultRegistry{teamVault, overlayVault, globalOverride.Global.Vault} {
		for _, pair := range pairsFromRegistry(vr) {
			if seen[pair] {
				continue
			}
			seen[pair] = true
			if failure, isFailure := checkSweptPair(ctx, pool, pair); isFailure {
				result.OtherFailures = append(result.OtherFailures, failure)
			}
		}
	}

	return result, nil
}

// checkTargetPair looks up the target (kind, project) pair with
// strict semantics: a missing entry (falls through to CLI-session) IS
// a failure, since this is specifically the pair the wizard just
// wrote and expects to resolve through the vault path.
func checkTargetPair(ctx context.Context, pool *CredentialPool, kind, project string) ProviderAuthCheckResult {
	entry, rec, lookupErr := pool.Lookup(ctx, kind, project)
	result := ProviderAuthCheckResult{Kind: kind, Project: project, Source: rec.Source}
	if lookupErr != nil {
		result.Err = lookupErr
		return result
	}
	if entry == nil {
		result.Err = fmt.Errorf("%w: (kind=%s, project=%s)", ErrProviderAuthMissingEntry, kind, project)
		return result
	}
	return result
}

// checkSweptPair looks up a pair discovered by the three-registry
// sweep with graceful semantics, mirroring injectProviderTokens's own
// isSoftenable tolerance exactly (not merely in spirit): a missing
// entry (no local-file or vault entry -- falls through to CLI-session)
// is NOT a failure, and neither is a softenable lookup error (a
// vault-unreachable condition on a pair the wizard isn't specifically
// responsible for proving, PRD R13.1) -- both are the normal,
// successful-or-tolerated outcomes apply.go's real production path
// already accepts for any pair besides the one it's actively
// resolving. Only a hard lookup-level error (a resolved-but-malformed
// body, a missing field, or an unsupported schema version) is
// reported. Returns isFailure=false for a clean resolution, a graceful
// fallthrough, or a softened vault-unreachable error; isFailure=true
// only for a hard, non-softenable lookupErr.
func checkSweptPair(ctx context.Context, pool *CredentialPool, pair kindProjectPair) (ProviderAuthCheckResult, bool) {
	_, rec, lookupErr := pool.Lookup(ctx, pair.Kind, pair.Project)
	if lookupErr == nil || isSoftenable(lookupErr) {
		return ProviderAuthCheckResult{}, false
	}
	return ProviderAuthCheckResult{Kind: pair.Kind, Project: pair.Project, Source: rec.Source, Err: lookupErr}, true
}
