package resolve_test

// Characterization of how the resolver handles every Infisical failure shape
// before the vault-offline work changes any of it. Each scenario drives the
// real Infisical provider through ResolveWorkspace or ResolveGlobalOverride
// with a stub commander standing in for the `infisical` binary, and pins the
// resulting error text and key report as a fixture under testdata/golden.
//
// The fixtures are the baseline later behaviour is compared against: a
// scenario whose outcome is meant to change gets a new expectation in the
// change that moves it, and every other fixture must keep matching. Rewrite
// them only on purpose, with
//
//	go test ./internal/vault/resolve/ -run Golden -update

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/keyreport"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/infisical"
	"github.com/tsukumogami/niwa/internal/vault/resolve"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden fixtures under testdata/golden from the current code")

// goldenKey is the key every failure scenario asks for.
const goldenKey = "GOLDEN_KEY"

// Secret markers are the values a stub export hands back. None of them may
// ever reach a fixture: a fixture is error text and a key report, and both
// must stay free of resolved values.
const (
	markerGolden   = "golden-secret-marker-golden-key-value"
	markerOther    = "golden-secret-marker-other-key-value"
	markerFolder   = "golden-secret-marker-folder-key-value"
	markerPersonal = "golden-secret-marker-personal-key-value"
)

var goldenMarkers = []string{markerGolden, markerOther, markerFolder, markerPersonal}

// Stub stderr texts. The logged-out wordings are the ones the pinned CLI
// prints when its stored session is missing or expired; the server responses
// use the CLI's own "Response Code: <n>" line.
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

// goldenCommander stands in for the `infisical` binary. It answers every
// export with the configured outcome and counts invocations per subcommand,
// so a test can say how many subprocesses a run would have started.
type goldenCommander struct {
	stdout   string
	stderr   string
	exitCode int
	startErr error
	// probe, when set, is the stdout of `infisical login status --json`
	// (exit 0). Unset, the probe fails like any unexpected invocation,
	// which the classifier reads as no usable answer.
	probe string

	mu    sync.Mutex
	calls [][]string
}

func (c *goldenCommander) Run(_ context.Context, _ string, args []string) ([]byte, []byte, int, error) {
	c.mu.Lock()
	c.calls = append(c.calls, append([]string(nil), args...))
	c.mu.Unlock()
	if c.startErr != nil {
		return nil, nil, -1, c.startErr
	}
	if c.probe != "" && len(args) >= 2 && args[0] == "login" && args[1] == "status" {
		return []byte(c.probe), nil, 0, nil
	}
	if len(args) == 0 || args[0] != "export" {
		return nil, []byte("golden stub: unexpected invocation\n"), 1, nil
	}
	return []byte(c.stdout), []byte(c.stderr), c.exitCode, nil
}

// Probe answers. The logged-out answer lists no session; the verified
// one vouches for the session the export ran as.
const (
	probeNoSession = `{"sessions":[]}`
	probeVerified  = `{"sessions":[{"status":"authenticated","tokenSource":"keyring","verification":{"state":"verified"}}]}`
)

// count reports how many invocations started with the given subcommand.
func (c *goldenCommander) count(sub string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, a := range c.calls {
		if len(a) > 0 && a[0] == sub {
			n++
		}
	}
	return n
}

// newInfisicalRegistry registers the real Infisical factory on a fresh
// registry, leaving vault.DefaultRegistry untouched.
func newInfisicalRegistry(t *testing.T) *vault.Registry {
	t.Helper()
	reg := vault.NewRegistry()
	if err := reg.Register(infisical.NewFactory()); err != nil {
		t.Fatalf("register infisical factory: %v", err)
	}
	return reg
}

// infisicalRegistry is the provider block every scenario declares, with the
// stub injected through the provider's test-only commander hook.
func infisicalRegistry(project string, c *goldenCommander) *config.VaultRegistry {
	return &config.VaultRegistry{
		Provider: &config.VaultProviderConfig{
			Kind: "infisical",
			Config: map[string]any{
				"project":    project,
				"_commander": c,
			},
		},
	}
}

// secretsTable declares refs in an env.secrets table, with any keys listed in
// required also declared in its required sub-table.
func secretsTable(refs map[string]string, required ...string) config.EnvVarsTable {
	t := config.EnvVarsTable{Values: map[string]config.MaybeSecret{}}
	for k, ref := range refs {
		t.Values[k] = config.MaybeSecret{Plain: ref}
	}
	if len(required) > 0 {
		t.Required = map[string]string{}
		for _, k := range required {
			t.Required[k] = "golden required key"
		}
	}
	return t
}

