package onboard

import (
	"context"
	"errors"
	"fmt"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault/infisical"
)

// Options collects the resolved inputs Run's shared entry sequence
// needs: the operator's setup/topology overrides, the api_url
// acknowledgment flag, whether the run is interactive, and the
// per-phase inputs (Team, Individual) the team and individual runners
// need once SetupOverride resolves to one of them.
type Options struct {
	// SetupOverride is the operator's --team/--individual choice, or
	// PhaseUnknown when neither flag was passed. A non-unknown value
	// both selects the runner AND skips Detect entirely (Issue 9): no
	// ClientID is fetched via Detect's own ReadIdentity call, because
	// RunIndividualSetup performs its own ReadIdentity as the first
	// step of its pipeline regardless of how the phase was chosen --
	// there is no caller that needs Detect's DetectionResult.ClientID
	// once an override is supplied. PhaseUnknown routes through Run's
	// own Detect invocation (see Options.Detect) instead.
	SetupOverride SetupPhase
	// TopologyOverride is the operator's --same-login/--split-login
	// choice, or TopologyUnknown when neither flag was passed. Only
	// meaningful once the setup is (or would resolve to) PhaseIndividual.
	TopologyOverride Topology

	// APIURLConfigVal is the workspace's declared [vault.provider]
	// api_url, when known. Empty until the team/individual runners load
	// it; the entry gate still resolves correctly against the env
	// override or the cloud default in that case.
	APIURLConfigVal string
	// AcceptAPIURL is --accept-api-url: pre-acknowledges a non-default
	// api_url, satisfying the entry gate without an interactive prompt.
	AcceptAPIURL bool

	// Interactive is true when stdin is a terminal. Gates both the
	// non-TTY precondition and whether the api_url gate may prompt.
	Interactive bool
	// Confirm is invoked by the api_url gate's interactive prompt. Only
	// consulted when Interactive is true and AcceptAPIURL is false; must
	// be non-nil in that case.
	Confirm ConfirmFunc

	// Team carries the operator-session and config-sourced inputs the
	// team runner (Issue 5) needs -- the project id, the identity to
	// check/guide, and the operator's own session bearer. Only
	// consulted once SetupOverride resolves to PhaseTeam; nil is a
	// caller bug at that point (every production caller,
	// internal/cli/onboard.go, populates it once detection/config
	// wiring lands).
	Team *TeamOptions

	// Individual carries the operator-session and config-sourced
	// inputs the individual runner (Issue 6) needs -- the identity to
	// mint against, the workspace project/environment, the
	// credential-sync destination spec, and the confirmed topology.
	// Only consulted once SetupOverride resolves to PhaseIndividual;
	// nil is a caller bug at that point, mirroring Team above.
	Individual *IndividualSetupParams

	// Verify carries the R11 wizard-end check's inputs (Issue 8).
	// Consulted after a successful RunIndividualSetup, and directly
	// when SetupOverride is PhaseVerifyOnly (R15's re-run-goes-
	// straight-to-verification shortcut). nil is a caller bug once
	// SetupOverride resolves to either of those, mirroring
	// Team/Individual above.
	Verify *VerifyIndividualParams

	// Preconditions carries the R22 session and personal-overlay
	// precondition inputs (Issue 8). nil skips R22 entirely -- every
	// caller not yet wired for it (and every existing test) keeps
	// today's behavior unchanged; a caller ready to exercise R22
	// populates this field and Run enforces both checks at wizard
	// entry, before the api_url gate and before any routing decision.
	Preconditions *PreconditionsParams

	// Detect carries the raw inputs Run needs to invoke the detection
	// funnel (detect.go) itself when SetupOverride is PhaseUnknown
	// (R2: infer, then confirm or override -- Issue 9's wiring of the
	// previously-unwired Detect call). nil combined with PhaseUnknown
	// falls through to the pre-existing "not yet implemented" error,
	// so every caller that resolves SetupOverride itself (every
	// existing test, and any --team/--individual invocation) is
	// unaffected.
	Detect *DetectInputs
}

// DetectInputs collects Detect's raw inputs (detect.go's own
// parameters) for the caller that wants Run to perform detection
// itself rather than resolving SetupOverride up front. Only consulted
// when SetupOverride is PhaseUnknown.
type DetectInputs struct {
	// APIURL is the same resolved, already-gated api_url Team/
	// Individual carry -- Detect's ReadIdentity call is the first
	// bearer-carrying call once the api_url gate above has passed.
	APIURL string
	// Bearer is the operator's own live session bearer.
	Bearer secret.Value
	// IdentityID is the config-sourced identity id Detect checks.
	IdentityID string
	// TeamVaultEmpty is Detect's free local signal
	// (config.VaultRegistry.IsEmpty() on the team config).
	TeamVaultEmpty bool
	// PersonalCredResolves is Detect's R15 free local signal: whether
	// the personal overlay already declares a credential-sync entry
	// that resolves for the target (kind, project) pair.
	PersonalCredResolves bool
}

