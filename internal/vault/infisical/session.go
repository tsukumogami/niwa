package infisical

import (
	"context"
	"encoding/json"

	"github.com/tsukumogami/niwa/internal/vault"
)

// loginStatusSession models one entry of the "sessions" array emitted
// by `infisical login status --json`, per the shape confirmed in
// NOTE-onboard-rest-verification.md (Assumption C). Only the fields
// the wizard's detection funnel needs are modeled; the CLI may emit
// additional fields (authMethod, tokenSource, verification.state,
// etc.) which are ignored here.
type loginStatusSession struct {
	Status       string `json:"status"`
	Organization string `json:"organization"`
}

// loginStatusOutput is the top-level shape of `infisical login
// status --json`.
type loginStatusOutput struct {
	Sessions []loginStatusSession `json:"sessions"`
}

// SessionStatus is the outcome of detecting the operator's current
// `infisical` CLI session. It is a proactive UX aid for the topology
// prompt, NOT a safety-critical gate -- the authoritative "wrong org"
// signal is always the classified response of an actual privileged
// call (ErrUnauthorized from ReadIdentity or another management
// call), per Decision 4.
type SessionStatus struct {
	// Authenticated reports whether `infisical login status` found at
	// least one session reporting status "authenticated".
	Authenticated bool
	// Organization is the org identifier of the authenticated
	// session, when present. Empty when unauthenticated, or when the
	// session is authenticated but the CLI did not report an
	// "organization" field (e.g., a machine-identity token session) --
	// callers must treat an empty Organization on an authenticated
	// session as "unparseable for org context" and fall back to
	// classifying the management call's own error, per Assumption C's
	// documented fallback.
	Organization string
}

// DetectSessionStatus shells to `infisical login status --json` via
// the supplied commander and parses its output into a SessionStatus.
//
// A non-zero exit, a start failure, or malformed JSON is treated as
// "no usable session" (Authenticated: false) rather than a hard
// error -- this call is advisory, and any caller depending on session
// state for a real decision (R22's missing-session precondition, or
// the wizard's own privileged calls) has its own authoritative check
// downstream. The one exception is a nil commander combined with a
// process start failure that suggests the `infisical` binary itself
// is missing; that case is also folded into Authenticated: false
// rather than surfaced as an error, since the wizard's next step
// (walking the operator through `infisical login`) applies equally
// whether the CLI is merely logged out or entirely absent from PATH.
func DetectSessionStatus(ctx context.Context, c commander) (SessionStatus, error) {
	if c == nil {
		c = defaultCommander{}
	}

	stdout, stderrBytes, exitCode, err := c.Run(ctx, "infisical", []string{"login", "status", "--json"})
	if err != nil {
		return SessionStatus{}, nil
	}
	if exitCode != 0 {
		// Scrub defensively even though a login-status stderr is not
		// expected to carry secret material -- consistent with every
		// other subprocess call site in this package.
		_ = vault.ScrubStderr(ctx, stderrBytes)
		return SessionStatus{}, nil
	}

	var parsed loginStatusOutput
	if err := json.Unmarshal(stdout, &parsed); err != nil {
		// Unparseable output falls back to "no usable session" per
		// Assumption C -- the wizard's classify-the-error fallback
		// takes over from here.
		return SessionStatus{}, nil
	}

	for _, s := range parsed.Sessions {
		if s.Status == "authenticated" {
			return SessionStatus{Authenticated: true, Organization: s.Organization}, nil
		}
	}
	return SessionStatus{}, nil
}
