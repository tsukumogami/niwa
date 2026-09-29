package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/fallbacknotice"
	"github.com/tsukumogami/niwa/internal/keyreport"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/store"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// These tests cover how each provisioning surface delivers the store
// fallback's notices: stderr for the terminal commands, the hook's
// additionalContext payload on its success and strict-refusal paths, and
// the hook's stderr on its other failure returns.

const (
	servedMarker = "may be stale"
	servedLogin  = "Run `infisical login` to refresh them"
	missMarker   = "no previously resolved value exists to fall back on"
	keyReportTop = "declared key"
)

var noticeID = fallbacknotice.Identity{
	Kind:        "infisical",
	APIDomain:   "https://app.infisical.com",
	ProjectID:   "proj-123",
	Environment: "prod",
	FolderPath:  "/backend",
}

// servedNotices returns a collector holding one served identity whose
// oldest value is five days old.
func servedNotices() *fallbacknotice.Collector {
	now := time.Now()
	c := fallbacknotice.New(func() time.Time { return now })
	c.Served(noticeID, fallbacknotice.ReasonLoggedOut, now.Add(-5*24*time.Hour))
	return c
}

// missingKey is one key-report entry, so tests can check the notices land
// after the report.
var missingKey = keyreport.Entry{
	Scope: "env.secrets",
	Key:   "OTHER_KEY",
	Cause: config.CauseProviderUnreachable,
	Level: config.LevelRequired,
}

// assertAfterKeyReport requires got to hold the key report and then marker.
func assertAfterKeyReport(t *testing.T, got, marker string) {
	t.Helper()
	k := strings.Index(got, keyReportTop)
	m := strings.Index(got, marker)
	if k < 0 || m < 0 || m < k {
		t.Errorf("want the key report followed by %q, got:\n%s", marker, got)
	}
}

func TestWireKeyReportRendersNoticesAfterTheKeyReport(t *testing.T) {
	var buf bytes.Buffer
	applier := &workspace.Applier{}
	render := wireKeyReport(applier, &buf)
	if applier.Notices == nil {
		t.Fatal("wireKeyReport did not attach a notice collector")
	}
	applier.Keys.Add(missingKey)
	applier.Notices.Served(noticeID, fallbacknotice.ReasonLoggedOut, time.Now().Add(-5*24*time.Hour))
	render()
	got := buf.String()
	assertAfterKeyReport(t, got, servedMarker)
	for _, w := range []string{servedLogin, "5 days", "proj-123", "logged out or expired"} {
		if !strings.Contains(got, w) {
			t.Errorf("stderr lacks %q:\n%s", w, got)
		}
	}
}

// create, apply, init and reset all render through wireKeyReport with
// the command's stderr writer; this scan keeps any of them from building
// its applier without it. The runs below exercise apply and create end to
// end, and the wireKeyReport test above covers the rendering all four
// share.
func TestProvisioningCommandsRenderNoticesThroughWireKeyReport(t *testing.T) {
	for _, file := range []string{"create.go", "apply.go", "init.go", "reset.go"} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}
		found := false
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "wireKeyReport" || len(call.Args) != 2 {
				return true
			}
			w, ok := call.Args[1].(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := w.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ErrOrStderr" {
				found = true
			}
			return true
		})
		if !found {
			t.Errorf("%s does not call wireKeyReport(applier, cmd.ErrOrStderr())", file)
		}
	}
}

// fallbackIdentity is the store identity the fake provider in
// fallbackWorkspaceTOML resolves under.
func fallbackIdentity() vault.Identity {
	return vault.Identity{Kind: "fake", APIDomain: "https://vault.example", ProjectID: "cli-proj", Environment: "dev", FolderPath: "/"}
}

// fallbackWorkspaceTOML declares a fake provider that fails
// unauthenticated for every key, and one secret through it.
func fallbackWorkspaceTOML(required bool) string {
	s := `[workspace]
name = "fb-cli"

[vault.provider]
kind = "fake"
fail_class = "unauthenticated"

[vault.provider.identity]
api_domain = "https://vault.example"
project_id = "cli-proj"
environment = "dev"
folder_path = "/"

[env.secrets]
API_TOKEN = "vault://API_TOKEN"
`
	if required {
		s += "\n[env.secrets.required]\nAPI_TOKEN = \"token the tools need\"\n"
	}
	return s
}

