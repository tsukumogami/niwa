package onboard

import (
	"context"
	"errors"
	"fmt"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault/infisical"
)

// SetupPhase names which half of the wizard should run, per Decision
// 3's team/individual/verify-only funnel.
type SetupPhase int

const (
	PhaseUnknown SetupPhase = iota
	PhaseTeam
	PhaseIndividual
	// PhaseVerifyOnly is the R15 shortcut: the identity is found and
	// the personal overlay already declares a resolving credential, so
	// the wizard goes straight to R11 verification instead of running
	// either setup phase.
	PhaseVerifyOnly
)

func (p SetupPhase) String() string {
	switch p {
	case PhaseTeam:
		return "team"
	case PhaseIndividual:
		return "individual"
	case PhaseVerifyOnly:
		return "verify-only"
	default:
		return "unknown"
	}
}

// Topology names which login relationship the operator's active
// infisical session has with the team vault's organization. Only
// meaningful when Phase is PhaseIndividual: a same-login operator
// mints and stores in one continuous session; a split-login operator
// pauses for a single login switch between mint and store (R4).
type Topology int

const (
	TopologyUnknown Topology = iota
	TopologySameLogin
	TopologySplitLogin
)

func (t Topology) String() string {
	switch t {
	case TopologySameLogin:
		return "same-login"
	case TopologySplitLogin:
		return "split-login"
	default:
		return "unknown"
	}
}

// DetectionResult is Detect's inferred routing decision, before the
// operator has confirmed or overridden it (R2/R3) via ConfirmSetup /
// ConfirmTopology.
type DetectionResult struct {
	Phase    SetupPhase
	Topology Topology
	// ClientID is populated once ReadIdentity has succeeded. Callers
	// routing to PhaseIndividual or PhaseVerifyOnly reuse it rather
	// than re-fetching.
	ClientID string
}

// Detect implements the layered local-first detection funnel
// (Decision 3, steps 1-3): the free config signal, the reused
// identity-GET, and topology inferred from that call's success/failure
// shape. It must only be called after the api_url entry gate
// (CheckAPIURL) has already passed -- ReadIdentity is the first call
// that carries the operator's live session bearer.
//
// teamVaultEmpty is the free signal (VaultRegistry.IsEmpty()): true
// short-circuits to PhaseTeam with no network call at all.
// personalCredResolves is the cheap local signal (R15) of whether the
// personal overlay already declares [global.vault.provider] with a
// credential that resolves; it is only consulted once the
// identity-GET succeeds (team phase already complete).
func Detect(
	ctx context.Context,
	apiURL string,
	bearer secret.Value,
	identityID string,
	teamVaultEmpty bool,
	personalCredResolves bool,
) (DetectionResult, error) {
	if teamVaultEmpty {
		// Free: no project id exists yet to check anything against.
		return DetectionResult{Phase: PhaseTeam}, nil
	}

	clientID, err := infisical.ReadIdentity(ctx, apiURL, bearer, identityID)
	switch {
	case err == nil:
		if personalCredResolves {
			return DetectionResult{Phase: PhaseVerifyOnly, ClientID: clientID}, nil
		}
		// The call just succeeded with the operator's own current
		// session, so that session demonstrably reaches this
		// identity's org: same-login by construction, not a guess.
		return DetectionResult{Phase: PhaseIndividual, Topology: TopologySameLogin, ClientID: clientID}, nil

	case errors.Is(err, infisical.ErrIdentityNotFound):
		// Team setup incomplete -- Universal Auth isn't attached yet.
		// Topology is moot on this branch; it's resolved once team
		// setup completes and this call is retried.
		return DetectionResult{Phase: PhaseTeam}, nil

	case errors.Is(err, infisical.ErrUnauthorized):
		// The org-scope/unauthorized failure shape is the direct
		// split-login signal (Decision 3 step 3): the operator's
		// current session can't reach the org that owns this
		// identity, so a login switch is needed before mint (R4).
		return DetectionResult{Phase: PhaseIndividual, Topology: TopologySplitLogin}, nil

	default:
		// A generic transport/parse failure gives no distinguishable
		// shape -- Assumption C's documented fallback: assume
		// split-login as a prior and require confirmation, costing no
		// extra round trip, rather than aborting the wizard on an
		// ambiguous signal. ConfirmTopology always surfaces this as a
		// named, overridable prompt, never silently.
		return DetectionResult{Phase: PhaseIndividual, Topology: TopologySplitLogin}, nil
	}
}

// ConfirmSetup names the inferred setup phase and asks the operator to
// confirm or override it (R2: "MUST confirm or override," never
// silent). PhaseVerifyOnly is not a setup choice -- it's the automatic
// route when the individual credential already resolves -- so callers
// route it before ever reaching this function.
func ConfirmSetup(inferred SetupPhase, confirm ConfirmFunc) (SetupPhase, error) {
	if inferred != PhaseTeam && inferred != PhaseIndividual {
		return PhaseUnknown, fmt.Errorf("onboard: ConfirmSetup requires PhaseTeam or PhaseIndividual, got %s", inferred)
	}

	other := PhaseIndividual
	if inferred == PhaseIndividual {
		other = PhaseTeam
	}

	ok, err := confirm(fmt.Sprintf("Detected: %s setup. Continue?", inferred), true)
	if err != nil {
		return PhaseUnknown, err
	}
	if ok {
		return inferred, nil
	}
	return other, nil
}

// ConfirmTopology names the inferred topology and asks the operator to
// confirm or override it (R3), always as its own named prompt --
// never folded silently into another decision. A split-login
// inference is phrased to explain why: the current session doesn't
// yet reach the team vault's org.
func ConfirmTopology(inferred Topology, confirm ConfirmFunc) (Topology, error) {
	if inferred != TopologySameLogin && inferred != TopologySplitLogin {
		return TopologyUnknown, fmt.Errorf("onboard: ConfirmTopology requires TopologySameLogin or TopologySplitLogin, got %s", inferred)
	}

	label := inferred.String()
	other := TopologySplitLogin
	if inferred == TopologySplitLogin {
		label = "split-login -- your current session doesn't yet reach the team vault's org"
		other = TopologySameLogin
	}

	ok, err := confirm(fmt.Sprintf("Detected: %s. Continue?", label), true)
	if err != nil {
		return TopologyUnknown, err
	}
	if ok {
		return inferred, nil
	}
	return other, nil
}
