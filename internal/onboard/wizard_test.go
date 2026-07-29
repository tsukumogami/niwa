package onboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/vault/infisical"
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

// TestRun_PhaseIndividualRequiresOptionsIndividual mirrors the
// PhaseTeam/opts.Team nil-check test: a caller that resolves
// SetupOverride to PhaseIndividual without populating Options.Individual
// is misconfigured, and must get a plain (untyped) caller-bug error --
// never silently routed anywhere.
func TestRun_PhaseIndividualRequiresOptionsIndividual(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)
	result, err := Run(Options{
		Interactive:      false,
		SetupOverride:    PhaseIndividual,
		TopologyOverride: TopologySameLogin,
	})
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	var ece *ExitCodeError
	if errors.As(err, &ece) {
		t.Fatalf("a nil Options.Individual is a caller bug, not a typed exit outcome -- got *ExitCodeError{Code: %d}", ece.Code)
	}
	if result.Setup != PhaseIndividual {
		t.Errorf("Setup = %v, want PhaseIndividual to flow through even on this caller-bug error", result.Setup)
	}
}

// TestRun_PhaseIndividualRoutesToRunIndividualSetup drives Run all the
// way through the entry sequence and into RunIndividualSetup with a
// fully populated Options.Individual, confirming the routing branch
// actually reaches the individual runner (rather than merely compiling)
// and that the redactor Run attaches is usable by it -- the read-hop
// failure below is induced deliberately so this test doesn't need a
// full REST double for every endpoint the happy path would hit.
func TestRun_PhaseIndividualRoutesToRunIndividualSetup(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)

	srv := newIndividualFakeServer()
	srv.failReadEnv = true
	httpSrv := srv.Start()
	defer httpSrv.Close()

	result, err := Run(Options{
		Interactive:      false,
		SetupOverride:    PhaseIndividual,
		TopologyOverride: TopologySameLogin,
		Individual: &IndividualSetupParams{
			APIURL:      httpSrv.URL,
			IdentityID:  "ident-123",
			Kind:        "infisical",
			Project:     testWorkspaceProject,
			Environment: "dev",
			Topology:    TopologySameLogin,
		},
	})
	if err == nil {
		t.Fatal("want the induced read-hop failure to propagate, got nil")
	}
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != ExitAuthFailure {
		t.Errorf("Code = %d, want ExitAuthFailure (%d) -- confirms Run actually reached RunIndividualSetup", ece.Code, ExitAuthFailure)
	}
	if result.Setup != PhaseIndividual {
		t.Errorf("Setup = %v, want PhaseIndividual", result.Setup)
	}
	if n := srv.CountRequests("GET /v1/auth/universal-auth/identities/ident-123"); n != 1 {
		t.Errorf("read-identity requests = %d, want 1 -- Run's redactor-attached ctx must reach the real REST call", n)
	}
}

// --- Options.Preconditions wiring (R22) ---

// TestRun_PreconditionsNilIsANoOp confirms that leaving
// Options.Preconditions nil (every test above this one does exactly
// that) keeps today's behavior unchanged -- Run must not attempt any
// R22 check when the field isn't populated.
func TestRun_PreconditionsNilIsANoOp(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)
	result, err := Run(Options{Interactive: false, SetupOverride: PhaseTeam})
	var ece *ExitCodeError
	if errors.As(err, &ece) {
		t.Fatalf("nil Preconditions must not produce a typed R22 outcome, got *ExitCodeError{Code: %d}", ece.Code)
	}
	if err == nil {
		t.Fatal("want the not-yet-implemented stub error (Options.Team nil), got nil")
	}
	if result.Setup != PhaseTeam {
		t.Errorf("Setup = %v, want PhaseTeam", result.Setup)
	}
}

// TestRun_PreconditionsSessionFailureHaltsBeforeRouting drives the R22
// session precondition through Run itself (not just the standalone
// EnsureAuthenticatedSession unit tests): a checker reporting no
// session and a nil pause function must halt Run before it ever
// reaches the api_url gate or Team/Individual routing -- proven here
// by supplying a SetupOverride that would otherwise hit the
// Options.Team nil-check, and confirming the session-precondition
// error surfaces instead.
func TestRun_PreconditionsSessionFailureHaltsBeforeRouting(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)
	checker := func(ctx context.Context) (infisical.SessionStatus, error) {
		return infisical.SessionStatus{Authenticated: false}, nil
	}

	_, err := Run(Options{
		Interactive:   false,
		SetupOverride: PhaseTeam, // opts.Team is nil; if routing were reached, THIS is the error we'd otherwise see.
		Preconditions: &PreconditionsParams{
			SessionChecker: checker,
			// Pause left nil: EnsureAuthenticatedSession errors rather
			// than blocking forever, which is exactly what proves Run
			// actually invoked it before anything else.
		},
	})
	if err == nil {
		t.Fatal("want the R22 session-precondition error, got nil")
	}
	if !strings.Contains(err.Error(), "R22 session precondition") {
		t.Errorf("err = %v, want it to name the R22 session precondition (proving Run halted there, not at the Options.Team nil-check)", err)
	}
}