// isolateProvisioning points every location a provisioning run touches at
// temp directories, registers the fake backend, and returns the store
// directory.
func isolateProvisioning(t *testing.T) string {
	t.Helper()
	home := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "xdg-state"))
	t.Setenv("GITHUB_TOKEN", "test-token-not-used")
	t.Setenv("CODEX_HOME", "")
	withFakeVaultBackendForCLI(t)
	dir, err := store.Dir()
	if err != nil {
		t.Fatalf("store.Dir: %v", err)
	}
	return dir
}

func seedStore(t *testing.T, dir string, resolvedAt time.Time) {
	t.Helper()
	err := store.Update(dir, fallbackIdentity(), map[string]store.Entry{
		"API_TOKEN": {Value: []byte("stored-token-value-1"), ResolvedAt: resolvedAt, VersionToken: "v1"},
	}, nil, false)
	if err != nil {
		t.Fatalf("seeding the store: %v", err)
	}
}

// fallbackWorkspace writes a workspace root with the fallback config and
// returns it.
func fallbackWorkspace(t *testing.T, required bool) string {
	t.Helper()
	root := canonicalTempDir(t)
	if err := os.MkdirAll(filepath.Join(root, config.ConfigDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, config.ConfigDir, config.ConfigFile), []byte(fallbackWorkspaceTOML(required)), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// setStrict turns on strict_secrets in the workspace written by
// fallbackWorkspace, the setting a run with no flag reads.
func setStrict(t *testing.T, root string) {
	t.Helper()
	cfgPath := filepath.Join(root, config.ConfigDir, config.ConfigFile)
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(data), "name = \"fb-cli\"\n", "name = \"fb-cli\"\nstrict_secrets = true\n", 1)
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCreateRendersServedWarning(t *testing.T) {
	dir := isolateProvisioning(t)
	seedStore(t, dir, time.Now().Add(-3*24*time.Hour))
	root := fallbackWorkspace(t, false)
	chdir(t, root)

	var stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&stderr)
	cmd.SetContext(context.Background())
	if err := runCreate(cmd, nil); err != nil {
		t.Fatalf("runCreate: %v\nstderr:\n%s", err, stderr.String())
	}
	got := stderr.String()
	for _, w := range []string{servedMarker, servedLogin, "fake project cli-proj", "3 days", "logged out or expired"} {
		if !strings.Contains(got, w) {
			t.Errorf("create stderr lacks %q:\n%s", w, got)
		}
	}
	if n := strings.Count(got, servedMarker); n != 1 {
		t.Errorf("create printed %d served warnings, want 1", n)
	}
}

func TestApplyRendersServedWarning(t *testing.T) {
	dir := isolateProvisioning(t)
	seedStore(t, dir, time.Now().Add(-3*24*time.Hour))
	root := fallbackWorkspace(t, false)
	instanceDir := filepath.Join(root, "inst-1")
	if err := workspace.SaveState(instanceDir, &workspace.InstanceState{
		SchemaVersion: workspace.SchemaVersion,
		InstanceName:  "inst-1",
		Root:          instanceDir,
	}); err != nil {
		t.Fatal(err)
	}
	chdir(t, instanceDir)
	saved := applyNoCascade
	t.Cleanup(func() { applyNoCascade = saved })
	applyNoCascade = false

	var stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&stderr)
	cmd.SetContext(context.Background())
	if err := runApply(cmd, nil); err != nil {
		t.Fatalf("runApply: %v\nstderr:\n%s", err, stderr.String())
	}
	got := stderr.String()
	for _, w := range []string{servedMarker, servedLogin, "fake project cli-proj", "3 days"} {
		if !strings.Contains(got, w) {
			t.Errorf("apply stderr lacks %q:\n%s", w, got)
		}
	}
}