// goldenScenario is one stub outcome recorded against today's code.
type goldenScenario struct {
	name     string
	stdout   string
	stderr   string
	exitCode int
	startErr error
	// personal resolves the key through the personal global override layer
	// rather than the team workspace config.
	personal bool
	// required declares the key in the required sub-table.
	required bool
	// probe is the session probe's answer (see goldenCommander.probe).
	probe string
	// reclassified marks a scenario whose outcome failure
	// classification changed on purpose: its fixture still records the
	// hard error it produced before, and the test asserts the new
	// outcome (a tolerated mark carrying want) instead of matching it.
	// Those fixtures are snapshots of the pre-classification behaviour,
	// not of what the code does now; -update leaves them alone on
	// purpose, since the test returns before checkGolden.
	reclassified bool
	want         vault.FailureClass
}

func goldenScenarios() []goldenScenario {
	// The start error the provider sees when the binary is not on PATH.
	notInstalled := &exec.Error{Name: "infisical", Err: exec.ErrNotFound}
	loggedOut := vault.FailureClass{Class: vault.ClassUnauthenticated, Reason: vault.ReasonLoggedOut}
	unreachable := vault.FailureClass{Class: vault.ClassUnreachable, Reason: vault.ReasonUnreachable}
	return []goldenScenario{
		{name: "logged-out-no-valid-session", stderr: stderrNoValidSession, exitCode: 1,
			probe: probeNoSession, reclassified: true, want: loggedOut},
		{name: "logged-out-could-not-find-login", stderr: stderrCouldNotFindLogin, exitCode: 1,
			probe: probeNoSession, reclassified: true, want: loggedOut},
		{name: "logged-out-session-expired", stderr: stderrSessionExpired, exitCode: 1,
			probe: probeNoSession, reclassified: true, want: loggedOut},
		// A 401 or 403 stays the answered, tolerated mark it always was
		// only while the probe vouches for the session, so these
		// scenarios answer it with a verified one.
		{name: "response-401", stderr: stderrResponse401, exitCode: 1, probe: probeVerified},
		{name: "response-403", stderr: stderrResponse403, exitCode: 1, probe: probeVerified},
		{name: "response-404", stderr: stderrResponse404, exitCode: 1},
		{name: "response-500", stderr: stderrResponse500, exitCode: 1},
		{name: "logged-out-and-response-404", stderr: stderrLoggedOutAnd404, exitCode: 1},
		// With no server response and a session the probe vouches for,
		// the only explanation left is the network.
		{name: "connection-refused", stderr: stderrConnRefused, exitCode: 1,
			probe: probeVerified, reclassified: true, want: unreachable},
		{name: "client-not-installed", startErr: notInstalled},
		{name: "missing-key", stdout: fmt.Sprintf(`{"OTHER_KEY":%q}`, markerOther)},
		{name: "required-key-response-403", stderr: stderrResponse403, exitCode: 1, required: true, probe: probeVerified},
		{name: "personal-response-403", stderr: stderrResponse403, exitCode: 1, personal: true, probe: probeVerified},
		{name: "personal-logged-out-no-valid-session", stderr: stderrNoValidSession, exitCode: 1, personal: true,
			probe: probeNoSession, reclassified: true, want: loggedOut},
	}
}

