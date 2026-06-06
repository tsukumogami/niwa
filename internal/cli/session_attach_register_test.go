package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
)

// TestRunSessionAttach_NoArgsReturnsUsageError verifies PRD R10 behavior for
// the attach command: invoking `niwa session attach` with no session_id must
// return *sessionattach.ExitCodeError with Code=2 and the verbatim usage
// string (so root.Execute() translates it to os.Exit(2)). Cobra's default
// ExactArgs error exits 1 with a generic message; this guard ensures we
// don't regress to that behavior.
func TestRunSessionAttach_NoArgsReturnsUsageError(t *testing.T) {
	err := runSessionAttach(sessionAttachCmd, nil)
	if err == nil {
		t.Fatalf("want ExitCodeError, got nil")
	}
	var ece *sessionattach.ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T", err)
	}
	if ece.Code != 2 {
		t.Errorf("Code = %d, want 2 (usage error per PRD)", ece.Code)
	}
	wantSubstrs := []string{
		"niwa: usage",
		"niwa session attach",
		"<session_id>",
	}
	for _, s := range wantSubstrs {
		if !strings.Contains(ece.Msg, s) {
			t.Errorf("missing %q in usage message: %q", s, ece.Msg)
		}
	}
}

// TestRunSessionDetach_NoArgsReturnsUsageError mirrors the attach test but
// asserts the PRD R10 verbatim wording for detach, which differs from
// attach: the message names the auto-release semantics ("Normal attach
// release happens automatically when claude code exits") because detach
// only exists for stale-lock recovery.
func TestRunSessionDetach_NoArgsReturnsUsageError(t *testing.T) {
	err := runSessionDetach(sessionDetachCmd, nil)
	if err == nil {
		t.Fatalf("want ExitCodeError, got nil")
	}
	var ece *sessionattach.ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T", err)
	}
	if ece.Code != 2 {
		t.Errorf("Code = %d, want 2 (usage error per PRD R10)", ece.Code)
	}
	wantSubstrs := []string{
		"niwa: usage",
		"niwa session detach",
		"<session_id>",
		"[--force]",
		"break stale locks",
	}
	for _, s := range wantSubstrs {
		if !strings.Contains(ece.Msg, s) {
			t.Errorf("missing %q in usage message: %q", s, ece.Msg)
		}
	}
}
