package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/onboard"
)

// setupOnboardTestEnv gives runOnboard a real, valid workspace config to
// load: a temp instance directory declaring [vault.provider] with every
// field loadOnboardConfig requires, chdir'd into for the duration of the
// test (t.Chdir auto-restores on cleanup), plus a sandboxed HOME/
// XDG_CONFIG_HOME so config.GlobalConfigDir() resolves somewhere
// writable and never touches the real developer machine's config. The
// operator-bearer escape hatch is set so resolveOperatorBearer succeeds
// without a real `infisical login` session.
func setupOnboardTestEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(operatorBearerEnvOverride, "test-operator-token")

	instanceDir := t.TempDir()
	niwaDir := filepath.Join(instanceDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatalf("creating .niwa dir: %v", err)
	}
	cfg := "[workspace]\n" +
		"name = \"test-ws\"\n" +
		"\n" +
		"[vault.provider]\n" +
		"kind = \"infisical\"\n" +
		"project = \"proj-1\"\n" +
		"identity_id = \"ident-1\"\n" +
		"identity_name = \"Test Identity\"\n" +
		"env = \"dev\"\n"
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("writing workspace.toml: %v", err)
	}
	t.Chdir(instanceDir)

	// A real `infisical` binary may happen to be on the machine's PATH
	// (it is on this repo's own dev/CI image, installed via tsuku for
	// the functional suite) -- without a stub ahead of it, R22's
	// EnsureAuthenticatedSession precondition would shell out to it
	// for real and print its own login-status guidance to stdout,
	// making these plain CLI-plumbing unit tests depend on whatever
	// session state that real binary happens to have. Install a
	// minimal stub reporting "authenticated" first on PATH so R22's
	// precondition is a no-op here, exactly like the functional
	// suite's own writeFakeInfisical does for the same reason.
	installFakeInfisicalOnPath(t)
}

// installFakeInfisicalOnPath writes a tiny `infisical login status
// --json` stub reporting an authenticated session and prepends its
// directory to PATH for the duration of the test, so R22's session
// precondition is satisfied without depending on the host machine's
// real infisical CLI or session state.
func installFakeInfisicalOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"login\" ] && [ \"$2\" = \"status\" ]; then\n" +
		"  printf '{\"sessions\":[{\"principalType\":\"user\",\"status\":\"authenticated\",\"organization\":\"test-org\"}]}\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "infisical"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake infisical stub: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestOnboardCmd_WiredIntoRoot asserts onboard is registered on the
// root command as a single command with no subcommands (AC-1).
func TestOnboardCmd_WiredIntoRoot(t *testing.T) {
	found := false
	for _, c := range rootCmd.Commands() {
		if c.Name() == "onboard" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected 'onboard' command to be wired into rootCmd")
	}
	if len(onboardCmd.Commands()) != 0 {
		t.Errorf("onboard must have no subcommands, got %d", len(onboardCmd.Commands()))
	}
}

// resetOnboardFlags restores every onboard flag var to its zero value.
// Tests mutate the package-level flag vars directly (matching the
// existing init.go/init_test.go convention) rather than going through
// cobra flag parsing.
func resetOnboardFlags() {
	onboardTeam = false
	onboardIndividual = false
	onboardSameLogin = false
	onboardSplitLogin = false
	onboardJSON = false
	onboardAcceptAPIURL = false
}

func TestRunOnboard_ConflictingTeamIndividualIsPlainExitOne(t *testing.T) {
	resetOnboardFlags()
	defer resetOnboardFlags()
	onboardTeam = true
	onboardIndividual = true

	err := runOnboard(onboardCmd, nil)
	if err == nil {
		t.Fatal("want mutual-exclusion error, got nil")
	}
	var ece *onboard.ExitCodeError
	if errors.As(err, &ece) {
		t.Fatalf("conflicting --team/--individual must be a plain exit-1 error, not a typed ExitCodeError (Code=%d)", ece.Code)
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want substring 'mutually exclusive'", err.Error())
	}
}

func TestRunOnboard_ConflictingSameSplitLoginIsPlainExitOne(t *testing.T) {
	resetOnboardFlags()
	defer resetOnboardFlags()
	onboardSameLogin = true
	onboardSplitLogin = true

	err := runOnboard(onboardCmd, nil)
	if err == nil {
		t.Fatal("want mutual-exclusion error, got nil")
	}
	var ece *onboard.ExitCodeError
	if errors.As(err, &ece) {
		t.Fatalf("conflicting --same-login/--split-login must be a plain exit-1 error, got ExitCodeError (Code=%d)", ece.Code)
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want substring 'mutually exclusive'", err.Error())
	}
}

func TestRunOnboard_TeamWithTopologyFlagIsUsageConflict(t *testing.T) {
	resetOnboardFlags()
	defer resetOnboardFlags()
	onboardTeam = true
	onboardSameLogin = true

	err := runOnboard(onboardCmd, nil)
	if err == nil {
		t.Fatal("want usage-conflict error, got nil")
	}
	var ece *onboard.ExitCodeError
	if errors.As(err, &ece) {
		t.Fatalf("--team combined with --same-login must be a plain exit-1 error, got ExitCodeError (Code=%d)", ece.Code)
	}
}

func TestRunOnboard_NonTTYNoOverrideFailsFastExitTwo(t *testing.T) {
	resetOnboardFlags()
	defer resetOnboardFlags()
	setupOnboardTestEnv(t)
	// onboard.IsStdinTTY defaults to real term detection, which is false
	// under `go test` (stdin isn't a terminal), so no stub is needed for
	// the "non-interactive" half of this test -- but stub explicitly so
	// the test doesn't depend on however the test binary happens to be
	// invoked.
	prevTTY := onboard.IsStdinTTY
	onboard.IsStdinTTY = func() bool { return false }
	defer func() { onboard.IsStdinTTY = prevTTY }()

	err := runOnboard(onboardCmd, nil)
	var ece *onboard.ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *onboard.ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != onboard.ExitNonInteractivePrecondition {
		t.Errorf("Code = %d, want ExitNonInteractivePrecondition (%d)", ece.Code, onboard.ExitNonInteractivePrecondition)
	}
}

