package infisical

// Tests for failure classification. Comments such as "R2" or "R9" name
// requirements in docs/prds/PRD-dispatch-offline-secrets.md; the
// "rules" classify.go numbers are the ordered rules inside its R2.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tsukumogami/niwa/internal/vault"
)

// Export stderr texts, the same the golden fixtures in
// internal/vault/resolve/testdata/golden were recorded against.
const (
	stderrNoValidSession    = "error: No valid login session found, cannot perform this action. Please run [infisical login] manually\n"
	stderrCouldNotFindLogin = "error: we couldn't find your logged in details, try running [infisical login] then try again\n"
	stderrSessionExpired    = "error: Your login session has expired, please run [infisical login] and try again\n"
	stderrResponse401       = "error: CallGetRawSecretsV3: Unsuccessful response. Please make sure your secret path, workspace and environment name are all correct\nResponse Code: 401\nMessage: Token missing or invalid\n"
	stderrResponse403       = "error: CallGetRawSecretsV3: Unsuccessful response. Please make sure your secret path, workspace and environment name are all correct\nResponse Code: 403\nMessage: You do not have permission to read secrets in this environment\n"
	stderrResponse404       = "error: CallGetRawSecretsV3: Unsuccessful response. Please make sure your secret path, workspace and environment name are all correct\nResponse Code: 404\nMessage: Folder with path '/golden' in environment with slug 'dev' not found\n"
	stderrResponse500       = "error: CallGetRawSecretsV3: Unsuccessful response. Please make sure your secret path, workspace and environment name are all correct\nResponse Code: 500\nMessage: Something went wrong\n"
	stderrLoggedOutAnd404   = "error: No valid login session found, cannot perform this action. Please run [infisical login] manually\nResponse Code: 404\nMessage: Project not found\n"
	stderrConnRefused       = "error: CallGetRawSecretsV3: Unable to complete api request [err=Get \"https://app.infisical.com/api/v3/secrets/raw\": dial tcp 127.0.0.1:443: connect: connection refused]\n"
)

// Probe answers.
const (
	probeNoSessions  = `{"sessions":[]}`
	probeVerified    = `{"sessions":[{"principalType":"user","status":"authenticated","tokenSource":"keyring","verification":{"state":"verified"}}]}`
	probeExpired     = `{"sessions":[{"status":"expired","tokenSource":"keyring"}]}`
	probeRejected    = `{"sessions":[{"status":"rejected","tokenSource":"keyring"}]}`
	probePending     = `{"sessions":[{"status":"pending","tokenSource":"keyring"}]}`
	probeUnverified  = `{"sessions":[{"status":"authenticated","tokenSource":"keyring","verification":{"state":"unknown"}}]}`
	probeOtherDomain = `{"sessions":[{"status":"authenticated","domain":"https://eu.infisical.com","tokenSource":"keyring","verification":{"state":"verified"}}]}`
	probeExpiredThen = `{"sessions":[{"status":"expired","tokenSource":"keyring"},{"status":"authenticated","tokenSource":"keyring","verification":{"state":"verified"}}]}`
	probeVerifiedThe = `{"sessions":[{"status":"authenticated","tokenSource":"keyring","verification":{"state":"verified"}},{"status":"expired","tokenSource":"keyring"}]}`
	probeEnvRejected = `{"sessions":[{"status":"authenticated","tokenSource":"keyring","verification":{"state":"verified"}},{"status":"rejected","tokenSource":"env:INFISICAL_TOKEN"}]}`
	probeNotJSON     = "You are logged in as someone\n"
)

// scriptCommander answers `infisical export` and `infisical login
// status` separately and counts both. A hang blocks until the call's
// context is done, the way a stuck CLI is cut off by its bound.
type scriptCommander struct {
	exportStdout, exportStderr string
	exportCode                 int
	exportHang                 bool
	exportStartErr             error

	probeStdout, probeStderr string
	probeHang                bool

	exports, probes atomic.Int32
}

func (c *scriptCommander) Run(ctx context.Context, _ string, args []string) ([]byte, []byte, int, error) {
	if len(args) >= 2 && args[0] == "login" && args[1] == "status" {
		c.probes.Add(1)
		if c.probeHang {
			<-ctx.Done()
			return nil, nil, -1, nil
		}
		return []byte(c.probeStdout), []byte(c.probeStderr), 0, nil
	}
	c.exports.Add(1)
	if c.exportStartErr != nil {
		return nil, nil, -1, c.exportStartErr
	}
	if c.exportHang {
		<-ctx.Done()
		return nil, nil, -1, nil
	}
	return []byte(c.exportStdout), []byte(c.exportStderr), c.exportCode, nil
}

