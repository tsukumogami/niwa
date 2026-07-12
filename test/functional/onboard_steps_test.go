package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// registerOnboardSteps wires the `niwa onboard` step vocabulary: the
// Infisical management REST double (mint/verify/revoke, the R9 read
// hop, team-phase membership), the personal-overlay pointer/git
// fixtures the R22 precondition needs, and direct seeding of the CLI
// stub's credential store / the R20 mint record for the re-run
// scenarios (AC-19/20/21), which compose from state rather than a
// bespoke fixture format (design's Test-double architecture section).
func registerOnboardSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^an infisical REST double is configured$`, anInfisicalRestDoubleIsConfigured)
	ctx.Step(`^the infisical REST double has identity "([^"]*)" with client_id "([^"]*)"$`, theRestDoubleHasIdentity)
	ctx.Step(`^the infisical REST double mints client_secret "([^"]*)" with secret id "([^"]*)" for identity "([^"]*)"$`, theRestDoubleMints)
	ctx.Step(`^the infisical REST double exchanges client_secret "([^"]*)" for access token "([^"]*)"$`, theRestDoubleLoginExchange)
	ctx.Step(`^the infisical REST double serves environment secrets for project "([^"]*)" env "([^"]*)"$`, theRestDoubleServesEnvSecrets)
	ctx.Step(`^the infisical REST double grants project "([^"]*)" identity "([^"]*)" membership$`, theRestDoubleGrantsMembership)

	ctx.Step(`^the personal-overlay pointer is registered as "([^"]*)"$`, thePersonalOverlayPointerIsRegistered)
	ctx.Step(`^the personal overlay repo is git-initialized$`, thePersonalOverlayRepoIsGitInitialized)

	ctx.Step(`^the stored infisical secret "([^"]*)" at path "([^"]*)" env "([^"]*)" project "([^"]*)" exists with body:$`, theStoredInfisicalSecretExistsWithBody)
	ctx.Step(`^the onboard mint record for kind "([^"]*)" project "([^"]*)" has secret id "([^"]*)"$`, theOnboardMintRecordHasSecretID)

	ctx.Step(`^I run "([^"]*)" from workspace "([^"]*)" under a pty with input "([^"]*)"$`, iRunFromWorkspaceUnderPTYWithInput)
	ctx.Step(`^the infisical REST double recorded a request containing "([^"]*)"$`, theRestDoubleRecordedARequestContaining)
	ctx.Step(`^the infisical REST double recorded no request containing "([^"]*)"$`, theRestDoubleRecordedNoRequestContaining)
}

// anInfisicalRestDoubleIsConfigured spins up the per-scenario
// infisicalFakeServer and wires it in exactly the way the individual/
// team runners' REST calls resolve their api_url: NIWA_INFISICAL_API_URL
// (the same mechanism already proven for NIWA_GITHUB_API_URL), plus
// SSL_CERT_FILE so the niwa subprocess trusts the double's self-signed
// TLS cert -- CheckAPIURL (Decision 4) unconditionally hard-rejects a
// non-https api_url with no override, so the double must actually
// serve TLS, not plain HTTP, for a real onboard invocation to ever
// reach it (see infisicalFakeServer's own doc comment).
func anInfisicalRestDoubleIsConfigured(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.infisicalFake != nil {
		return ctx, nil
	}
	s.infisicalFake = newInfisicalFakeServer()
	s.envOverrides["NIWA_INFISICAL_API_URL"] = s.infisicalFake.URL()
	certPath := filepath.Join(s.tmpDir, "infisical-fake-cert.pem")
	if err := os.WriteFile(certPath, s.infisicalFake.CertPEM(), 0o644); err != nil {
		return ctx, fmt.Errorf("writing infisical fake cert: %w", err)
	}
	s.envOverrides["SSL_CERT_FILE"] = certPath
	return ctx, nil
}

func requireInfisicalFake(ctx context.Context) (*testState, *infisicalFakeServer, error) {
	s := getState(ctx)
	if s == nil {
		return nil, nil, fmt.Errorf("no test state")
	}
	if s.infisicalFake == nil {
		return nil, nil, fmt.Errorf("no infisical REST double; call 'an infisical REST double is configured' first")
	}
	return s, s.infisicalFake, nil
}