// PreconditionsParams collects the R22 precondition inputs Run
// consults when Options.Preconditions is non-nil.
type PreconditionsParams struct {
	// SessionChecker overrides the production
	// infisical.DetectSessionStatus call; nil uses the real CLI.
	// Tests inject a fake.
	SessionChecker SessionChecker
	// Overlay carries EnsurePersonalOverlay's own inputs, including
	// its own Pause for the scaffold-and-guide step.
	Overlay EnsurePersonalOverlayParams
	// Pause backs the session-login pause (EnsureAuthenticatedSession).
	Pause func(prompt string) error
}

// Result is the wizard's terminal outcome. Setup is populated whenever
// the phase is known -- from the operator's override today, from
// detection once Detect is wired into Run -- even on an error return,
// so callers (the --json envelope) can name the setup a failed run was
// attempting.
type Result struct {
	Setup SetupPhase
}

// ErrOverrideRequired is returned by Run (wrapped in an *ExitCodeError,
// code ExitNonInteractivePrecondition) when stdin is not a terminal and
// the supplied overrides don't cover what the wizard would otherwise
// need to ask about (R18/AC-30).
var ErrOverrideRequired = errors.New("onboard: stdin is not a terminal; pass --team/--individual (and --same-login/--split-login for an individual setup) to proceed")

// checkNonInteractivePrecondition implements the non-TTY fail-fast
// contract: a missing setup override always fails; a missing topology
// override only fails once the setup is (or would resolve to)
// individual, since topology has no meaning on the team path.
func checkNonInteractivePrecondition(interactive bool, setup SetupPhase, topology Topology) error {
	if interactive {
		return nil
	}
	if setup == PhaseUnknown {
		return ErrOverrideRequired
	}
	if setup == PhaseIndividual && topology == TopologyUnknown {
		return ErrOverrideRequired
	}
	return nil
}