// goldenErrorText returns the error section of a resolve golden fixture.
func goldenErrorText(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "resolve", "testdata", "golden", name+".golden"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, _ := strings.Cut(string(b), "error:\n")
	text, _, _ := strings.Cut(rest, "\nkey report:\n")
	return text
}

// toleratedAuthFailureText is the text a 401/403 export failure has always
// produced: the auth-failure wording wrapping ErrProviderUnreachable.
func toleratedAuthFailureText(stderr string) string {
	return "infisical: export exited 1 (auth failure): " + strings.TrimSpace(stderr) + ": " + vault.ErrProviderUnreachable.Error()
}

func TestClassifyExportFailure(t *testing.T) {
	quietOverride(t, "100ms")
	loggedOut := vault.FailureClass{Class: vault.ClassUnauthenticated, Reason: vault.ReasonLoggedOut}
	unreachable := vault.FailureClass{Class: vault.ClassUnreachable, Reason: vault.ReasonUnreachable}
	timedOut := vault.FailureClass{Class: vault.ClassUnreachable, Reason: vault.ReasonTimedOut}
	answered := vault.FailureClass{Class: vault.ClassAnswered}
	withStatus := func(c vault.FailureClass, n int) vault.FailureClass { c.HTTPStatus = n; return c }

	cases := []struct {
		name       string
		stderr     string
		exportHang bool
		probe      string
		probeErr   string
		probeHang  bool
		envToken   string
		minted     bool
		want       vault.FailureClass
		// wantProbes is how many probes the classification runs.
		wantProbes int32
		// fixture names the golden fixture whose recorded error text
		// the error must end with; text is the literal text instead.
		fixture string
		text    string
	}{
		// R1/R2: an expired or rejected deciding session.
		{name: "expired session", stderr: stderrNoValidSession, probe: probeExpired, want: loggedOut, wantProbes: 1, fixture: "logged-out-no-valid-session"},
		{name: "rejected session", stderr: stderrNoValidSession, probe: probeRejected, want: loggedOut, wantProbes: 1, fixture: "logged-out-no-valid-session"},
		// R2: other probe answers.
		{name: "verification unknown", stderr: stderrNoValidSession, probe: probeUnverified, want: unreachable, wantProbes: 1, fixture: "logged-out-no-valid-session"},
		{name: "malformed token", stderr: stderrConnRefused, probeErr: "error: token is malformed\n", want: loggedOut, wantProbes: 1, fixture: "connection-refused"},
		{name: "403 verified on another domain", stderr: stderrResponse403, probe: probeOtherDomain, want: withStatus(answered, 403), wantProbes: 1, text: toleratedAuthFailureText(stderrResponse403)},
		{name: "expired first then verified", stderr: stderrNoValidSession, probe: probeExpiredThen, want: loggedOut, wantProbes: 1, fixture: "logged-out-no-valid-session"},
		{name: "verified first then expired", stderr: stderrConnRefused, probe: probeVerifiedThe, want: unreachable, wantProbes: 1, fixture: "connection-refused"},
		// The same export as "expired first then verified": only the
		// session order differs, and it alone flips the class.
		{name: "verified first then expired, same export", stderr: stderrNoValidSession, probe: probeVerifiedThe, want: unreachable, wantProbes: 1},
		{name: "pending session", stderr: stderrConnRefused, probe: probePending, want: unreachable, wantProbes: 1, fixture: "connection-refused"},
		{name: "env token session rejected, stored login listed first", stderr: stderrConnRefused, probe: probeEnvRejected, envToken: "t", want: loggedOut, wantProbes: 1, fixture: "connection-refused"},
		{name: "env token session rejected with a 401", stderr: stderrResponse401, probe: probeEnvRejected, envToken: "t", want: withStatus(loggedOut, 401), wantProbes: 1, text: toleratedAuthFailureText(stderrResponse401)},
		{name: "env token set, no session names it", stderr: stderrConnRefused, probe: probeVerified, envToken: "t", want: loggedOut, wantProbes: 1, fixture: "connection-refused"},
		// R2, R11: server responses.
		{name: "404 with a probe that would time out", stderr: stderrResponse404, probeHang: true, want: withStatus(answered, 404), fixture: "response-404"},
		{name: "404 with no sessions", stderr: stderrResponse404, probe: probeNoSessions, want: withStatus(answered, 404), fixture: "response-404"},
		{name: "404 with an expired session", stderr: stderrResponse404, probe: probeExpired, want: withStatus(answered, 404), fixture: "response-404"},
		{name: "401 with a timed-out probe", stderr: stderrResponse401, probeHang: true, want: withStatus(loggedOut, 401), wantProbes: 1, text: toleratedAuthFailureText(stderrResponse401)},
		{name: "403 with a timed-out probe", stderr: stderrResponse403, probeHang: true, want: withStatus(loggedOut, 403), wantProbes: 1, text: toleratedAuthFailureText(stderrResponse403)},
		{name: "403 with an expired session", stderr: stderrResponse403, probe: probeExpired, want: withStatus(loggedOut, 403), wantProbes: 1, text: toleratedAuthFailureText(stderrResponse403)},
		{name: "403 with no sessions", stderr: stderrResponse403, probe: probeNoSessions, want: withStatus(loggedOut, 403), wantProbes: 1, text: toleratedAuthFailureText(stderrResponse403)},
		// A probe answer without a sessions list is no answer.
		{name: "403 with a null probe", stderr: stderrResponse403, probe: "null", want: withStatus(loggedOut, 403), wantProbes: 1, text: toleratedAuthFailureText(stderrResponse403)},
		{name: "connection refused, probe {}", stderr: stderrConnRefused, probe: "{}", want: unreachable, wantProbes: 1, fixture: "connection-refused"},
		{name: "logged out, probe {}", stderr: stderrNoValidSession, probe: "{}", want: loggedOut, wantProbes: 1, fixture: "logged-out-no-valid-session"},
		// R2: no server response.
		{name: "connection refused, verified", stderr: stderrConnRefused, probe: probeVerified, want: unreachable, wantProbes: 1, fixture: "connection-refused"},
		{name: "logged out, no sessions", stderr: stderrNoValidSession, probe: probeNoSessions, want: loggedOut, wantProbes: 1, fixture: "logged-out-no-valid-session"},
		// R2, R4, R20: the wording fallback.
		{name: "no valid session wording, probe not JSON", stderr: stderrNoValidSession, probe: probeNotJSON, want: loggedOut, wantProbes: 1, fixture: "logged-out-no-valid-session"},
		{name: "could not find login wording, probe not JSON", stderr: stderrCouldNotFindLogin, probe: probeNotJSON, want: loggedOut, wantProbes: 1, fixture: "logged-out-could-not-find-login"},
		{name: "session expired wording, probe not JSON", stderr: stderrSessionExpired, probe: probeNotJSON, want: loggedOut, wantProbes: 1, fixture: "logged-out-session-expired"},
		{name: "session expired wording, probe timed out", stderr: stderrSessionExpired, probeHang: true, want: loggedOut, wantProbes: 1, fixture: "logged-out-session-expired"},
		{name: "wording with a verified session", stderr: stderrNoValidSession, probe: probeVerified, want: unreachable, wantProbes: 1, fixture: "logged-out-no-valid-session"},
		{name: "no wording, probe not JSON", stderr: stderrConnRefused, probe: probeNotJSON, want: unreachable, wantProbes: 1, fixture: "connection-refused"},
		{name: "no wording, probe timed out", stderr: stderrConnRefused, probeHang: true, want: timedOut, wantProbes: 1, fixture: "connection-refused"},
		// R2, R4, R6: a server response beats the wording.
		{name: "wording and 404, verified", stderr: stderrLoggedOutAnd404, probe: probeVerified, want: withStatus(answered, 404), fixture: "logged-out-and-response-404"},
		// R6: answered failures keep today's handling.
		{name: "403 verified", stderr: stderrResponse403, probe: probeVerified, want: withStatus(answered, 403), wantProbes: 1, text: toleratedAuthFailureText(stderrResponse403)},
		{name: "500 verified", stderr: stderrResponse500, probe: probeVerified, want: withStatus(answered, 500), fixture: "response-500"},
		// Only a line that begins with "Response Code: " is a response.
		{name: "status digits elsewhere", stderr: "error: upstream said 404 Response Code: 404 at 10.0.0.1:403\n", probe: probeVerified, want: unreachable, wantProbes: 1, text: toleratedAuthFailureText("error: upstream said 404 Response Code: 404 at 10.0.0.1:403")},
		// R3: a minted principal is never probed.
		{name: "minted 401", stderr: stderrResponse401, minted: true, want: withStatus(answered, 401), text: toleratedAuthFailureText(stderrResponse401)},
		{name: "minted 404", stderr: stderrResponse404, minted: true, want: withStatus(answered, 404), fixture: "response-404"},
		{name: "minted connection refused", stderr: stderrConnRefused, minted: true, want: unreachable, fixture: "connection-refused"},
		{name: "minted timeout", exportHang: true, minted: true, want: timedOut, text: "infisical: export timed out after 100ms: vault: provider unreachable"},
		// Export timeouts for a CLI-session principal.
		{name: "export timeout, no sessions", exportHang: true, probe: probeNoSessions, want: loggedOut, wantProbes: 1, text: "infisical: export timed out after 100ms: vault: provider unreachable"},
		{name: "export timeout, verified", exportHang: true, probe: probeVerified, want: timedOut, wantProbes: 1, text: "infisical: export timed out after 100ms: vault: provider unreachable"},
		{name: "export and probe time out", exportHang: true, probeHang: true, want: timedOut, wantProbes: 1, text: "infisical: export timed out after 100ms: vault: provider unreachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tokenEnvVar, tc.envToken)
			c := &scriptCommander{
				exportStderr: tc.stderr, exportCode: 1, exportHang: tc.exportHang,
				probeStdout: tc.probe, probeStderr: tc.probeErr, probeHang: tc.probeHang,
			}
			token := ""
			if tc.minted {
				token = "minted-jwt"
			}
			_, _, err := runInfisicalExport(context.Background(), c, "golden-team-project", "dev", "/", token)

			var ce *vault.ClassifiedError
			if !errors.As(err, &ce) {
				t.Fatalf("err = %v (%T), want a *vault.ClassifiedError", err, err)
			}
			if ce.Class != tc.want {
				t.Errorf("class = %s / %s / %d, want %s / %s / %d",
					ce.Class.Class, ce.Class.Reason, ce.Class.HTTPStatus, tc.want.Class, tc.want.Reason, tc.want.HTTPStatus)
			}
			if ce.Class.Class == vault.ClassUnauthenticated && ce.Class.Reason != vault.ReasonLoggedOut {
				t.Errorf("an unauthenticated result carries reason %s", ce.Class.Reason)
			}
			if tc.want.Class != vault.ClassAnswered && !errors.Is(err, vault.ErrProviderUnreachable) {
				t.Errorf("a %s failure must match ErrProviderUnreachable: %v", tc.want.Class, err)
			}
			if errors.Is(err, vault.ErrClientNotInstalled) {
				t.Errorf("a started export reported as not installed: %v", err)
			}
			if got := c.probes.Load(); got != tc.wantProbes {
				t.Errorf("probes = %d, want %d", got, tc.wantProbes)
			}
			if got := c.exports.Load(); got != 1 {
				t.Errorf("exports = %d, want 1", got)
			}
			switch {
			case tc.fixture != "":
				want := goldenErrorText(t, tc.fixture)
				if !strings.HasSuffix(want, `via provider "(anonymous)": `+err.Error()) {
					t.Errorf("error text differs from fixture %s\nfixture: %s\ngot:     %s", tc.fixture, want, err.Error())
				}
			case tc.text != "":
				if err.Error() != tc.text {
					t.Errorf("error text = %q, want %q", err.Error(), tc.text)
				}
			}
		})
	}
}