// R18: notices never count as a shortfall. Every key is supplied from the
// store, so a strict run succeeds and prints the warning.
func TestStrictRunWithServedKeysIsNotRefused(t *testing.T) {
	dir := isolateProvisioning(t)
	seedStore(t, dir, time.Now().Add(-time.Hour))
	root := fallbackWorkspace(t, true)
	setStrict(t, root)
	chdir(t, root)

	var stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&stderr)
	cmd.SetContext(context.Background())
	if err := runCreate(cmd, nil); err != nil {
		t.Fatalf("strict create refused a run whose keys were all served: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), servedMarker) {
		t.Errorf("strict create did not print the served warning:\n%s", stderr.String())
	}
}

func TestDispatchRendersNoticesAfterKeyReport(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed=%v", fail), func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			f := installDispatchFakes(t, root)
			inner := provisionInstanceFunc
			provisionInstanceFunc = func(ctx context.Context, r, cwd, namePrefix, sep string, n int) (provisionResult, error) {
				if fail {
					return provisionResult{Keys: []keyreport.Entry{missingKey}, Notices: servedNotices()}, errors.New("create failed")
				}
				res, err := inner(ctx, r, cwd, namePrefix, sep, n)
				res.Keys = []keyreport.Entry{missingKey}
				res.Notices = servedNotices()
				return res, err
			}
			_, stderr, err := runDispatchCmd(t, "do a thing")
			if fail != (err != nil) {
				t.Fatalf("err = %v, want failure %v", err, fail)
			}
			assertAfterKeyReport(t, stderr, servedMarker)
			if !strings.Contains(stderr, servedLogin) {
				t.Errorf("stderr lacks %q:\n%s", servedLogin, stderr)
			}
			if !fail && f.launchCalled != 1 {
				t.Errorf("launch called %d times", f.launchCalled)
			}
		})
	}
}

func TestWatchRendersNoticesAfterKeyReport(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed=%v", fail), func(t *testing.T) {
			root, home := setupWatchCharEnv(t)
			installWatchCharFakes(t)
			stubAskPostureSeams(t, home, nil)
			inner := provisionInstanceFunc
			provisionInstanceFunc = func(ctx context.Context, r, cwd, namePrefix, sep string, n int) (provisionResult, error) {
				if fail {
					return provisionResult{Keys: []keyreport.Entry{missingKey}, Notices: servedNotices()}, errors.New("create failed")
				}
				res, err := inner(ctx, r, cwd, namePrefix, sep, n)
				res.Keys = []keyreport.Entry{missingKey}
				res.Notices = servedNotices()
				return res, err
			}
			client := newPullHeadServer(t)
			cmd, _, stderr := newWatchCharCmd()
			err := stageReview(cmd, root, root, "", client, watchCharPR, reviewPlan{})
			if fail != (err != nil) {
				t.Fatalf("err = %v, want failure %v", err, fail)
			}
			assertAfterKeyReport(t, stderr.String(), servedMarker)
		})
	}
}

// R17 through the real provisioner: an empty store, a logged-out provider.
// A required key in a strict workspace fails the dispatch through Create's
// failure path and the nothing-to-fall-back-on line is on stderr; an
// optional key with strict mode off lets it succeed and still prints the
// line. (Without strict mode a required key behind an unreachable provider
// is reported, not fatal, as it was before the fallback.)
func TestDispatchNothingToFallBackOn(t *testing.T) {
	for _, required := range []bool{true, false} {
		t.Run(fmt.Sprintf("required=%v", required), func(t *testing.T) {
			isolateProvisioning(t)
			root := fallbackWorkspace(t, required)
			if required {
				setStrict(t, root)
			}
			chdir(t, root)
			installDispatchFakes(t, root)
			provisionInstanceFunc = realProvisionInstance

			_, stderr, err := runDispatchCmd(t, "do a thing")
			if required && err == nil {
				t.Fatal("dispatch with a required, unstored key succeeded")
			}
			if !required && err != nil {
				t.Fatalf("dispatch with an optional, unstored key failed: %v\n%s", err, stderr)
			}
			for _, w := range []string{missMarker, "fake project cli-proj (env dev, path /, https://vault.example)", "Run `infisical login`."} {
				if !strings.Contains(stderr, w) {
					t.Errorf("stderr lacks %q:\n%s", w, stderr)
				}
			}
			if strings.Count(stderr, missMarker) != 1 {
				t.Errorf("want exactly one nothing-to-fall-back-on line:\n%s", stderr)
			}
		})
	}
}

