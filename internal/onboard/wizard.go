package onboard

import (
	"errors"
	"fmt"
	"os"
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
	// PhaseUnknown when neither flag was passed.
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

// cloudDefaultAPIURL and apiURLEnvVar duplicate the env-and-default
// half of infisical's private resolveAPIURL precedence (config -> env
// -> default; internal/vault/infisical/auth.go). The config half of
// that precedence isn't reachable from here until the team/individual
// runners load the workspace's [vault.provider] declaration and pass it
// as APIURLConfigVal; until then this still resolves correctly against
// the env override or the cloud default, and no caller changes are
// needed once APIURLConfigVal starts arriving populated.
const (
	cloudDefaultAPIURL = "https://app.infisical.com/api"
	apiURLEnvVar       = "NIWA_INFISICAL_API_URL"
)

// resolveAPIURLForGate mirrors infisical's config -> env -> default
// api_url precedence for the entry gate, which must run before any
// per-workspace vault config is necessarily available.
func resolveAPIURLForGate(configVal string) string {
	if configVal != "" {
		return configVal
	}
	if v := os.Getenv(apiURLEnvVar); v != "" {
		return v
	}
	return cloudDefaultAPIURL
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

	apiURL := resolveAPIURLForGate(opts.APIURLConfigVal)
	if err := CheckAPIURL(apiURL, opts.AcceptAPIURL, opts.Interactive, opts.Confirm); err != nil {
		return result, &ExitCodeError{Code: ExitNonInteractivePrecondition, Msg: err.Error()}
	}

	if opts.SetupOverride == PhaseUnknown {
		return result, fmt.Errorf("onboard: setup detection is not yet implemented; pass --team or --individual")
	}
	return result, fmt.Errorf("onboard: %s setup is not yet implemented", opts.SetupOverride)
}