// An answered failure keeps exactly today's sentinel: a 404 or 500 is a
// hard error, a 403 the tolerated mark.
func TestClassifyAnsweredKeepsSentinel(t *testing.T) {
	t.Setenv(tokenEnvVar, "")
	for _, tc := range []struct {
		stderr          string
		wantUnreachable bool
	}{
		{stderrResponse404, false},
		{stderrResponse500, false},
		{stderrLoggedOutAnd404, false},
		{stderrResponse403, true},
	} {
		c := &scriptCommander{exportStderr: tc.stderr, exportCode: 1, probeStdout: probeVerified}
		_, _, err := runInfisicalExport(context.Background(), c, "p", "dev", "/", "")
		if got := errors.Is(err, vault.ErrProviderUnreachable); got != tc.wantUnreachable {
			t.Errorf("%q: errors.Is(ErrProviderUnreachable) = %v, want %v", strings.SplitN(tc.stderr, "\n", 3)[1], got, tc.wantUnreachable)
		}
	}
}

// Malformed output from an export that exited 0 is answered and needs no
// probe.
func TestClassifyMalformedJSONIsAnswered(t *testing.T) {
	c := &scriptCommander{exportStdout: "not json"}
	_, _, err := runInfisicalExport(context.Background(), c, "p", "dev", "/", "")
	var ce *vault.ClassifiedError
	if !errors.As(err, &ce) || ce.Class.Class != vault.ClassAnswered {
		t.Fatalf("err = %v, want an answered ClassifiedError", err)
	}
	if errors.Is(err, vault.ErrProviderUnreachable) || c.probes.Load() != 0 {
		t.Errorf("malformed output: unreachable=%v probes=%d, want false and 0", errors.Is(err, vault.ErrProviderUnreachable), c.probes.Load())
	}
}