func TestRunOnboard_NonTTYWithTeamOverridePassesPreconditionGate(t *testing.T) {
	resetOnboardFlags()
	defer resetOnboardFlags()
	setupOnboardTestEnv(t)
	onboardTeam = true
	prevTTY := onboard.IsStdinTTY
	onboard.IsStdinTTY = func() bool { return false }
	defer func() { onboard.IsStdinTTY = prevTTY }()

	result, err := resolveAndRunOnboard(onboardCmd)
	// Precondition and api_url gate both pass with a bare --team
	// override, routing into the real team runner instead of the
	// exit-2 fail-fast -- AC-3's "the override forces the named
	// setup". What happens inside the team runner from here on is a
	// subprocess/network integration concern (there is no real
	// `infisical` session in this unit test's sandbox) covered by the
	// functional Gherkin suite, not asserted here; this test only
	// pins that the CLI wiring reached the team phase at all rather
	// than failing the non-TTY precondition.
	var ece *onboard.ExitCodeError
	if errors.As(err, &ece) && ece.Code == onboard.ExitNonInteractivePrecondition {
		t.Fatalf("expected --team to satisfy the non-TTY precondition, got ExitNonInteractivePrecondition: %v", err)
	}
	if result.Setup != onboard.PhaseTeam {
		t.Errorf("Setup = %v, want PhaseTeam (AC-3 override forces the named setup)", result.Setup)
	}
}

func TestRunOnboard_JSONEnvelopeShape(t *testing.T) {
	resetOnboardFlags()
	defer resetOnboardFlags()
	setupOnboardTestEnv(t)
	onboardJSON = true
	prevTTY := onboard.IsStdinTTY
	onboard.IsStdinTTY = func() bool { return false }
	defer func() { onboard.IsStdinTTY = prevTTY }()

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := runOnboard(cmd, nil)
	if err == nil {
		t.Fatal("want the non-TTY fail-fast error, got nil")
	}

	var env map[string]any
	if decodeErr := json.Unmarshal(buf.Bytes(), &env); decodeErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (stdout: %q)", decodeErr, buf.String())
	}
	for _, key := range []string{"status", "exit_code", "detail"} {
		if _, ok := env[key]; !ok {
			t.Errorf("envelope missing key %q: %v", key, env)
		}
	}
	if got := env["exit_code"]; got != float64(onboard.ExitNonInteractivePrecondition) {
		t.Errorf("exit_code = %v, want %d", got, onboard.ExitNonInteractivePrecondition)
	}
	if env["status"] != "non_interactive_precondition_failed" {
		t.Errorf("status = %v, want non_interactive_precondition_failed", env["status"])
	}
	for _, secretish := range []string{"client_secret", "clientSecret"} {
		if _, ok := env[secretish]; ok {
			t.Errorf("envelope must never carry a secret-shaped key %q", secretish)
		}
	}
}

// TestRunOnboard_JSONEnvelopeEmittedOnFlagConflict guards a scrutiny
// finding: --json must emit exactly one envelope on stdout for every
// terminal outcome, including the flag-conflict usage errors -- not
// just the wizard's own gate/stub outcomes. Before the fix, the three
// mutual-exclusion checks returned before runOnboard ever reached the
// --json block, so `niwa onboard --json --team --individual` silently
// produced zero JSON objects despite --json's own flag help text.
func TestRunOnboard_JSONEnvelopeEmittedOnFlagConflict(t *testing.T) {
	resetOnboardFlags()
	defer resetOnboardFlags()
	onboardJSON = true
	onboardTeam = true
	onboardIndividual = true

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := runOnboard(cmd, nil)
	if err == nil {
		t.Fatal("want the mutual-exclusion error, got nil")
	}

	var env map[string]any
	if decodeErr := json.Unmarshal(buf.Bytes(), &env); decodeErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (stdout: %q)", decodeErr, buf.String())
	}
	if env["exit_code"] != float64(1) {
		t.Errorf("exit_code = %v, want 1 (untyped fallback for a plain usage conflict)", env["exit_code"])
	}
	if !strings.Contains(env["detail"].(string), "mutually exclusive") {
		t.Errorf("detail = %v, want substring 'mutually exclusive'", env["detail"])
	}
}

func TestRunOnboard_JSONEnvelopeCarriesSetupOnStubError(t *testing.T) {
	resetOnboardFlags()
	defer resetOnboardFlags()
	setupOnboardTestEnv(t)
	onboardJSON = true
	onboardTeam = true
	prevTTY := onboard.IsStdinTTY
	onboard.IsStdinTTY = func() bool { return false }
	defer func() { onboard.IsStdinTTY = prevTTY }()

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	if err := runOnboard(cmd, nil); err == nil {
		t.Fatal("want the not-yet-implemented stub error, got nil")
	}

	var env map[string]any
	if decodeErr := json.Unmarshal(buf.Bytes(), &env); decodeErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (stdout: %q)", decodeErr, buf.String())
	}
	if env["setup"] != "team" {
		t.Errorf("setup = %v, want \"team\"", env["setup"])
	}
	if env["exit_code"] != float64(1) {
		t.Errorf("exit_code = %v, want 1 (untyped fallback for the not-yet-implemented stub)", env["exit_code"])
	}
	if env["status"] != "error" {
		t.Errorf("status = %v, want \"error\"", env["status"])
	}
}
