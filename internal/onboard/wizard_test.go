package onboard

import (
	"errors"
	"os"
	"testing"
)

func TestCheckNonInteractivePrecondition_InteractiveAlwaysPasses(t *testing.T) {
	if err := checkNonInteractivePrecondition(true, PhaseUnknown, TopologyUnknown); err != nil {
		t.Errorf("interactive run with no overrides: got %v, want nil", err)
	}
}

func TestCheckNonInteractivePrecondition_NonInteractiveNoSetupOverrideFails(t *testing.T) {
	err := checkNonInteractivePrecondition(false, PhaseUnknown, TopologyUnknown)
	if !errors.Is(err, ErrOverrideRequired) {
		t.Fatalf("err = %v, want ErrOverrideRequired", err)
	}
}

func TestCheckNonInteractivePrecondition_NonInteractiveTeamOverrideNoTopologyNeededPasses(t *testing.T) {
	// Topology has no meaning on the team path, so a missing topology
	// override must not block it.
	if err := checkNonInteractivePrecondition(false, PhaseTeam, TopologyUnknown); err != nil {
		t.Errorf("team override with no topology: got %v, want nil", err)
	}
}

func TestCheckNonInteractivePrecondition_NonInteractiveIndividualNoTopologyFails(t *testing.T) {
	err := checkNonInteractivePrecondition(false, PhaseIndividual, TopologyUnknown)
	if !errors.Is(err, ErrOverrideRequired) {
		t.Fatalf("err = %v, want ErrOverrideRequired", err)
	}
}

func TestCheckNonInteractivePrecondition_NonInteractiveIndividualWithTopologyPasses(t *testing.T) {
	if err := checkNonInteractivePrecondition(false, PhaseIndividual, TopologySameLogin); err != nil {
		t.Errorf("individual override with topology: got %v, want nil", err)
	}
}

// apiURLEnvVarForTest mirrors infisical's unexported env-override
// variable name, used only to clear/set the environment around tests
// that must not depend on whatever value happens to be inherited from
// the test runner's environment. See infisical.ResolveAPIURL, which
// Run calls directly -- there's no wizard-local resolution left to
// unit-test here beyond what infisical's own auth_test.go already
// covers.
const apiURLEnvVarForTest = "NIWA_INFISICAL_API_URL"

func TestRun_NonInteractiveNoOverrideFailsFastBeforeAPIURLGate(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)
	_, err := Run(Options{Interactive: false})
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != ExitNonInteractivePrecondition {
		t.Errorf("Code = %d, want ExitNonInteractivePrecondition (%d)", ece.Code, ExitNonInteractivePrecondition)
	}
}

func TestRun_NonInteractiveIndividualWithoutTopologyFails(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)
	_, err := Run(Options{Interactive: false, SetupOverride: PhaseIndividual})
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != ExitNonInteractivePrecondition {
		t.Errorf("Code = %d, want ExitNonInteractivePrecondition (%d)", ece.Code, ExitNonInteractivePrecondition)
	}
}

func TestRun_NonInteractiveTeamOverridePassesPreconditionThenHitsStub(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)
	result, err := Run(Options{Interactive: false, SetupOverride: PhaseTeam})
	// Precondition and api_url gate both pass; the not-yet-implemented
	// stub returns a plain (untyped) error, not an *ExitCodeError -- it
	// must fall through to Execute()'s exit-1 fallback, not claim one of
	// the five typed codes it doesn't actually represent.
	var ece *ExitCodeError
	if errors.As(err, &ece) {
		t.Fatalf("stub error must be untyped (exit-1 fallback), got *ExitCodeError{Code: %d}", ece.Code)
	}
	if err == nil {
		t.Fatal("want a not-yet-implemented error, got nil")
	}
	if result.Setup != PhaseTeam {
		t.Errorf("Setup = %v, want PhaseTeam to flow through even on the stub error (AC-3)", result.Setup)
	}
}