// R0: a missing binary keeps today's handling, carries no class and runs
// no probe.
func TestClassifyClientNotInstalled(t *testing.T) {
	c := &scriptCommander{exportStartErr: &exec.Error{Name: "infisical", Err: exec.ErrNotFound}}
	_, _, err := runInfisicalExport(context.Background(), c, "p", "dev", "/", "")
	if !errors.Is(err, vault.ErrClientNotInstalled) {
		t.Fatalf("err = %v, want ErrClientNotInstalled", err)
	}
	var fc *vault.FailureClass
	if errors.As(err, &fc) {
		t.Errorf("a start failure carries class %+v, want none", *fc)
	}
	if c.probes.Load() != 0 {
		t.Errorf("probes = %d, want 0", c.probes.Load())
	}
	if want := goldenErrorText(t, "client-not-installed"); want != "<nil>" {
		t.Fatalf("client-not-installed fixture changed: %s", want)
	}
}

// R22: nothing from the probe reaches an error, even when it carries a
// token.
func TestClassifyNeverEchoesProbeOutput(t *testing.T) {
	const marker = "probe-token-marker-7f3a"
	t.Setenv(tokenEnvVar, "")
	probes := []string{
		`{"sessions":[{"status":"expired","token":"` + marker + `"}]}`,
		`{"sessions":[{"status":"authenticated","token":"` + marker + `","verification":{"state":"verified"}}]}`,
		"token: " + marker + "\n",
	}
	for _, probe := range probes {
		for _, stderr := range []string{stderrNoValidSession, stderrResponse403, stderrResponse404, stderrConnRefused} {
			c := &scriptCommander{exportStderr: stderr, exportCode: 1, probeStdout: probe, probeStderr: "error: token is malformed: " + marker}
			_, _, err := runInfisicalExport(context.Background(), c, "p", "dev", "/", "")
			if err == nil || strings.Contains(err.Error(), marker) {
				t.Errorf("error %v contains the probe's token", err)
			}
			var fc *vault.FailureClass
			if errors.As(err, &fc) && strings.Contains(fc.Error(), marker) {
				t.Errorf("class text %q contains the probe's token", fc.Error())
			}
		}
	}
}