// TestRun_PreconditionsOverlayFailureHaltsBeforeRouting mirrors the
// session test for the overlay half of R22: an EnsurePersonalOverlay
// failure (here, an unregistered pointer with no Repo supplied) must
// also halt Run before routing.
func TestRun_PreconditionsOverlayFailureHaltsBeforeRouting(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	alwaysAuthenticated := func(ctx context.Context) (infisical.SessionStatus, error) {
		return infisical.SessionStatus{Authenticated: true}, nil
	}

	_, err := Run(Options{
		Interactive:   false,
		SetupOverride: PhaseTeam, // opts.Team is nil; would error differently if routing were reached.
		Preconditions: &PreconditionsParams{
			SessionChecker: alwaysAuthenticated,
			Overlay: EnsurePersonalOverlayParams{
				OverlayDir: filepath.Join(t.TempDir(), "overlay"),
				// Repo deliberately empty: the pointer is unregistered
				// (fresh XDG_CONFIG_HOME above) and EnsurePersonalOverlay
				// requires a Repo to register it.
			},
		},
	})
	if err == nil {
		t.Fatal("want the R22 personal-overlay-precondition error, got nil")
	}
	if !strings.Contains(err.Error(), "R22 personal-overlay precondition") {
		t.Errorf("err = %v, want it to name the R22 personal-overlay precondition (proving Run halted there, not at the Options.Team nil-check)", err)
	}
}

// TestRun_PreconditionsPassThenReachesRouting proves the positive
// direction: when both R22 checks succeed, Run proceeds past them into
// its normal routing (reaching the same Options.Team nil-check error
// the no-preconditions tests above see), rather than the precondition
// block silently swallowing the rest of Run.
func TestRun_PreconditionsPassThenReachesRouting(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	overlayDir := filepath.Join(t.TempDir(), "overlay")
	mustGitInit(t, overlayDir)

	alwaysAuthenticated := func(ctx context.Context) (infisical.SessionStatus, error) {
		return infisical.SessionStatus{Authenticated: true}, nil
	}

	result, err := Run(Options{
		Interactive:   false,
		SetupOverride: PhaseTeam,
		Preconditions: &PreconditionsParams{
			SessionChecker: alwaysAuthenticated,
			Overlay: EnsurePersonalOverlayParams{
				OverlayDir: overlayDir, // already a git repo; pointer registration is skippable via Repo below.
				Repo:       "acme/dot-niwa-overlay",
			},
		},
	})
	var ece *ExitCodeError
	if errors.As(err, &ece) {
		t.Fatalf("expected R22 to pass and reach the Options.Team nil-check (untyped), got *ExitCodeError{Code: %d}", ece.Code)
	}
	if err == nil || !strings.Contains(err.Error(), "Options.Team must be populated") {
		t.Fatalf("err = %v, want it to reach the Options.Team nil-check, proving Run passed both R22 checks and continued routing", err)
	}
	if result.Setup != PhaseTeam {
		t.Errorf("Setup = %v, want PhaseTeam", result.Setup)
	}
}

// TestRun_PhaseVerifyOnlyRequiresOptionsVerify mirrors the
// PhaseTeam/opts.Team and PhaseIndividual/opts.Individual nil-check
// tests: R15's re-run shortcut requires Options.Verify, and a caller
// that omits it gets a plain (untyped) caller-bug error, never a
// typed exit outcome for a phase it never actually reached.
func TestRun_PhaseVerifyOnlyRequiresOptionsVerify(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)
	result, err := Run(Options{
		Interactive:   false,
		SetupOverride: PhaseVerifyOnly,
	})
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	var ece *ExitCodeError
	if errors.As(err, &ece) {
		t.Fatalf("a nil Options.Verify is a caller bug, not a typed exit outcome -- got *ExitCodeError{Code: %d}", ece.Code)
	}
	if result.Setup != PhaseVerifyOnly {
		t.Errorf("Setup = %v, want PhaseVerifyOnly to flow through even on this caller-bug error", result.Setup)
	}
}