func TestRun_NonHTTPSAPIURLHardRejectsEvenWhenInteractiveWithAccept(t *testing.T) {
	// Rule 1 (CheckAPIURL): non-https has no override, in any mode.
	_, err := Run(Options{
		Interactive:     true,
		SetupOverride:   PhaseTeam,
		AcceptAPIURL:    true,
		APIURLConfigVal: "http://insecure.example/api",
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != ExitNonInteractivePrecondition {
		t.Errorf("Code = %d, want ExitNonInteractivePrecondition (%d)", ece.Code, ExitNonInteractivePrecondition)
	}
}

func TestRun_NonDefaultAPIURLNonInteractiveWithoutAcceptFails(t *testing.T) {
	_, err := Run(Options{
		Interactive:     false,
		SetupOverride:   PhaseTeam,
		APIURLConfigVal: "https://self-hosted.example.com/api",
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != ExitNonInteractivePrecondition {
		t.Errorf("Code = %d, want ExitNonInteractivePrecondition (%d)", ece.Code, ExitNonInteractivePrecondition)
	}
}

func TestRun_NonDefaultAPIURLWithAcceptFlagPasses(t *testing.T) {
	result, err := Run(Options{
		Interactive:     false,
		SetupOverride:   PhaseTeam,
		AcceptAPIURL:    true,
		APIURLConfigVal: "https://self-hosted.example.com/api",
	})
	// Passes both gates; reaches the not-yet-implemented stub (untyped
	// error), not the api_url gate's typed rejection.
	var ece *ExitCodeError
	if errors.As(err, &ece) {
		t.Fatalf("expected --accept-api-url to clear the gate, got *ExitCodeError{Code: %d}: %v", ece.Code, err)
	}
	if err == nil {
		t.Fatal("want the not-yet-implemented stub error, got nil")
	}
	if result.Setup != PhaseTeam {
		t.Errorf("Setup = %v, want PhaseTeam", result.Setup)
	}
}

// TestRun_ResultSetupPropagatesOnGateFailures guards a scrutiny
// finding: Result.Setup must carry opts.SetupOverride through on the
// two gate-failure returns, not just the stub success/not-implemented
// path, per Result's own doc comment ("so a caller's --json envelope
// can still name the setup a failed run was attempting"). Before the
// fix, both gate-failure returns discarded a known override with a
// bare Result{}.
func TestRun_ResultSetupPropagatesOnGateFailures(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)

	t.Run("non-interactive precondition failure", func(t *testing.T) {
		result, err := Run(Options{Interactive: false, SetupOverride: PhaseIndividual})
		if err == nil {
			t.Fatal("want the non-TTY precondition error, got nil")
		}
		if result.Setup != PhaseIndividual {
			t.Errorf("Setup = %v, want PhaseIndividual to propagate on the precondition-failure path", result.Setup)
		}
	})

	t.Run("api_url gate failure", func(t *testing.T) {
		result, err := Run(Options{
			Interactive:     false,
			SetupOverride:   PhaseTeam,
			APIURLConfigVal: "https://self-hosted.example.com/api",
		})
		if err == nil {
			t.Fatal("want the api_url gate error, got nil")
		}
		if result.Setup != PhaseTeam {
			t.Errorf("Setup = %v, want PhaseTeam to propagate on the api_url gate-failure path", result.Setup)
		}
	})
}

// TestRun_InteractiveWithoutConfirmIsCallerBugNotPolicyFailure guards a
// maintainability finding: a caller that sets Interactive without also
// wiring Confirm (and without AcceptAPIURL) is misconfigured, not
// hitting the non-interactive precondition -- Run must not fold that
// case into ExitNonInteractivePrecondition, which would misreport a
// programmer error as a policy outcome a script might reasonably
// branch on.
func TestRun_InteractiveWithoutConfirmIsCallerBugNotPolicyFailure(t *testing.T) {
	_, err := Run(Options{
		Interactive:     true,
		SetupOverride:   PhaseTeam,
		APIURLConfigVal: "https://self-hosted.example.com/api",
		// Confirm deliberately left nil.
	})
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	var ece *ExitCodeError
	if errors.As(err, &ece) {
		t.Fatalf("a nil Confirm with Interactive=true is a caller bug, not ExitNonInteractivePrecondition -- got *ExitCodeError{Code: %d}", ece.Code)
	}
}

func TestRun_InteractiveAPIURLDeclineFails(t *testing.T) {
	declineConfirm := func(prompt string, defaultYes bool) (bool, error) { return false, nil }
	_, err := Run(Options{
		Interactive:     true,
		SetupOverride:   PhaseTeam,
		APIURLConfigVal: "https://self-hosted.example.com/api",
		Confirm:         declineConfirm,
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != ExitNonInteractivePrecondition {
		t.Errorf("Code = %d, want ExitNonInteractivePrecondition (%d)", ece.Code, ExitNonInteractivePrecondition)
	}
}