// R26: a successful export starts one export and no probe.
func TestClassifySuccessRunsNoProbe(t *testing.T) {
	c := &scriptCommander{exportStdout: `{"K":"v"}`}
	p := openScript(t, c, nil)
	for _, path := range []string{"", "", "other"} {
		if _, _, err := p.Resolve(context.Background(), vault.Ref{Path: path, Key: "K"}); err != nil {
			t.Fatal(err)
		}
	}
	if c.exports.Load() != 2 || c.probes.Load() != 0 {
		t.Errorf("exports=%d probes=%d, want 2 and 0", c.exports.Load(), c.probes.Load())
	}
}

// openScript opens a provider over c with any extra config.
func openScript(t *testing.T, c commander, extra vault.ProviderConfig) *Provider {
	t.Helper()
	cfg := vault.ProviderConfig{"project": "proj-1", "_commander": c}
	for k, v := range extra {
		cfg[k] = v
	}
	p, err := (Factory{}).Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p.(*Provider)
}

func TestStoreIdentity(t *testing.T) {
	t.Setenv(apiURLEnvOverride, "")
	c := &scriptCommander{}
	p := openScript(t, c, vault.ProviderConfig{"env": "prod", "path": "team/"})
	var _ vault.StoreIdentifier = p

	id, ok := p.StoreIdentity(vault.Ref{Key: "K"})
	want := vault.Identity{Kind: "infisical", APIDomain: "https://app.infisical.com", ProjectID: "proj-1", Environment: "prod", FolderPath: "/team"}
	if !ok || id != want {
		t.Errorf("StoreIdentity(no path) = %+v, %v; want %+v", id, ok, want)
	}
	id, _ = p.StoreIdentity(vault.Ref{Path: "sub/dir", Key: "K"})
	if id.FolderPath != "/sub/dir" {
		t.Errorf("ref folder = %q, want /sub/dir", id.FolderPath)
	}
	id, _ = openScript(t, c, nil).StoreIdentity(vault.Ref{Key: "K"})
	if id.FolderPath != "/" || id.Environment != "dev" {
		t.Errorf("defaults = %+v, want folder / and env dev", id)
	}

	// api_url in the config wins; the environment override is next.
	id, _ = openScript(t, c, vault.ProviderConfig{"api_url": "https://Vault.Example.com/api/"}).StoreIdentity(vault.Ref{})
	if id.APIDomain != "https://vault.example.com" {
		t.Errorf("config api_url domain = %q", id.APIDomain)
	}
	t.Setenv(apiURLEnvOverride, "https://env.example.com/api")
	id, _ = openScript(t, c, nil).StoreIdentity(vault.Ref{})
	if id.APIDomain != "https://env.example.com" {
		t.Errorf("env api_url domain = %q", id.APIDomain)
	}

	if _, err := (Factory{}).Open(context.Background(), vault.ProviderConfig{"project": "p", "api_url": 3}); err == nil {
		t.Error("a non-string api_url was accepted")
	}
	// Reading api_url passes nothing new to the CLI.
	sc := &scriptCommander{exportStdout: `{}`}
	rec := &argRecorder{inner: sc}
	pp := openScript(t, rec, vault.ProviderConfig{"api_url": "https://vault.example.com/api"})
	_, _, _ = pp.Resolve(context.Background(), vault.Ref{Key: "K"})
	for _, a := range rec.args {
		if strings.Contains(a, "example.com") {
			t.Errorf("api_url reached the CLI's argv: %v", rec.args)
		}
	}
}

