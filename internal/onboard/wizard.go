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
	// PhaseUnknown when neither flag was passed. Today this is Run's
	// only routing input -- a non-unknown value both selects the
	// runner AND is returned verbatim rather than confirmed against
	// Detect's inference. Whoever wires Detect in must decide whether a
	// supplied override skips Detect entirely (no ClientID fetched) or
	// only skips ConfirmSetup while Detect still runs to populate
	// DetectionResult.ClientID -- the design's R2 text ("MUST confirm
	// or override") reads as the latter, since the individual runner
	// needs ClientID regardless of how the phase was chosen.
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
// Detect/ConfirmSetup/ConfirmTopology are not yet wired in here -- a
// caller must resolve SetupOverride and TopologyOverride itself (the
// command layer's --team/--individual/--same-login/--split-login flags
// today); this returns a plain (untyped, exit-1 fallback) error when
// SetupOverride is PhaseUnknown or any other phase this function
// doesn't yet route.
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

	if opts.SetupOverride == PhaseUnknown {
		return result, fmt.Errorf("onboard: setup detection is not yet implemented; pass --team or --individual")
	}

	if opts.SetupOverride == PhaseTeam {
		if opts.Team == nil {
			return result, fmt.Errorf("onboard: Options.Team must be populated when SetupOverride is PhaseTeam")
		}
		if _, err := RunTeam(ctx, *opts.Team); err != nil {
			return result, err
		}
		return result, nil
	}

	if opts.SetupOverride == PhaseIndividual {
		if opts.Individual == nil {
			return result, fmt.Errorf("onboard: Options.Individual must be populated when SetupOverride is PhaseIndividual")
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

	if opts.SetupOverride == PhaseVerifyOnly {
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

	return result, fmt.Errorf("onboard: %s setup is not yet implemented", opts.SetupOverride)
}