// TestRun_PhaseVerifyOnlyRoutesToVerifyIndividual drives R15's re-run
// shortcut end to end: no mint, no store -- Run goes straight to the
// R11 wizard-end check. The GlobalOverride here deliberately declares
// no credential-sync provider, so VerifyIndividual returns its own
// setup-level ExitVerification failure; that's enough to prove Run
// actually reached VerifyIndividual (rather than merely compiling)
// without requiring a full credential-sync vault fixture.
func TestRun_PhaseVerifyOnlyRoutesToVerifyIndividual(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)
	result, err := Run(Options{
		Interactive:   false,
		SetupOverride: PhaseVerifyOnly,
		Verify: &VerifyIndividualParams{
			Kind:    "infisical",
			Project: "uuid-1",
		},
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != ExitVerification {
		t.Errorf("Code = %d, want ExitVerification (%d) -- confirms Run reached VerifyIndividual", ece.Code, ExitVerification)
	}
	if result.Setup != PhaseVerifyOnly {
		t.Errorf("Setup = %v, want PhaseVerifyOnly", result.Setup)
	}
}

// TestRun_PhaseIndividualSuccessCallsVerifyIndividual drives the full
// individual pipeline through Run (mint, R9 verify, store via a fake
// `infisical` CLI on PATH) and confirms the R11 wizard-end check runs
// immediately afterward: the credential-sync provider in Verify is
// wired to a fake commander that resolves the stored pair cleanly, so
// a nil error here can only mean Run reached RunIndividualSetup,
// succeeded, and then reached VerifyIndividual, which also succeeded.
func TestRun_PhaseIndividualSuccessCallsVerifyIndividual(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	fake := newIndividualFakeServer()
	srv := fake.Start()
	defer srv.Close()

	// A fake `infisical` CLI on PATH so RunIndividualSetup's real
	// execSecretsSetRunner subprocess call for `secrets set` succeeds
	// without a real Infisical service -- it only needs to drain
	// stdin and exit 0.
	binDir := t.TempDir()
	script := "#!/bin/sh\ncat >/dev/null\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "infisical"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake infisical: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	individualParams := baseIndividualParams(srv, t.TempDir())

	verifyCmd := &fakeVerifyCommander{
		stdout: `{"p-` + testWorkspaceProject + `": "version = \"1\"\nclient_id = \"cid\"\nclient_secret = \"csec\"\n"}`,
	}

	result, err := Run(Options{
		Interactive:      false,
		SetupOverride:    PhaseIndividual,
		TopologyOverride: TopologySameLogin,
		Individual:       &individualParams,
		Verify: &VerifyIndividualParams{
			GlobalOverride: testVerifyGlobalOverride("sync-project", verifyCmd),
			Kind:           "infisical",
			Project:        testWorkspaceProject,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error (want the full individual pipeline plus R11 check to succeed): %v", err)
	}
	if result.Setup != PhaseIndividual {
		t.Errorf("Setup = %v, want PhaseIndividual", result.Setup)
	}
}

// --- Options.Detect wiring (Issue 9: invoking the previously-unwired
// Detect funnel from Run itself) ---

// TestRun_DetectNilFallsThroughToNotImplemented pins that a caller
// which still resolves SetupOverride itself (every test above this
// one) is completely unaffected by the Detect wiring: PhaseUnknown
// with Options.Detect left nil hits the exact same untyped
// not-yet-implemented error as before.
func TestRun_DetectNilFallsThroughToNotImplemented(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)
	confirmAccept := func(prompt string, defaultYes bool) (bool, error) { return true, nil }

	_, err := Run(Options{Interactive: true, Confirm: confirmAccept})
	if err == nil {
		t.Fatal("want the not-yet-implemented stub error, got nil")
	}
	var ece *ExitCodeError
	if errors.As(err, &ece) {
		t.Fatalf("stub error must be untyped, got *ExitCodeError{Code: %d}", ece.Code)
	}
}

// TestRun_DetectTeamVaultEmptyRoutesTeam confirms Run's own Detect
// invocation resolves PhaseTeam (the free, no-network-call signal)
// and routes into the team branch -- reaching the "Options.Team must
// be populated" caller-bug error (not the not-yet-implemented stub)
// proves routing actually happened via Detect.
func TestRun_DetectTeamVaultEmptyRoutesTeam(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)
	confirmAccept := func(prompt string, defaultYes bool) (bool, error) { return true, nil }

	result, err := Run(Options{
		Interactive: true,
		Confirm:     confirmAccept,
		Detect: &DetectInputs{
			Bearer:         testBearer(),
			IdentityID:     "ident-1",
			TeamVaultEmpty: true,
		},
	})
	if err == nil {
		t.Fatal("want the Options.Team caller-bug error, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "Options.Team must be populated") {
		t.Errorf("err = %q, want it to name Options.Team (proves Detect routed to PhaseTeam)", got)
	}
	if result.Setup != PhaseTeam {
		t.Errorf("Setup = %v, want PhaseTeam", result.Setup)
	}
}

// TestRun_DetectIndividualDeclineAbortsWithExitDecline is the AC-32
// unit-level pin: when Detect infers an individual setup and the
// operator declines the confirmation prompt, Run must abort with
// ExitDecline (exit 3) -- not silently switch to the team setup. This
// is deliberately different from ConfirmSetup's own tested "decline
// switches to the other phase" contract (TestConfirmSetup_
// OverridesToOther): switching phases here would mean team-setup
// writes could occur despite the operator having said "no" to what it
// was actually asked, which AC-4 forbids.
func TestRun_DetectIndividualDeclineAbortsWithExitDecline(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)

	srv := newIndividualFakeServer() // identity found -> PhaseIndividual
	httpSrv := srv.Start()
	defer httpSrv.Close()

	var confirmPrompts []string
	confirmDecline := func(prompt string, defaultYes bool) (bool, error) {
		confirmPrompts = append(confirmPrompts, prompt)
		return false, nil
	}

	result, err := Run(Options{
		Interactive: true,
		Confirm:     confirmDecline,
		Detect: &DetectInputs{
			APIURL:     httpSrv.URL,
			Bearer:     testBearer(),
			IdentityID: "ident-1",
		},
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != ExitDecline {
		t.Errorf("Code = %d, want ExitDecline (%d)", ece.Code, ExitDecline)
	}
	if result.Setup != PhaseIndividual {
		t.Errorf("Setup = %v, want PhaseIndividual (the declined setup, still named per Result's own doc contract)", result.Setup)
	}
	if len(confirmPrompts) != 1 {
		t.Fatalf("want exactly one confirm prompt (the setup-confirmation), got %d: %v", len(confirmPrompts), confirmPrompts)
	}
	if n := srv.CountRequests("client-secrets"); n != 0 {
		t.Errorf("mint requests = %d, want 0 -- a decline must change no state (AC-4)", n)
	}
}

// TestRun_DetectIndividualAcceptRoutesThroughFullPipeline confirms the
// accept path: Detect infers individual (identity found; same-login,
// since the read-identity call just succeeded with the current
// session), the operator accepts both the setup and topology
// confirmations, and Run proceeds into the real individual pipeline
// with the detected topology threaded onto Options.Individual.
func TestRun_DetectIndividualAcceptRoutesThroughFullPipeline(t *testing.T) {
	os.Unsetenv(apiURLEnvVarForTest)

	srv := newIndividualFakeServer()
	srv.failReadEnv = true // stop the pipeline right after mint+verify-hop begins, before any store
	httpSrv := srv.Start()
	defer httpSrv.Close()

	confirmAccept := func(prompt string, defaultYes bool) (bool, error) { return true, nil }

	result, err := Run(Options{
		Interactive: true,
		Confirm:     confirmAccept,
		Detect: &DetectInputs{
			APIURL:     httpSrv.URL,
			Bearer:     testBearer(),
			IdentityID: "ident-1",
		},
		Individual: &IndividualSetupParams{
			APIURL:      httpSrv.URL,
			IdentityID:  "ident-1",
			Kind:        "infisical",
			Project:     testWorkspaceProject,
			Environment: "dev",
			// Topology deliberately left unset here: Run must set it
			// from the confirmed detection result, not require the
			// caller to have already guessed it.
		},
	})
	if result.Setup != PhaseIndividual {
		t.Errorf("Setup = %v, want PhaseIndividual", result.Setup)
	}
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != ExitAuthFailure {
		t.Errorf("Code = %d, want ExitAuthFailure (%d) -- confirms Run reached RunIndividualSetup with a resolved topology", ece.Code, ExitAuthFailure)
	}
}