// argRecorder keeps the argv of the last call.
type argRecorder struct {
	inner commander
	args  []string
}

func (r *argRecorder) Run(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
	r.args = append([]string(nil), args...)
	return r.inner.Run(ctx, name, args)
}

// R9: under one run state, a logged-out domain costs one export and one
// probe, however many folders and providers read from it.
func TestRunStateSkipsAfterLoggedOut(t *testing.T) {
	t.Setenv(tokenEnvVar, "")
	t.Setenv(apiURLEnvOverride, "")
	ctx := vault.WithRunState(context.Background())
	c := &scriptCommander{exportStderr: stderrNoValidSession, exportCode: 1, probeStdout: probeNoSessions}
	team := openScript(t, c, nil)
	other := openScript(t, c, vault.ProviderConfig{"project": "proj-2"})

	var errs []error
	for _, path := range []string{"a", "b", "c"} {
		_, _, err := team.Resolve(ctx, vault.Ref{Path: path, Key: "K"})
		errs = append(errs, err)
	}
	_, _, err := other.Resolve(ctx, vault.Ref{Key: "K"})
	errs = append(errs, err)

	if c.exports.Load() != 1 || c.probes.Load() != 1 {
		t.Errorf("exports=%d probes=%d, want 1 and 1", c.exports.Load(), c.probes.Load())
	}
	for i, err := range errs {
		var ce *vault.ClassifiedError
		if !errors.As(err, &ce) || ce.Class.Class != vault.ClassUnauthenticated || ce.Class.Reason != vault.ReasonLoggedOut {
			t.Errorf("call %d: err = %v, want unauthenticated / logged out", i, err)
		}
		if !errors.Is(err, vault.ErrProviderUnreachable) {
			t.Errorf("call %d does not match ErrProviderUnreachable: %v", i, err)
		}
		if i > 0 {
			want := "infisical: export for https://app.infisical.com skipped: an earlier call in this run was logged out or expired"
			if err.Error() != want {
				t.Errorf("call %d text = %q, want %q", i, err.Error(), want)
			}
		}
	}

	// A minted principal on the same domain is not covered by a
	// CLI-session lapse.
	minted := openScript(t, &scriptCommander{exportStdout: `{"K":"v"}`}, vault.ProviderConfig{"token": "minted-jwt"})
	if _, _, err := minted.Resolve(ctx, vault.Ref{Key: "K"}); err != nil {
		t.Errorf("minted provider skipped after a CLI-session lapse: %v", err)
	}
}

