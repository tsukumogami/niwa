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
// requires) and the (kind, project) pair the individual runner just
// stored a credential for.
type VerifyIndividualParams struct {
	GlobalOverride *config.GlobalConfigOverride
	Kind           string
	Project        string
}

// VerifyIndividual runs the PRD R11 wizard-end check by delegating to
// workspace.CheckProviderAuth -- the exact credential-sync read
// topology `niwa apply` uses (one provider opened once, the
// credential pool, the shared parseProviderAuthBody validator, R9
// self-exclusion inherited structurally) -- so the wizard and a later
// apply can never disagree about whether the just-stored credential
// resolves.
//
// Returns nil on success. Returns *ExitCodeError{Code: ExitVerification}
// on any other outcome, naming the failing (kind, project), its
// source, and the nature of the failure -- missing entry, malformed
// body, missing field, unsupported schema version, or vault
// unreachable -- exactly as workspace.CheckProviderAuth reports it,
// never collapsed into a bare "verification failed" message (AC-18b).
// A setup-level failure (no credential-sync provider declared,
// provider unreachable) is reported through the same typed error:
// there's nothing meaningful to distinguish it from a verification
// failure at the wizard-end check's own boundary.
func VerifyIndividual(ctx context.Context, p VerifyIndividualParams) error {
	result, err := workspace.CheckProviderAuth(ctx, p.GlobalOverride, p.Kind, p.Project)
	if err != nil {
		return &ExitCodeError{
			Code: ExitVerification,
			Msg:  fmt.Sprintf("R11 individual verification failed: %v", err),
		}
	}
	if result.Err != nil {
		return &ExitCodeError{
			Code: ExitVerification,
			Msg: fmt.Sprintf("R11 individual verification failed: (kind=%s, project=%s) source=%s: %v",
				result.Kind, result.Project, sourceOrUnknown(result.Source), result.Err),
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
