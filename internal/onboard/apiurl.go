package onboard

import (
	"errors"
	"fmt"

	"github.com/tsukumogami/niwa/internal/vault/infisical"
)

// ErrAPIURLNotAccepted is returned by CheckAPIURL when a non-default
// https api_url is presented and neither an interactive confirm nor
// the --accept-api-url override accepts it. This is a typed
// condition, not a plain error string, so the command layer (a later
// issue) can map it to exit 2 via errors.Is.
var ErrAPIURLNotAccepted = errors.New("onboard: api_url requires explicit acknowledgment (confirm or --accept-api-url)")

// ConfirmFunc is the prompt-kit hook CheckAPIURL (and the detection
// funnel's ConfirmSetup/ConfirmTopology) use to ask for explicit
// acknowledgment. Bound to onboard.Confirm over real stdin/stdout in
// production; tests and the non-interactive path substitute their
// own.
type ConfirmFunc func(prompt string, defaultYes bool) (bool, error)

// CheckAPIURL runs the entry-time api_url trust gate (Decision 3 step
// 0 / Decision 4's supply-chain guard). It MUST run right after
// infisical's resolveAPIURL and strictly before any bearer-carrying
// call, including Detect's ReadIdentity probe: the detection call is
// itself the first call that carries the operator's live session
// bearer, so a guard folded into a later confirm would fire only
// after that bearer was already in flight -- it would never protect
// the call it guards.
//
// A non-https apiURL, or one that fails to parse, is an unconditional
// hard reject in every mode, before any request is built -- there is
// no override for this rule; "warn and proceed" would be silent
// acceptance in a scripted run.
//
// A non-default https apiURL requires explicit acknowledgment:
//   - accept (the --accept-api-url override) short-circuits to
//     acceptance, in any mode;
//   - otherwise, when interactive is true, confirm is invoked to ask
//     the operator (confirm must be non-nil in this case);
//   - otherwise (non-interactive, no override) the gate fails closed
//     with ErrAPIURLNotAccepted -- never silently accepted.
func CheckAPIURL(apiURL string, accept bool, interactive bool, confirm ConfirmFunc) error {
	nonDefault, err := infisical.ValidateAPIURL(apiURL)
	if err != nil {
		// Rule 1: unconditional hard reject (non-https or malformed).
		// No mode-dependent branch here -- this fires before accept or
		// interactive is even consulted.
		return err
	}
	if !nonDefault {
		return nil
	}

	// Rule 2: a well-formed but non-default https apiURL needs
	// explicit acknowledgment.
	if accept {
		return nil
	}
	if !interactive {
		return ErrAPIURLNotAccepted
	}
	if confirm == nil {
		return fmt.Errorf("onboard: CheckAPIURL: interactive gate requires a non-nil confirm function")
	}

	prompt := fmt.Sprintf("api_url is %s -- not the Infisical cloud default. Continue?", SanitizeURL(apiURL))
	ok, err := confirm(prompt, false)
	if err != nil {
		return err
	}
	if !ok {
		return ErrAPIURLNotAccepted
	}
	return nil
}