// R9: after an export times out, a credential-sync lookup, further
// folders and a minted provider on the same domain all skip their
// exports with reason "timed out", while niwa's own universal-auth
// login request is still made and records nothing.
func TestRunStateSkipsAfterTimeout(t *testing.T) {
	quietOverride(t, "100ms")
	t.Setenv(tokenEnvVar, "")
	var logins atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		logins.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accessToken":"minted-jwt"}`))
	}))
	defer srv.Close()
	t.Setenv(apiURLEnvOverride, srv.URL)

	ctx := vault.WithRunState(context.Background())
	c := &scriptCommander{exportHang: true, probeStdout: probeVerified}
	team := openScript(t, c, nil)
	personal := openScript(t, c, vault.ProviderConfig{"project": "personal"})

	_, _, first := team.Resolve(ctx, vault.Ref{Key: "K"})
	var ce *vault.ClassifiedError
	if !errors.As(first, &ce) || ce.Class.Class != vault.ClassUnreachable || ce.Class.Reason != vault.ReasonTimedOut {
		t.Fatalf("first call: err = %v, want unreachable / timed out", first)
	}

	// The credential-sync lookup reads through the personal provider.
	_, _, credSync := personal.Resolve(ctx, vault.Ref{Path: "/niwa/provider-auth/infisical", Key: "p-uuid"})
	_, _, f2 := team.Resolve(ctx, vault.Ref{Path: "two", Key: "K"})
	_, _, f3 := team.Resolve(ctx, vault.Ref{Path: "three", Key: "K"})

	jwt, err := Authenticate(ctx, map[string]any{"client_id": "cid", "client_secret": "csec"})
	if err != nil || jwt != "minted-jwt" {
		t.Fatalf("universal-auth login = %q, %v; want it made and successful", jwt, err)
	}
	mintedCmd := &scriptCommander{exportStdout: `{"K":"v"}`}
	minted := openScript(t, mintedCmd, vault.ProviderConfig{"token": jwt})
	_, _, fm := minted.Resolve(ctx, vault.Ref{Key: "K"})

	domain := vault.NormalizeIdentity(vault.Identity{APIDomain: srv.URL}).APIDomain
	for name, err := range map[string]error{"credential sync": credSync, "folder two": f2, "folder three": f3, "minted": fm} {
		if !errors.As(err, &ce) || ce.Class.Class != vault.ClassUnreachable || ce.Class.Reason != vault.ReasonTimedOut {
			t.Errorf("%s: err = %v, want the skipped unreachable / timed out error", name, err)
			continue
		}
		want := "infisical: export for " + domain + " skipped: an earlier call in this run timed out"
		if err.Error() != want {
			t.Errorf("%s: text = %q, want %q", name, err.Error(), want)
		}
	}
	if c.exports.Load() != 1 || c.probes.Load() > 1 || mintedCmd.exports.Load() != 0 {
		t.Errorf("exports=%d probes=%d minted exports=%d, want 1, at most 1, 0",
			c.exports.Load(), c.probes.Load(), mintedCmd.exports.Load())
	}
	if logins.Load() != 1 {
		t.Errorf("universal-auth logins = %d, want 1", logins.Load())
	}
}