func theRestDoubleHasIdentity(ctx context.Context, identityID, clientID string) (context.Context, error) {
	_, fake, err := requireInfisicalFake(ctx)
	if err != nil {
		return ctx, err
	}
	fake.SetIdentityPresent(identityID, clientID)
	return ctx, nil
}

func theRestDoubleMints(ctx context.Context, clientSecret, secretID, identityID string) (context.Context, error) {
	_, fake, err := requireInfisicalFake(ctx)
	if err != nil {
		return ctx, err
	}
	fake.SetMintPresent(identityID, clientSecret, secretID)
	return ctx, nil
}

func theRestDoubleLoginExchange(ctx context.Context, clientSecret, accessToken string) (context.Context, error) {
	_, fake, err := requireInfisicalFake(ctx)
	if err != nil {
		return ctx, err
	}
	fake.SetLoginExchange(clientSecret, accessToken)
	return ctx, nil
}

func theRestDoubleServesEnvSecrets(ctx context.Context, project, env string) (context.Context, error) {
	_, fake, err := requireInfisicalFake(ctx)
	if err != nil {
		return ctx, err
	}
	fake.SetEnvironmentSecretsPresent(project, env, "/")
	return ctx, nil
}

func theRestDoubleGrantsMembership(ctx context.Context, project, identityID string) (context.Context, error) {
	_, fake, err := requireInfisicalFake(ctx)
	if err != nil {
		return ctx, err
	}
	fake.SetMembershipGranted(project, identityID)
	return ctx, nil
}

func theRestDoubleRecordedARequestContaining(ctx context.Context, substr string) error {
	_, fake, err := requireInfisicalFake(ctx)
	if err != nil {
		return err
	}
	if fake.CountRequests(substr) == 0 {
		return fmt.Errorf("no recorded request contains %q; requests: %v", substr, fake.Requests())
	}
	return nil
}

func theRestDoubleRecordedNoRequestContaining(ctx context.Context, substr string) error {
	_, fake, err := requireInfisicalFake(ctx)
	if err != nil {
		return err
	}
	if n := fake.CountRequests(substr); n != 0 {
		return fmt.Errorf("want zero requests containing %q, got %d; requests: %v", substr, n, fake.Requests())
	}
	return nil
}

// thePersonalOverlayPointerIsRegistered writes the [global_config]
// repo pointer directly into the sandboxed ~/.config/niwa/config.toml
// -- the operator-local file WriteLocalPointer itself would write --
// so R22's overlay precondition finds it already registered and skips
// straight to the overlay-repo-exists check.
func thePersonalOverlayPointerIsRegistered(ctx context.Context, repo string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	cfgDir := filepath.Join(s.homeDir, ".config", "niwa")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating global config dir: %w", err)
	}
	body := fmt.Sprintf("[global_config]\nrepo = %q\n", repo)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(body), 0o600); err != nil {
		return ctx, fmt.Errorf("writing global config: %w", err)
	}
	return ctx, nil
}

// thePersonalOverlayRepoIsGitInitialized runs `git init` in the
// sandboxed personal-overlay directory (~/.config/niwa/global) after a
// prior step has written its niwa.toml, so R22's overlay-repo-exists
// check (a bare os.Stat on .git) finds it already landed and never
// enters the scaffold-and-guide flow that would otherwise pause
// waiting for operator input this hermetic scenario never supplies.
func thePersonalOverlayRepoIsGitInitialized(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.homeDir, ".config", "niwa", "global")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating personal overlay dir: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "init")
	if out, err := cmd.CombinedOutput(); err != nil {
		return ctx, fmt.Errorf("git init: %w: %s", err, out)
	}
	return ctx, nil
}

// theStoredInfisicalSecretExistsWithBody seeds the CLI stub's
// credential store directly (bypassing a real `secrets set` call) so
// a re-run scenario can start from an already-completed individual
// setup -- composing from the same seeding the stub's own export
// round-trips, per the design's re-run fixture strategy.
func theStoredInfisicalSecretExistsWithBody(ctx context.Context, key, path, env, project string, body *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.tmpDir, "infisical-stub-store", "secrets", project, env+path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating stub store dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, key), []byte(body.Content), 0o644); err != nil {
		return ctx, fmt.Errorf("seeding stored secret: %w", err)
	}
	return ctx, nil
}

