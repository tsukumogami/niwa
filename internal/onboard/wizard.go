package onboard

import (
	"context"
	"errors"
	"fmt"

	"github.com/tsukumogami/niwa/internal/vault/infisical"
)

// Options collects the resolved inputs Run's shared entry sequence
// needs: the operator's setup/topology overrides, the api_url
// acknowledgment flag, and whether the run is interactive. Real
// config/session wiring -- the workspace's declared vault provider, the
// identity id, and an authenticated bearer -- lands with the team and
// individual runners (Issues 5/6); until then Run exercises the entry
// sequence (the non-TTY precondition, then the api_url trust gate) and
// routes to a not-yet-implemented stub in place of those runners.
type Options struct {
	// SetupOverride is the operator's --team/--individual choice, or
	// PhaseUnknown when neither flag was passed. Today this is Run's
	// only routing input -- a non-unknown value both selects the
	// eventual runner AND, at this skeleton stage, is returned verbatim
	// rather than confirmed against Detect's inference. Once the
	// team/individual runners land (Issues 5/6), whoever wires Detect in
	// must decide whether a supplied override skips Detect entirely (no
	// ClientID fetched) or only skips ConfirmSetup while Detect still
	// runs to populate DetectionResult.ClientID -- the design's R2 text
	// ("MUST confirm or override") reads as the latter, since the
	// individual runner needs ClientID regardless of how the phase was
	// chosen.
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
}

// Result is the wizard's terminal outcome. Setup is populated whenever
// the phase is known -- from the operator's override today, from
// detection once the team/individual runners land -- even on an error
// return, so callers (the --json envelope) can name the setup a failed
// run was attempting.
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
// Once both pass, Run routes to the setup runner named by
// opts.SetupOverride. The team and individual runners themselves land
// in Issues 5/6; until then this returns a plain (untyped, exit-1
// fallback) error naming the phase that would have run, so the command
// surface, exit codes, and non-TTY/api_url contracts are fully wired
// and testable ahead of those runners.
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
		if _, err := RunTeam(context.Background(), *opts.Team); err != nil {
			return result, err
		}
		return result, nil
	}

	return result, fmt.Errorf("onboard: %s setup is not yet implemented", opts.SetupOverride)
}