// TestGoldenVaultFailureHandling pins the error text and key report for
// every stub scenario: a scenario whose outcome never changed must match
// its fixture, and a reclassified one must show its new outcome (see
// checkReclassified).
func TestGoldenVaultFailureHandling(t *testing.T) {
	// The deciding probe session depends on INFISICAL_TOKEN; keep the
	// host's value out of it.
	t.Setenv("INFISICAL_TOKEN", "")
	for _, sc := range goldenScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			c := &goldenCommander{stdout: sc.stdout, stderr: sc.stderr, exitCode: sc.exitCode, startErr: sc.startErr, probe: sc.probe}
			var required []string
			if sc.required {
				required = []string{goldenKey}
			}
			table := secretsTable(map[string]string{goldenKey: "vault://" + goldenKey}, required...)

			keys := keyreport.New()
			var err error
			if sc.personal {
				gco := &config.GlobalConfigOverride{Global: config.GlobalOverride{
					Vault: infisicalRegistry("golden-personal-project", c),
					Env:   config.EnvConfig{Secrets: table},
				}}
				var out *config.GlobalConfigOverride
				out, err = resolve.ResolveGlobalOverride(context.Background(), gco, resolve.ResolveOptions{
					Registry: newInfisicalRegistry(t),
				})
				if out != nil {
					addMarks(keys, "global.env.secrets", out.Global.Env.Secrets)
				}
			} else {
				cfg := &config.WorkspaceConfig{
					Workspace: config.WorkspaceMeta{Name: "golden"},
					Vault:     infisicalRegistry("golden-team-project", c),
					Env:       config.EnvConfig{Secrets: table},
				}
				var out *config.WorkspaceConfig
				out, err = resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{
					Registry: newInfisicalRegistry(t),
				})
				if out != nil {
					addMarks(keys, "env.secrets", out.Env.Secrets)
				}
			}

			if got := c.count("export"); got != 1 {
				t.Fatalf("export invocations = %d, want 1", got)
			}
			if sc.reclassified {
				checkReclassified(t, sc, err, keys)
				return
			}
			checkGolden(t, sc.name, renderOutcome(sc.name, err, keys))
		})
	}
}

// checkReclassified asserts a reclassified scenario's new outcome against
// the fixtures, without rewriting any of them:
//
//   - its own fixture still records the hard "export exited 1" error it
//     produced before classification, so the change stays pinned;
//   - the resolver now tolerates the failure as a mark, and the key report
//     reads exactly as the fixture of the 403 scenario in the same layer
//     (the tolerated mark recorded before the change);
//   - the provider's own error is a ClassifiedError with the expected class,
//     matching ErrProviderUnreachable, whose text is byte for byte the text
//     the fixture recorded behind the resolver's prefix.
func checkReclassified(t *testing.T, sc goldenScenario, err error, keys *keyreport.Collector) {
	t.Helper()
	before := readGolden(t, sc.name)
	beforeErr, beforeReport := splitOutcome(t, before)
	if !strings.Contains(beforeErr, "infisical: export exited 1: ") || beforeReport != "<empty>\n" {
		t.Fatalf("fixture %s no longer records the pre-classification hard error:\n%s", sc.name, before)
	}

	if err != nil {
		t.Fatalf("resolver error = %v, want the failure tolerated as a mark", err)
	}
	markFixture := "response-403"
	if sc.personal {
		markFixture = "personal-response-403"
	}
	_, wantReport := splitOutcome(t, readGolden(t, markFixture))
	gotReport := keyreport.RenderText(keys.Report())
	if gotReport != wantReport {
		t.Errorf("key report differs from %s's tolerated mark\n--- want\n%s--- got\n%s", markFixture, wantReport, gotReport)
	}

	c := &goldenCommander{stdout: sc.stdout, stderr: sc.stderr, exitCode: sc.exitCode, probe: sc.probe}
	p, openErr := infisical.NewFactory().Open(context.Background(), vault.ProviderConfig{"project": "golden-project", "_commander": c})
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer p.Close()
	_, _, perr := p.Resolve(context.Background(), vault.Ref{Key: goldenKey})
	var ce *vault.ClassifiedError
	if !errors.As(perr, &ce) {
		t.Fatalf("provider error = %v (%T), want a *vault.ClassifiedError", perr, perr)
	}
	if ce.Class.Class != sc.want.Class || ce.Class.Reason != sc.want.Reason {
		t.Errorf("class = %s / %s, want %s / %s", ce.Class.Class, ce.Class.Reason, sc.want.Class, sc.want.Reason)
	}
	if !errors.Is(perr, vault.ErrProviderUnreachable) {
		t.Errorf("provider error does not match ErrProviderUnreachable: %v", perr)
	}
	if !strings.HasSuffix(beforeErr, `via provider "(anonymous)": `+perr.Error()) {
		t.Errorf("provider error text changed\nfixture: %s\ngot:     %s", beforeErr, perr.Error())
	}
}

// readGolden returns a fixture's contents.
func readGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "golden", name+".golden"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// splitOutcome splits a renderOutcome body into its error line and its
// key report.
func splitOutcome(t *testing.T, body string) (errText, report string) {
	t.Helper()
	_, rest, ok := strings.Cut(body, "error:\n")
	if !ok {
		t.Fatalf("fixture has no error section:\n%s", body)
	}
	errText, report, ok = strings.Cut(rest, "\nkey report:\n")
	if !ok {
		t.Fatalf("fixture has no key report section:\n%s", body)
	}
	return errText, report
}