// theOnboardMintRecordHasSecretID seeds the R20 mint record
// (~/.config/niwa/onboard-mint-record-<kind>-<project>.json) so a
// re-run scenario observes a prior recorded secret id -- the
// supersession/topology-change re-mint path (AC-21) reads this to
// decide whether to best-effort revoke the superseded secret.
func theOnboardMintRecordHasSecretID(ctx context.Context, kind, project, secretID string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.homeDir, ".config", "niwa")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating config dir: %w", err)
	}
	sanitize := func(v string) string { return strings.ReplaceAll(v, "/", "_") }
	fileName := fmt.Sprintf("onboard-mint-record-%s-%s.json", sanitize(kind), sanitize(project))
	data, err := json.Marshal(map[string]string{"secret_id": secretID})
	if err != nil {
		return ctx, fmt.Errorf("marshalling mint record: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), data, 0o600); err != nil {
		return ctx, fmt.Errorf("writing mint record: %w", err)
	}
	return ctx, nil
}

// iRunFromWorkspaceUnderPTYWithInput mirrors iRunUnderPTYWithInput
// (util-linux `script -q` allocates a real pty so IsStdinTTY() reads
// true) but cd's into a named workspace's .niwa directory first,
// matching iRunFromWorkspace's own resolution -- needed for the
// auto-detect re-run scenario (AC-19), which requires Interactive to
// gate true before Detect ever runs.
func iRunFromWorkspaceUnderPTYWithInput(ctx context.Context, command, workspace, input string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if _, err := exec.LookPath("script"); err != nil {
		return ctx, fmt.Errorf("util-linux `script` not on PATH; cannot drive PTY scenario: %w", err)
	}

	args := strings.Fields(command)
	if len(args) > 0 && args[0] == "niwa" {
		args[0] = s.binPath
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	cwd := filepath.Join(s.workspaceRoot, workspace, ".niwa")
	// util-linux `script` does not propagate the wrapped command's exit
	// status as its own (confirmed empirically: `script -q -c "exit 42"
	// /dev/null` exits 0) -- every existing PTY scenario in this suite
	// only ever asserts exit 0 for exactly this reason. AC-32 needs a
	// real non-zero exit code out of a TTY-driven run, so the inner
	// command prints its own $? as a sentinel line instead of relying on
	// `exec` + script's exit status. This drops the `exec` optimization
	// (the child no longer replaces the wrapping shell), but the pty is
	// still the child's controlling terminal via inherited fds either
	// way -- `exec` was about not consuming stdin twice, not about TTY
	// visibility, and this wrapper's stdin is fed by argv/command-string
	// parsing, never by reading from the pty as a script.
	const sentinel = "NIWA_ONBOARD_PTY_EXIT_CODE"
	innerCmd := "cd " + shellQuote(cwd) + " && " + strings.Join(quoted, " ") +
		"; printf '\\n" + sentinel + ":%d\\n' \"$?\""

	cmd := exec.CommandContext(ctx, "script", "-q", "-c", innerCmd, "/dev/null")
	cmd.Env = s.buildEnv()
	rawInput := strings.ReplaceAll(input, `\n`, "\n")
	cmd.Stdin = strings.NewReader(rawInput)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr != nil {
		return ctx, fmt.Errorf("pty run failed: %w; stdout: %s; stderr: %s", runErr, stdout.String(), stderr.String())
	}

	out := stdout.String()
	idx := strings.LastIndex(out, sentinel+":")
	if idx < 0 {
		return ctx, fmt.Errorf("pty run: sentinel %s not found in output: %q", sentinel, out)
	}
	rest := out[idx+len(sentinel)+1:]
	var code int
	if _, scanErr := fmt.Sscanf(rest, "%d", &code); scanErr != nil {
		return ctx, fmt.Errorf("pty run: parsing exit code from %q: %w", rest, scanErr)
	}
	s.exitCode = code
	s.stdout = out[:idx]
	s.stderr = s.stdout + stderr.String()
	s.shellPwd = ""
	return ctx, nil
}
