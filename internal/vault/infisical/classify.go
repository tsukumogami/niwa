package infisical

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/tsukumogami/niwa/internal/vault"
)

// responseCodePrefix starts the line the CLI prints when the server
// answered a request. Only a line that begins with it counts as a
// server response: the same digits elsewhere (an echoed message, a
// URL) are ignored.
const responseCodePrefix = "Response Code: "

// tokenEnvVar is the environment variable the CLI reads a token from.
// When it is set, the probe session that reports it as its source is
// the one the failed export ran as.
const tokenEnvVar = "INFISICAL_TOKEN"

// loggedOutWordings are the pinned CLI's messages for a missing or
// expired stored login. They decide only when the probe gave no usable
// answer and the export got no server response.
var loggedOutWordings = []string{
	"No valid login session found",
	"couldn't find your logged in details",
	"Your login session has expired",
}

// malformedTokenMarker in the probe's stderr means the credential
// itself is unusable, which no amount of retrying fixes.
const malformedTokenMarker = "token is malformed"

// exportFailure is what the classifier needs from a failed export. The
// output fields are already scrubbed; none of them is ever copied into
// a message by the classifier.
type exportFailure struct {
	stdout, stderr string
	// timedOut is true when the export reached its bound.
	timedOut bool
	// minted is true when the export ran with a token niwa minted from
	// a machine identity, rather than the CLI's own session.
	minted bool
}

// responseStatus returns the status of the export's server response,
// or 0 when the export got none.
func (f exportFailure) responseStatus() int {
	for _, out := range []string{f.stderr, f.stdout} {
		for _, line := range strings.Split(out, "\n") {
			if n := parseStatusLine(line); n > 0 {
				return n
			}
		}
	}
	return 0
}

// parseStatusLine returns the status a server status line carries, or
// 0 when line isn't one.
func parseStatusLine(line string) int {
	rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), responseCodePrefix)
	if !ok {
		return 0
	}
	if n, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil && n > 0 {
		return n
	}
	return 0
}

// probeResult is what the classifier reads from one probe run.
type probeResult struct {
	// parsed is true when stdout decoded as JSON.
	parsed   bool
	sessions []loginStatusSession
	// timedOut is true when the probe reached its bound.
	timedOut bool
	// malformedToken is true when the probe's stderr says the token is
	// malformed.
	malformedToken bool
}

// runProbe runs `infisical login status --json` under its own bound,
// derived from the caller's ctx rather than from the export's call
// context: by the time a timed-out export is classified, that context
// has already expired and would make the probe read as timed out
// before it starts.
//
// The probe's exit code is never read (it exits 0 when it can't reach
// the service), and its output never leaves this function except as
// the decoded fields, which hold no token.
func runProbe(ctx context.Context, c commander) probeResult {
	callCtx, cancel := withCallDeadline(ctx, callBound(probeTimeout))
	defer cancel()
	stdout, stderr, _, err := c.Run(callCtx, "infisical", []string{"login", "status", "--json"})
	var r probeResult
	if callTimedOut(callCtx, err) {
		r.timedOut = true
		return r
	}
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		// The probe didn't start, or its stdout passed the size cap
		// (errOutputTooLarge): no answer at all.
		return r
	}
	r.malformedToken = strings.Contains(strings.ToLower(vault.ScrubStderr(ctx, stderr)), malformedTokenMarker)
	// Only a JSON object with a sessions list is an answer. Anything
	// else (null, {}, a bare value) says nothing about the session, and
	// treating it as "no session" would call a real refusal a lapse.
	var out struct {
		Sessions *[]loginStatusSession `json:"sessions"`
	}
	if json.Unmarshal(stdout, &out) == nil && out.Sessions != nil {
		r.parsed = true
		r.sessions = *out.Sessions
	}
	return r
}