// hookNotices records one of each R21 condition.
func hookNotices() *fallbacknotice.Collector {
	c := servedNotices()
	// Every field differs from the served identity, so a field found in
	// one sentence can't stand in for the other's.
	miss := fallbacknotice.Identity{
		Kind:        "infisical",
		APIDomain:   "https://eu.infisical.com",
		ProjectID:   "proj-miss",
		Environment: "staging",
		FolderPath:  "/other",
	}
	c.NothingToFallBackOn(miss)
	c.StoreInWorkTree("/state/secret-cache")
	c.StoreUnwritable("/state/secret-cache")
	return c
}

// hookNoticeMarkers checks each notice's fields inside its own sentence:
// the served warning's project, env, path, domain, reason, age and login
// instruction as one span, and the miss line's identity joined to its
// wording.
var hookNoticeMarkers = []string{
	"using stored values that may be stale for infisical project proj-123 (env prod, path /backend, https://app.infisical.com): " +
		"the provider is logged out or expired; the oldest value is 5 days old. " + servedLogin + ".",
	"infisical project proj-miss (env staging, path /other, https://eu.infisical.com) could not be used and " + missMarker,
	"/state/secret-cache is inside a git work tree",
	"/state/secret-cache could not be read or written",
	"Ask the operator to run `infisical login`",
}

func decodeContext(t *testing.T, out string) string {
	t.Helper()
	var inj sessionStartInjection
	if err := json.Unmarshal([]byte(out), &inj); err != nil {
		t.Fatalf("unmarshal payload %q: %v", out, err)
	}
	return inj.HookSpecificOutput.AdditionalContext
}

// R21 on the hook's success path: every notice is in the payload and none
// is on stderr.
func TestSessionStartNoticesGoIntoThePayload(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	writeJobState(t, jobsDir, testSessionID[:8], testSessionID, "bg")
	stubProvision(t, "# guidance\n")
	inner := provisionInstanceFunc
	provisionInstanceFunc = func(ctx context.Context, r, cwd, p, sep string, n int) (provisionResult, error) {
		res, err := inner(ctx, r, cwd, p, sep, n)
		res.Notices = hookNotices()
		return res, err
	}
	out, stderr, err := runStart(t, instanceHookPayload{HookEventName: hookEventSessionStart, SessionID: testSessionID, Cwd: root}, jobsDir)
	if err != nil {
		t.Fatalf("runInstanceHookStart: %v", err)
	}
	ctx := decodeContext(t, out)
	for _, w := range hookNoticeMarkers {
		if !strings.Contains(ctx, w) {
			t.Errorf("additionalContext lacks %q:\n%s", w, ctx)
		}
	}
	if stderr != "" {
		t.Errorf("notices leaked to stderr on the payload path:\n%s", stderr)
	}
}

func TestSessionStartStrictRefusalCarriesNotices(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	writeJobState(t, jobsDir, testSessionID[:8], testSessionID, "bg")
	prev := provisionInstanceFunc
	provisionInstanceFunc = func(context.Context, string, string, string, string, int) (provisionResult, error) {
		return provisionResult{Keys: []keyreport.Entry{missingKey}, Notices: hookNotices()},
			fmt.Errorf("%w: strict mode is enabled", workspace.ErrStrictSecrets)
	}
	t.Cleanup(func() { provisionInstanceFunc = prev })
	out, stderr, err := runStart(t, instanceHookPayload{HookEventName: hookEventSessionStart, SessionID: testSessionID, Cwd: root}, jobsDir)
	if err != nil {
		t.Fatalf("strict refusal must exit 0: %v", err)
	}
	ctx := decodeContext(t, out)
	for _, w := range hookNoticeMarkers {
		if !strings.Contains(ctx, w) {
			t.Errorf("strict payload lacks %q:\n%s", w, ctx)
		}
	}
	if stderr != "" {
		t.Errorf("notices leaked to stderr on the strict path:\n%s", stderr)
	}
}

