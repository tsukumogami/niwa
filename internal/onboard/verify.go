// PRD R11 (DESIGN-niwa-onboard.md Phase 7): the individual-setup
// wizard-end verification. This is a "doctor-depth" shape-and-
// resolution check, deliberately distinct from R9 (individual.go's
// verifyMintedPair, a real authentication exchange run at mint time,
// before the store) and from R21 (team.go's verifyR21, the team-phase
// landing-check sweep). Every message this file produces is prefixed
// "R11" so a caller can never confuse it with either of those (AC-18).
package onboard

import (
	"context"
	"fmt"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// VerifyIndividualParams collects VerifyIndividual's inputs: the
// personal overlay's parsed niwa.toml (to locate and open the
// credential-sync provider, exactly as workspace.CheckProviderAuth
// requires), the (kind, project) pair the individual runner just
// stored a credential for, and -- when the caller has them loaded --
// the team config and workspace overlay vault registries, so the R11
// check can sweep all three vault-registry sources per the design,
// not just the just-written pair. Either or both may be nil when the
// caller doesn't have that registry loaded; the sweep simply
// contributes nothing for a nil registry.
type VerifyIndividualParams struct {
	GlobalOverride *config.GlobalConfigOverride
	TeamVault      *config.VaultRegistry
	OverlayVault   *config.VaultRegistry
	Kind           string
	Project        string
}

// VerifyIndividual runs the PRD R11 wizard-end check by delegating to
// workspace.CheckProviderAuth -- the exact credential-sync read
// topology `niwa apply` uses (one provider opened once, the
// credential pool swept across the three vault-registry sources, the
// shared parseProviderAuthBody validator, R9 self-exclusion inherited
// structurally) -- so the wizard and a later apply can never disagree
// about whether the just-stored credential, or any other declared
// credential-sync pair, resolves.
//
// Returns nil on success. Returns *ExitCodeError{Code: ExitVerification}
// on any other outcome, naming the failing (kind, project), its
// source, and the nature of the failure -- missing entry, malformed
// body, missing field, unsupported schema version, or vault
// unreachable -- exactly as workspace.CheckProviderAuth reports it,
// never collapsed into a bare "verification failed" message (AC-18b).
// The just-stored target pair is checked first and takes priority in
// the returned message; when the target resolves cleanly but the
// sweep of the other two vault-registry sources found an unrelated
// pair with a genuinely broken (not merely CLI-session-fallback)
// resolved body, that failure is reported instead, still R11-prefixed.
// A setup-level failure (no credential-sync provider declared,
// provider unreachable) is reported through the same typed error:
// there's nothing meaningful to distinguish it from a verification
// failure at the wizard-end check's own boundary.
func VerifyIndividual(ctx context.Context, p VerifyIndividualParams) error {
	result, err := workspace.CheckProviderAuth(ctx, p.GlobalOverride, p.TeamVault, p.OverlayVault, p.Kind, p.Project)
	if err != nil {
		return &ExitCodeError{
			Code: ExitVerification,
			Msg:  fmt.Sprintf("R11 individual verification failed: %v", err),
		}
	}
	if result.Target.Err != nil {
		return &ExitCodeError{
			Code: ExitVerification,
			Msg: fmt.Sprintf("R11 individual verification failed: (kind=%s, project=%s) source=%s: %v",
				result.Target.Kind, result.Target.Project, sourceOrUnknown(result.Target.Source), result.Target.Err),
		}
	}
	if len(result.OtherFailures) > 0 {
		f := result.OtherFailures[0]
		return &ExitCodeError{
			Code: ExitVerification,
			Msg: fmt.Sprintf("R11 individual verification failed: declared credential-sync pair (kind=%s, project=%s) source=%s: %v",
				f.Kind, f.Project, sourceOrUnknown(f.Source), f.Err),
		}
	}
	return nil
}

// sourceOrUnknown renders a workspace.Source for the R11 failure
// message, substituting "unknown" for the empty value (a source that
// was never classified, e.g. a setup-level failure before the pool
// ever ran a Lookup).
func sourceOrUnknown(s workspace.Source) string {
	if s == "" {
		return "unknown"
	}
	return string(s)
}