// niwa's universal-auth login never records a verdict, even when it
// fails, and answered failures are never recorded either.
func TestRunStateRecordsOnlyLapsesAndOutages(t *testing.T) {
	t.Setenv(tokenEnvVar, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv(apiURLEnvOverride, srv.URL)
	domain := vault.NormalizeIdentity(vault.Identity{APIDomain: srv.URL}).APIDomain

	ctx := vault.WithRunState(context.Background())
	state := vault.RunStateFrom(ctx)
	if _, err := Authenticate(ctx, map[string]any{"client_id": "cid", "client_secret": "csec"}); err == nil {
		t.Fatal("login against a 500 succeeded")
	}
	if _, ok := state.Check(domain, false); ok {
		t.Error("a failed universal-auth login recorded a run-state verdict")
	}

	for _, stderr := range []string{stderrResponse404, stderrResponse500, stderrResponse403} {
		c := &scriptCommander{exportStderr: stderr, exportCode: 1, probeStdout: probeVerified}
		p := openScript(t, c, nil)
		_, _, _ = p.Resolve(ctx, vault.Ref{Key: "K"})
		_, _, _ = p.Resolve(ctx, vault.Ref{Key: "K"})
		if _, ok := state.Check(domain, false); ok {
			t.Errorf("an answered failure (%q) recorded a verdict", strings.SplitN(stderr, "\n", 3)[1])
		}
		if c.exports.Load() != 2 {
			t.Errorf("answered failures ran %d exports for 2 calls, want 2 (never skipped)", c.exports.Load())
		}
	}
}

// A failure the caller's own cancellation caused says nothing about the
// vault: it is not classified, runs no probe and records no verdict.
func TestCallerCancellationIsNotClassified(t *testing.T) {
	t.Setenv(tokenEnvVar, "")
	ctx, cancel := context.WithCancel(vault.WithRunState(context.Background()))
	c := &cancellingCommander{cancel: cancel}
	p := openScript(t, c, nil)
	_, _, err := p.Resolve(ctx, vault.Ref{Key: "K"})
	if err == nil {
		t.Fatal("cancelled export returned no error")
	}
	var fc *vault.FailureClass
	if errors.As(err, &fc) {
		t.Errorf("a cancelled export was classified %+v", *fc)
	}
	if c.probes != 0 {
		t.Errorf("probes = %d, want 0", c.probes)
	}
	if _, ok := vault.RunStateFrom(ctx).Check(p.apiDomain, false); ok {
		t.Error("a cancelled export recorded a run-state verdict")
	}
}

// cancellingCommander cancels the caller's context during the export,
// which then comes back killed; with duringProbe, the export fails
// normally and the cancel lands during the probe instead.
type cancellingCommander struct {
	cancel      context.CancelFunc
	duringProbe bool
	probes      int
}

func (c *cancellingCommander) Run(_ context.Context, _ string, args []string) ([]byte, []byte, int, error) {
	if args[0] == "login" {
		c.probes++
		if c.duringProbe {
			c.cancel()
		}
		return []byte(probeNoSessions), nil, 0, nil
	}
	if c.duringProbe {
		return nil, []byte(stderrNoValidSession), 1, nil
	}
	c.cancel()
	return nil, nil, -1, nil
}

// A cancel that lands while the probe runs leaves the export's own error
// unclassified too, with its text unchanged and nothing recorded.
func TestCallerCancellationDuringProbeIsNotClassified(t *testing.T) {
	t.Setenv(tokenEnvVar, "")
	ctx, cancel := context.WithCancel(vault.WithRunState(context.Background()))
	c := &cancellingCommander{cancel: cancel, duringProbe: true}
	p := openScript(t, c, nil)
	_, _, err := p.Resolve(ctx, vault.Ref{Key: "K"})
	var fc *vault.FailureClass
	if err == nil || errors.As(err, &fc) {
		t.Fatalf("err = %v, want the export's unclassified error", err)
	}
	if want := "infisical: export exited 1: " + strings.TrimSpace(stderrNoValidSession); err.Error() != want {
		t.Errorf("text = %q, want %q", err.Error(), want)
	}
	if _, ok := vault.RunStateFrom(ctx).Check(p.apiDomain, false); ok {
		t.Error("a cancelled classification recorded a run-state verdict")
	}
}

// Without a run state (status checks, onboarding) nothing is skipped.
func TestNoRunStateNeverSkips(t *testing.T) {
	t.Setenv(tokenEnvVar, "")
	c := &scriptCommander{exportStderr: stderrNoValidSession, exportCode: 1, probeStdout: probeNoSessions}
	p := openScript(t, c, nil)
	for i := 0; i < 3; i++ {
		_, _, err := p.Resolve(context.Background(), vault.Ref{Key: "K"})
		if strings.Contains(err.Error(), "skipped") {
			t.Fatalf("call %d skipped with no run state: %v", i, err)
		}
	}
	if c.exports.Load() != 3 {
		t.Errorf("exports = %d, want 3", c.exports.Load())
	}
}