// A provisioning failure that is not a strict refusal writes no payload;
// the notices go to stderr.
func TestSessionStartProvisionFailurePrintsNoticesToStderr(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	writeJobState(t, jobsDir, testSessionID[:8], testSessionID, "bg")
	prev := provisionInstanceFunc
	provisionInstanceFunc = func(context.Context, string, string, string, string, int) (provisionResult, error) {
		c := fallbacknotice.New(nil)
		c.NothingToFallBackOn(noticeID)
		return provisionResult{Keys: []keyreport.Entry{missingKey}, Notices: c}, errors.New("required key API_TOKEN has no value")
	}
	t.Cleanup(func() { provisionInstanceFunc = prev })
	out, stderr, err := runStart(t, instanceHookPayload{HookEventName: hookEventSessionStart, SessionID: testSessionID, Cwd: root}, jobsDir)
	if err == nil {
		t.Fatal("a non-strict provisioning failure must still fail")
	}
	if out != "" {
		t.Errorf("a failing hook wrote a payload: %q", out)
	}
	if !strings.Contains(stderr, "warning: infisical project proj-123 (env prod, path /backend, https://app.infisical.com) could not be used and "+missMarker) {
		t.Errorf("stderr lacks the nothing-to-fall-back-on line:\n%s", stderr)
	}
}

// The mapping write is the other failure return reachable from a test: a
// file where the sessions directory belongs makes it fail after a successful
// provision.
func TestSessionStartMappingFailurePrintsNoticesToStderr(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	writeJobState(t, jobsDir, testSessionID[:8], testSessionID, "bg")
	stubProvision(t, "")
	inner := provisionInstanceFunc
	provisionInstanceFunc = func(ctx context.Context, r, cwd, p, sep string, n int) (provisionResult, error) {
		res, err := inner(ctx, r, cwd, p, sep, n)
		res.Notices = servedNotices()
		// Block the mapping write: its directory path is taken by a file.
		sessions := filepath.Join(r, config.ConfigDir, "sessions")
		_ = os.RemoveAll(sessions)
		if wErr := os.WriteFile(sessions, nil, 0o644); wErr != nil {
			t.Fatal(wErr)
		}
		return res, err
	}
	out, stderr, err := runStart(t, instanceHookPayload{HookEventName: hookEventSessionStart, SessionID: testSessionID, Cwd: root}, jobsDir)
	if err == nil {
		t.Fatal("the mapping write did not fail with its directory blocked by a file")
	}
	if out != "" {
		t.Errorf("a failing hook wrote a payload: %q", out)
	}
	if !strings.Contains(stderr, servedMarker) {
		t.Errorf("stderr lacks the served warning:\n%s", stderr)
	}
}

// A run with no fallback changes nothing: the payload builders produce
// the bytes they produce without a collector.
func TestEmptyNoticesLeavePayloadsUnchanged(t *testing.T) {
	dir := t.TempDir()
	empty := fallbacknotice.New(nil)
	keys := []keyreport.Entry{missingKey}
	a, err := buildSessionStartInjection(dir, keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildSessionStartInjection(dir, keys, empty)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("an empty collector changed the session-start payload:\n%s\n%s", a, b)
	}
	a, _ = buildStrictFailureInjection(keys, nil)
	b, _ = buildStrictFailureInjection(keys, empty)
	if !bytes.Equal(a, b) {
		t.Errorf("an empty collector changed the strict payload:\n%s\n%s", a, b)
	}
	var buf bytes.Buffer
	renderNotices(&buf, empty)
	renderNotices(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("an empty collector rendered %q", buf.String())
	}
}