// Run executes the wizard's shared entry sequence: the non-TTY/override
// precondition (R18/AC-30), then the api_url trust gate (Decision 3
// step 0 / Decision 4), which must run before any bearer-carrying call.
// Once both pass, Run attaches a redactor and routes to the setup
// runner named by opts.SetupOverride (RunTeam or RunIndividualSetup).
// When SetupOverride is PhaseUnknown (no --team/--individual flag),
// Run performs detection itself (Issue 9): it invokes Detect against
// opts.Detect's inputs, then confirms an inferred individual setup via
// opts.Confirm before any state changes, declining into ExitDecline
// rather than silently switching to the other phase (AC-4/AC-32).
// opts.Detect nil (every caller that resolves SetupOverride itself --
// every existing test, and any --team/--individual invocation) falls
// through to the pre-existing "not yet implemented" error, preserving
// prior behavior for those callers exactly.
func Run(opts Options) (Result, error) {
	// Result.Setup carries opts.SetupOverride through every return path
	// below, including the two gate-failure paths -- not just the stub
	// success/not-implemented path -- so a caller's --json envelope can
	// still name the setup a failed run was attempting.
	result := Result{Setup: opts.SetupOverride}

	if err := checkNonInteractivePrecondition(opts.Interactive, opts.SetupOverride, opts.TopologyOverride); err != nil {
		return result, &ExitCodeError{Code: ExitNonInteractivePrecondition, Msg: err.Error()}
	}

	// A caller that sets Interactive without also wiring Confirm is a
	// configuration bug, not a policy failure -- CheckAPIURL's own nil-
	// Confirm error would otherwise fold into ExitNonInteractivePrecondition
	// below, misreporting a programmer error as the non-interactive
	// precondition it isn't. Every production caller (internal/cli/onboard.go)
	// always wires Confirm when Interactive is true, so this should never
	// fire outside a misconfigured direct caller of Run.
	if opts.Interactive && !opts.AcceptAPIURL && opts.Confirm == nil {
		return result, fmt.Errorf("onboard: Options.Confirm must be non-nil when Interactive is true and AcceptAPIURL is false")
	}

	// A redactor is attached once here, before any of R22's
	// preconditions, the api_url gate, or either runner makes its first
	// bearer-carrying call, and shared by all of them -- R17 requires
	// every secret registered on it before use, and this is the one
	// place every call path funnels through. Neither RunTeam nor
	// RunIndividualSetup attaches its own; without this, every secret
	// they register would have nowhere to land, and scrub-before-error
	// would silently no-op.
	ctx := secret.WithRedactor(context.Background(), secret.NewRedactor())

	// R22 preconditions run at wizard entry, before detection/routing
	// and before the api_url gate below -- Options.Preconditions is nil
	// for every caller not yet wired for R22, which keeps today's
	// behavior unchanged for them (Issue 8).
	if opts.Preconditions != nil {
		if err := EnsureAuthenticatedSession(ctx, opts.Preconditions.SessionChecker, opts.Preconditions.Pause); err != nil {
			return result, fmt.Errorf("onboard: R22 session precondition: %w", err)
		}
		if _, err := EnsurePersonalOverlay(ctx, opts.Preconditions.Overlay); err != nil {
			return result, fmt.Errorf("onboard: R22 personal-overlay precondition: %w", err)
		}
	}

	apiURL := infisical.ResolveAPIURL(opts.APIURLConfigVal)
	if err := CheckAPIURL(apiURL, opts.AcceptAPIURL, opts.Interactive, opts.Confirm); err != nil {
		return result, &ExitCodeError{Code: ExitNonInteractivePrecondition, Msg: err.Error()}
	}

	setupPhase := opts.SetupOverride
	topology := opts.TopologyOverride

	// R2: when the operator didn't supply --team/--individual, Run
	// performs detection itself, then presents the inferred phase for
	// confirmation before any state changes (AC-2/AC-4). A decline
	// here is a genuine abort (AC-32, exit 3) -- it does NOT silently
	// switch to the other phase; the explicit --team/--individual
	// override already exists for that (AC-3), and the wizard must
	// not guess a different action than the one it just asked the
	// operator to confirm. This is why the shared ConfirmSetup helper
	// (detect.go), whose own tested contract is "decline switches to
	// the other phase," is deliberately not reused here -- its
	// contract is real and pinned by its own tests, just not the one
	// this confirmation needs.
	if setupPhase == PhaseUnknown {
		if opts.Detect == nil {
			return result, fmt.Errorf("onboard: setup detection is not yet implemented; pass --team or --individual")
		}
		det, err := Detect(ctx, opts.Detect.APIURL, opts.Detect.Bearer, opts.Detect.IdentityID, opts.Detect.TeamVaultEmpty, opts.Detect.PersonalCredResolves)
		if err != nil {
			return result, err
		}
		// Result.Setup names the detected phase from this point on,
		// including a decline return below -- matching Result's own
		// doc comment ("populated whenever the phase is known ...
		// even on an error return"), so a caller's --json envelope can
		// still say which setup the operator declined.
		result.Setup = det.Phase

		switch det.Phase {
		case PhaseTeam, PhaseVerifyOnly:
			// The team path has no confirmable inference short of
			// R21's own re-verification, and PhaseVerifyOnly is an
			// automatic route (R15), not an operator choice -- neither
			// presents a decline-able prompt.
			setupPhase = det.Phase
		case PhaseIndividual:
			if opts.Confirm == nil {
				return result, fmt.Errorf("onboard: Options.Confirm must be non-nil to confirm a detected individual setup")
			}
			ok, err := opts.Confirm(fmt.Sprintf("Detected: individual setup (%s). Continue?", det.Topology), true)
			if err != nil {
				return result, err
			}
			if !ok {
				return result, &ExitCodeError{Code: ExitDecline, Msg: "onboard: operator declined the detected individual setup"}
			}
			resolvedTopology, err := ConfirmTopology(det.Topology, opts.Confirm)
			if err != nil {
				return result, err
			}
			setupPhase = PhaseIndividual
			topology = resolvedTopology
		default:
			return result, fmt.Errorf("onboard: Detect returned unrecognized phase %s", det.Phase)
		}
	}

	if setupPhase == PhaseTeam {
		if opts.Team == nil {
			return result, fmt.Errorf("onboard: Options.Team must be populated when SetupOverride is PhaseTeam")
		}
		if _, err := RunTeam(ctx, *opts.Team); err != nil {
			return result, err
		}
		return result, nil
	}

	if setupPhase == PhaseIndividual {
		if opts.Individual == nil {
			return result, fmt.Errorf("onboard: Options.Individual must be populated when SetupOverride is PhaseIndividual")
		}
		if topology != TopologyUnknown {
			// Detection above resolved a topology the caller's
			// pre-built Individual params couldn't have known in
			// advance; every other path (an explicit --same-login/
			// --split-login override) already set this consistently.
			opts.Individual.Topology = topology
		}
		if _, err := RunIndividualSetup(ctx, *opts.Individual); err != nil {
			return result, err
		}
		// R11 wizard-end check: the individual pipeline's mint/store
		// succeeded, but success isn't declared until the stored
		// credential proves it resolves through the same read topology
		// `niwa apply` uses (Issue 8). The Verify nil-check sits here,
		// after RunIndividualSetup runs, rather than up front -- a
		// caller whose individual pipeline fails before ever reaching
		// this point (e.g. an induced mint/verify failure) must still
		// get that typed failure back, not a caller-bug error about a
		// step it never reached.
		if opts.Verify == nil {
			return result, fmt.Errorf("onboard: Options.Verify must be populated when SetupOverride is PhaseIndividual")
		}
		if err := VerifyIndividual(ctx, *opts.Verify); err != nil {
			return result, err
		}
		return result, nil
	}

	if setupPhase == PhaseVerifyOnly {
		// R15: re-running against a completed individual setup goes
		// straight to the wizard-end check -- no re-mint, no re-store.
		if opts.Verify == nil {
			return result, fmt.Errorf("onboard: Options.Verify must be populated when SetupOverride is PhaseVerifyOnly")
		}
		if err := VerifyIndividual(ctx, *opts.Verify); err != nil {
			return result, err
		}
		return result, nil
	}

	return result, fmt.Errorf("onboard: %s setup is not yet implemented", setupPhase)
}
