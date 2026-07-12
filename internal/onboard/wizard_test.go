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

func TestResolveAPIURLForGate_ConfigValWins(t *testing.T) {
	t.Setenv(apiURLEnvVar, "https://env-value.example/api")
	got := resolveAPIURLForGate("https://config-value.example/api")
	if got != "https://config-value.example/api" {
		t.Errorf("resolveAPIURLForGate = %q, want config value to win over env", got)
	}
}

func TestResolveAPIURLForGate_EnvWinsOverDefault(t *testing.T) {
	t.Setenv(apiURLEnvVar, "https://env-value.example/api")
	got := resolveAPIURLForGate("")
	if got != "https://env-value.example/api" {
		t.Errorf("resolveAPIURLForGate = %q, want env override", got)
	}
}

func TestResolveAPIURLForGate_DefaultWhenNeitherSet(t *testing.T) {
	os.Unsetenv(apiURLEnvVar)
	got := resolveAPIURLForGate("")
	if got != cloudDefaultAPIURL {
		t.Errorf("resolveAPIURLForGate = %q, want default %q", got, cloudDefaultAPIURL)
	}
}

func TestRun_NonInteractiveNoOverrideFailsFastBeforeAPIURLGate(t *testing.T) {
	os.Unsetenv(apiURLEnvVar)
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
	os.Unsetenv(apiURLEnvVar)
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
	os.Unsetenv(apiURLEnvVar)
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
