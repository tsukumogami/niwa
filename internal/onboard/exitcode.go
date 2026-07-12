package onboard

// ExitCodeError carries a niwa-onboard-specific exit code and message,
// the same two-field shape as sessionattach.ExitCodeError. Constructed
// at each terminal outcome the wizard reaches and unwrapped by
// cli.Execute() via errors.As, so `niwa onboard` gets its own
// self-consistent exit-code vocabulary without touching any other
// command's codes (Decision 2).
type ExitCodeError struct {
	Code int
	Msg  string
}

func (e *ExitCodeError) Error() string { return e.Msg }

// Exit codes per the design's table (Decision 2), ordered along the
// wizard's own pipeline: precondition -> confirm/decline ->
// mint+authenticate -> store -> verify. Code 0 (success) and code 1
// (the repo-wide untyped fallback for anything not listed here) are
// not named as constants -- 1 in particular must stay the ordinary
// unclassified-error path, never something this package constructs on
// purpose.
const (
	// ExitNonInteractivePrecondition is returned when stdin is not a
	// terminal and the supplied overrides don't cover what the wizard
	// needs to proceed (R18/AC-30), including the non-TTY api_url
	// contract (a non-default api_url without --accept-api-url, or any
	// non-https api_url in any mode).
	ExitNonInteractivePrecondition = 2
	// ExitDecline is returned when the operator declines or aborts
	// mid-wizard (R2/AC-4, AC-32).
	ExitDecline = 3
	// ExitAuthFailure is returned on an authentication failure (R9/AC-14).
	ExitAuthFailure = 4
	// ExitStorageWrite is returned when the credential store write
	// fails (R8 step 4/AC-34).
	ExitStorageWrite = 5
	// ExitVerification is returned when wizard-end verification fails
	// (R11/AC-18b).
	ExitVerification = 6
)

// StatusForCode returns the --json envelope's "status" string tied 1:1
// to code, per Decision 2's envelope shape. Any code outside the named
// set (0 and the five above) maps to "error", covering the untyped
// exit-1 fallback and anything unanticipated.
func StatusForCode(code int) string {
	switch code {
	case 0:
		return "success"
	case ExitNonInteractivePrecondition:
		return "non_interactive_precondition_failed"
	case ExitDecline:
		return "declined"
	case ExitAuthFailure:
		return "authentication_failed"
	case ExitStorageWrite:
		return "storage_write_failed"
	case ExitVerification:
		return "verification_failed"
	default:
		return "error"
	}
}