// TestGoldenSuccessfulRunInvocations records how many subprocesses a
// successful resolution starts today and asserts every run still starts
// exactly that many exports and no `login status`. A successful resolution
// must not pay for the failure handling added around it.
func TestGoldenSuccessfulRunInvocations(t *testing.T) {
	team := &goldenCommander{stdout: fmt.Sprintf(`{%q:%q,%q:%q}`, goldenKey, markerGolden, "FOLDER_KEY", markerFolder)}
	personal := &goldenCommander{stdout: fmt.Sprintf(`{%q:%q}`, "PERSONAL_KEY", markerPersonal)}
	reg := newInfisicalRegistry(t)

	// Two team references share the root folder and one reads a sub-folder,
	// so the team layer needs two exports; the personal layer needs one.
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "golden"},
		Vault:     infisicalRegistry("golden-team-project", team),
		Env: config.EnvConfig{Secrets: secretsTable(map[string]string{
			goldenKey:    "vault://" + goldenKey,
			"GOLDEN_DUP": "vault://" + goldenKey,
			"FOLDER_KEY": "vault://folder/FOLDER_KEY",
		})},
	}
	gco := &config.GlobalConfigOverride{Global: config.GlobalOverride{
		Vault: infisicalRegistry("golden-personal-project", personal),
		Env: config.EnvConfig{Secrets: secretsTable(map[string]string{
			"PERSONAL_KEY": "vault://PERSONAL_KEY",
		})},
	}}

	outCfg, err := resolve.ResolveWorkspace(context.Background(), cfg, resolve.ResolveOptions{Registry: reg})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	outGCO, err := resolve.ResolveGlobalOverride(context.Background(), gco, resolve.ResolveOptions{Registry: reg})
	if err != nil {
		t.Fatalf("ResolveGlobalOverride: %v", err)
	}
	for k, ms := range outCfg.Env.Secrets.Values {
		if !ms.IsSecret() {
			t.Fatalf("team key %s not resolved", k)
		}
	}
	if !outGCO.Global.Env.Secrets.Values["PERSONAL_KEY"].IsSecret() {
		t.Fatal("personal key not resolved")
	}

	exports := team.count("export") + personal.count("export")
	logins := team.count("login") + personal.count("login")
	if logins != 0 {
		t.Errorf("login invocations = %d, want 0 on a successful run", logins)
	}
	checkGolden(t, "successful-run-invocations",
		fmt.Sprintf("scenario: successful-run-invocations\nexport invocations: %d\n", exports))
}

// addMarks records every marked value in a resolved table, the way the
// post-merge walk does for a real run.
func addMarks(c *keyreport.Collector, scope string, t config.EnvVarsTable) {
	for k, ms := range t.Values {
		c.AddMark(scope, k, ms.Unresolved)
	}
}

// renderOutcome is the fixture body: the resolver's error and the key report
// a terminal would show for the marks it left.
func renderOutcome(name string, err error, keys *keyreport.Collector) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "scenario: %s\n", name)
	sb.WriteString("error:\n")
	if err == nil {
		sb.WriteString("<nil>\n")
	} else {
		sb.WriteString(err.Error())
		sb.WriteString("\n")
	}
	sb.WriteString("key report:\n")
	if r := keyreport.RenderText(keys.Report()); r != "" {
		sb.WriteString(r)
	} else {
		sb.WriteString("<empty>\n")
	}
	return sb.String()
}

var goldenTimeRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)

// normalizeGolden replaces the fragments that differ between runs: the
// system temp directory and wall-clock timestamps.
func normalizeGolden(s string) string {
	if tmp := os.TempDir(); tmp != "" && tmp != "/" {
		s = strings.ReplaceAll(s, tmp, "<TMP>")
	}
	return goldenTimeRe.ReplaceAllString(s, "<TIME>")
}

// checkGolden compares got against testdata/golden/<name>.golden, or rewrites
// the fixture under -update. Either way a fixture holding a secret marker
// fails the test.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	got = normalizeGolden(got)
	for _, m := range goldenMarkers {
		if strings.Contains(got, m) {
			t.Fatalf("fixture %s would contain the secret marker %q", name, m)
		}
	}
	path := filepath.Join("testdata", "golden", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing fixture %s; record it with -update", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("fixture %s differs from the current output\n--- want\n%s--- got\n%s", path, want, got)
	}
}
