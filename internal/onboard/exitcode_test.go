package onboard

import "testing"

func TestExitCodeError_ErrorReturnsMsg(t *testing.T) {
	e := &ExitCodeError{Code: ExitAuthFailure, Msg: "authentication failed: bad credentials"}
	if e.Error() != "authentication failed: bad credentials" {
		t.Errorf("Error() = %q, want the Msg field verbatim", e.Error())
	}
}

// TestStatusForCode_CoversTheFullTable asserts each of the five R16/R18
// terminal outcomes plus success gets its own distinct status string
// (AC-26), and any unrecognized code -- including the untyped exit-1
// fallback -- maps to "error" rather than colliding with a named status.
func TestStatusForCode_CoversTheFullTable(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{0, "success"},
		{ExitNonInteractivePrecondition, "non_interactive_precondition_failed"},
		{ExitDecline, "declined"},
		{ExitAuthFailure, "authentication_failed"},
		{ExitStorageWrite, "storage_write_failed"},
		{ExitVerification, "verification_failed"},
		{1, "error"},
		{99, "error"},
	}

	seen := map[string]bool{}
	for _, c := range cases {
		got := StatusForCode(c.code)
		if got != c.want {
			t.Errorf("StatusForCode(%d) = %q, want %q", c.code, got, c.want)
		}
		if c.want != "error" {
			if seen[c.want] {
				t.Errorf("status %q is not distinct across codes", c.want)
			}
			seen[c.want] = true
		}
	}
}