// decidingSession picks the session the failed export ran as: with
// INFISICAL_TOKEN set, the first session whose tokenSource names that
// variable (none decides if no session does); otherwise the first
// session listed, which is the one the CLI itself uses. Domains are
// never compared.
func decidingSession(sessions []loginStatusSession) *loginStatusSession {
	if os.Getenv(tokenEnvVar) != "" {
		for i := range sessions {
			if strings.Contains(strings.ToUpper(sessions[i].TokenSource), tokenEnvVar) {
				return &sessions[i]
			}
		}
		return nil
	}
	if len(sessions) == 0 {
		return nil
	}
	return &sessions[0]
}

// classifyExportFailure classifies a started export that failed or
// timed out. For a minted principal it needs nothing but the export.
// For a CLI-session principal it runs the probe, unless the export's
// own server response already decides.
//
// The numbered rules below are the six ordered rules of requirement R2
// in docs/prds/PRD-dispatch-offline-secrets.md, applied first match
// wins; the minted branch is that PRD's R3, and the wording fallback
// its R4.
func classifyExportFailure(ctx context.Context, c commander, f exportFailure) vault.FailureClass {
	status := f.responseStatus()
	answered := vault.FailureClass{Class: vault.ClassAnswered, HTTPStatus: status}
	unauthenticated := vault.FailureClass{Class: vault.ClassUnauthenticated, Reason: vault.ReasonLoggedOut, HTTPStatus: status}
	authStatus := status == 401 || status == 403

	if f.minted {
		// No probe: the minted token is niwa's own, and the server
		// either answered it or couldn't be reached.
		if status != 0 {
			return answered
		}
		return unreachableClass(f.timedOut, false, status)
	}

	// Rule 1: the server answered with something other than 401 or
	// 403. A missing folder or a server error is never a lapse, so the
	// probe can't change the answer and isn't run.
	if status != 0 && !authStatus {
		return answered
	}

	probe := runProbe(ctx, c)
	deciding := decidingSession(probe.sessions)
	conclusive := deciding != nil && deciding.Status == "authenticated" && deciding.Verification.State == "verified"

	switch {
	// Rule 2: the session the export ran as is expired or rejected,
	// or its token is malformed.
	case deciding != nil && (deciding.Status == "expired" || deciding.Status == "rejected"),
		probe.malformedToken:
		return unauthenticated
	// Rule 3: a 401 or 403 for a session the probe vouches for is a
	// real refusal.
	case authStatus && conclusive:
		return answered
	// Rule 4: a 401 or 403 the probe can't vouch for is a lapse. That
	// includes a probe that timed out, didn't start, or printed output
	// that doesn't parse (or passed the size cap), not only one that
	// lists an unverified session. So a real revocation that coincides
	// with an inconclusive probe is served stale, with the fallback
	// warning, until a probe can vouch for the session again.
	case authStatus:
		return unauthenticated
	// Rule 5: the probe answered, and lists no session the export
	// could have run as.
	case probe.parsed && deciding == nil:
		return unauthenticated
	// Wording fallback: the probe gave no usable answer, so the CLI's
	// own logged-out message decides. It must stay below rules 1, 3
	// and 4: they have already taken every export with a server
	// response, and the wording must never override one.
	case !probe.parsed && hasLoggedOutWording(f.stderr):
		return unauthenticated
	}
	// Rule 6: everything else.
	return unreachableClass(f.timedOut, probe.timedOut, status)
}

// unreachableClass is the unreachable class, with the reason saying
// whether the export or the probe reached its bound.
func unreachableClass(exportTimedOut, probeTimedOut bool, status int) vault.FailureClass {
	reason := vault.ReasonUnreachable
	if exportTimedOut || probeTimedOut {
		reason = vault.ReasonTimedOut
	}
	return vault.FailureClass{Class: vault.ClassUnreachable, Reason: reason, HTTPStatus: status}
}

// hasLoggedOutWording reports whether stderr holds one of the pinned
// CLI's logged-out messages.
func hasLoggedOutWording(stderr string) bool {
	for _, w := range loggedOutWordings {
		if strings.Contains(stderr, w) {
			return true
		}
	}
	return false
}
