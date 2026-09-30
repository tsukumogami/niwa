package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/fallbacknotice"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/keyreport"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/store"
)

// These tests drive the store fallback through the whole provisioning
// pipeline with the fake vault backend: a first run stores what it
// resolves, a later run whose provider fails is served from the store.
// XDG_STATE_HOME is a per-test directory, so every store lives in it.

const fbHeader = `
[workspace]
name = "fb-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`

const fbDomain = "https://vault.example"

// fbIdentity is the store identity of a fake provider rendered by
// fakeProvider for project.
func fbIdentity(project string) vault.Identity {
	return vault.Identity{Kind: "fake", APIDomain: fbDomain, ProjectID: project, Environment: "dev", FolderPath: "/"}
}

func fbNoticeIdentity(project string) fallbacknotice.Identity {
	id := fbIdentity(project)
	return fallbacknotice.Identity{Kind: id.Kind, APIDomain: id.APIDomain, ProjectID: id.ProjectID, Environment: id.Environment, FolderPath: id.FolderPath}
}

// fakeProvider renders a fake provider declared at table, with a store
// identity for project, the given values, and extra knob lines.
func fakeProvider(table, project string, values map[string]string, knobs ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n[%s]\nkind = \"fake\"\n", table)
	for _, k := range knobs {
		b.WriteString(k + "\n")
	}
	fmt.Fprintf(&b, "\n[%s.identity]\napi_domain = %q\nproject_id = %q\nenvironment = \"dev\"\nfolder_path = \"/\"\n", table, fbDomain, project)
	if len(values) > 0 {
		fmt.Fprintf(&b, "\n[%s.values]\n", table)
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s = %q\n", k, values[k])
		}
	}
	return b.String()
}

// secretsTable renders an env secrets table mapping each key to a
// reference to the same key through provider (empty for anonymous).
func secretsTable(table, provider string, keys ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n[%s]\n", table)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = \"vault://%s%s\"\n", k, provider, k)
	}
	return b.String()
}

func fakeToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// fbEnv is one workspace instance applied repeatedly.
type fbEnv struct {
	t            *testing.T
	niwaDir      string
	instanceRoot string
	storeDir     string
	globalDir    string // empty until setGlobal
	overlay      string // empty until setOverlay; set before the first run
	// unwired leaves Applier.Notices nil, as a caller that never wires
	// a collector would.
	unwired bool
}

func newFBEnv(t *testing.T, withOverlay bool) *fbEnv {
	t.Helper()
	withFakeVaultBackend(t)
	return anotherFBEnv(t, withOverlay)
}

// anotherFBEnv is newFBEnv for a test that already registered the fake
// backend. Each env has its own store.
func anotherFBEnv(t *testing.T, withOverlay bool) *fbEnv {
	t.Helper()
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	niwaDir, instanceRoot := setupTestWorkspace(t, fbHeader, nil, []struct{ group, name string }{{"all", "repo1"}})
	st := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "fb-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
		NoOverlay:      !withOverlay,
	}
	if withOverlay {
		st.OverlayURL = "testorg/fb-ws-overlay"
		st.OverlayCommit = "abc123"
	}
	if err := SaveState(instanceRoot, st); err != nil {
		t.Fatal(err)
	}
	return &fbEnv{t: t, niwaDir: niwaDir, instanceRoot: instanceRoot, storeDir: filepath.Join(stateHome, "niwa", "secret-cache")}
}

func (e *fbEnv) setTeam(body string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.niwaDir, "workspace.toml"), []byte(fbHeader+body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *fbEnv) setGlobal(body string) {
	e.t.Helper()
	if e.globalDir == "" {
		e.globalDir = e.t.TempDir()
	}
	if err := os.WriteFile(filepath.Join(e.globalDir, "niwa.toml"), []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *fbEnv) setOverlay(body string) { e.overlay = body }

// fbRun is what one apply produced.
type fbRun struct {
	err     error
	notices *fallbacknotice.Collector
	keys    *keyreport.Collector
	// output is everything the run wrote through its reporter.
	output *syncBuffer
}

func (e *fbEnv) apply(strict bool) fbRun {
	e.t.Helper()
	loaded, err := config.Load(filepath.Join(e.niwaDir, "workspace.toml"))
	if err != nil {
		e.t.Fatalf("loading config: %v", err)
	}
	applier := NewApplier(&mockGitHubClient{repos: map[string][]github.Repo{
		"testorg": {{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"}},
	}})
	applier.Cloner = &Cloner{}
	applier.GlobalConfigDir = e.globalDir
	overlay := e.overlay
	applier.cloneOrSync = func(_ context.Context, _, dir string) (bool, int, error) {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			return false, 0, err
		}
		return false, 0, os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), []byte(overlay), 0o644)
	}
	applier.headSHA = func(string) (string, error) { return "abc123", nil }
	run := fbRun{notices: fallbacknotice.New(nil), keys: keyreport.New(), output: &syncBuffer{}}
	applier.Reporter = NewReporterWithTTY(run.output, false)
	if !e.unwired {
		applier.Notices = run.notices
	}
	applier.Keys = run.keys
	applier.StrictSecrets = strict
	run.err = applier.Apply(context.Background(), loaded.Config, e.niwaDir, e.instanceRoot)
	return run
}

func (e *fbEnv) mustApply(strict bool) fbRun {
	e.t.Helper()
	run := e.apply(strict)
	if run.err != nil {
		e.t.Fatalf("Apply: %v", run.err)
	}
	return run
}

// envFile returns the materialized env file of the instance's repo.
func (e *fbEnv) envFile() string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.instanceRoot, "all", "repo1", ".local.env"))
	if err != nil {
		e.t.Fatalf("reading materialized env: %v", err)
	}
	return string(data)
}

// tokens returns the vault version tokens instance state records, by
// source ID ("provider/key", "/key" for the anonymous provider).
func (e *fbEnv) tokens() map[string]string {
	e.t.Helper()
	st, err := LoadState(e.instanceRoot)
	if err != nil {
		e.t.Fatalf("LoadState: %v", err)
	}
	out := map[string]string{}
	for _, mf := range st.ManagedFiles {
		for _, s := range mf.Sources {
			if s.Kind == SourceKindVault {
				out[s.SourceID] = s.VersionToken
			}
		}
	}
	return out
}

func (e *fbEnv) stored(project string) map[string]store.Entry {
	e.t.Helper()
	entries, err := store.Load(e.storeDir, fbIdentity(project))
	if err != nil {
		e.t.Fatalf("store.Load: %v", err)
	}
	return entries
}

func (e *fbEnv) seed(project string, entries map[string]store.Entry) {
	e.t.Helper()
	if err := store.Update(e.storeDir, fbIdentity(project), entries, nil, false); err != nil {
		e.t.Fatalf("seeding the store: %v", err)
	}
}

func assertEnvHas(t *testing.T, env string, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if !strings.Contains(env, l+"\n") {
			t.Errorf("materialized env lacks %q:\n%s", l, env)
		}
	}
}

func assertEnvLacks(t *testing.T, env string, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if strings.Contains(env, k+"=") {
			t.Errorf("materialized env holds %s:\n%s", k, env)
		}
	}
}

func assertServed(t *testing.T, n *fallbacknotice.Collector, reason fallbacknotice.Reason, projects ...string) {
	t.Helper()
	notes := n.ServedNotes()
	if len(notes) != len(projects) {
		t.Fatalf("served notes = %+v, want one for each of %v", notes, projects)
	}
	for i, p := range projects {
		if notes[i].Identity != fbNoticeIdentity(p) || notes[i].Reason != reason {
			t.Errorf("served note %d = %+v, want %s with reason %v", i, notes[i], p, reason)
		}
	}
}

// R16 per layer: a key stored by an earlier run is served when its
// provider fails unauthenticated, in the team configuration, the
// workspace overlay and the personal global configuration.
func TestStoreFallbackServesEveryLayer(t *testing.T) {
	const value = "stored-layer-value-1"
	layers := []struct {
		name  string
		write func(e *fbEnv, providerBody string)
		table string
		ref   string
	}{
		{"team", func(e *fbEnv, b string) {
			e.setTeam(b + secretsTable("env.secrets", "", "LAYER_KEY"))
		}, "vault.provider", "/LAYER_KEY"},
		{"overlay", func(e *fbEnv, b string) {
			e.setTeam("")
			e.setOverlay(b + secretsTable("env.secrets", "", "LAYER_KEY"))
		}, "vault.provider", "/LAYER_KEY"},
		{"personal", func(e *fbEnv, b string) {
			e.setTeam("")
			e.setGlobal(b + secretsTable("global.env.secrets", "personal/", "LAYER_KEY"))
		}, "global.vault.providers.personal", "personal/LAYER_KEY"},
	}
	for _, l := range layers {
		t.Run(l.name, func(t *testing.T) {
			e := newFBEnv(t, l.name == "overlay")
			l.write(e, fakeProvider(l.table, "proj-"+l.name, map[string]string{"LAYER_KEY": value}))
			first := e.mustApply(false)
			if !first.notices.Empty() {
				t.Fatalf("a healthy run recorded notices: %+v", first.notices.ServedNotes())
			}
			if got := e.stored("proj-" + l.name)["LAYER_KEY"]; string(got.Value) != value {
				t.Fatalf("first run stored %+v", got)
			}

			l.write(e, fakeProvider(l.table, "proj-"+l.name, nil, `fail_class = "unauthenticated"`))
			second := e.mustApply(false)
			assertEnvHas(t, e.envFile(), "LAYER_KEY="+value)
			assertServed(t, second.notices, fallbacknotice.ReasonLoggedOut, "proj-"+l.name)
			if got := e.tokens()[l.ref]; got != fakeToken(value) {
				t.Errorf("state token for %s = %q, want the stored one", l.ref, got)
			}
		})
	}
}

// R7/R9/R16: an unreachable provider, timed out or not, is served too,
// and the note carries the matching reason.
func TestStoreFallbackServesUnreachable(t *testing.T) {
	cases := []struct {
		knob   string
		reason fallbacknotice.Reason
	}{
		{"timed_out", fallbacknotice.ReasonTimedOut},
		{"unreachable", fallbacknotice.ReasonUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.knob, func(t *testing.T) {
			e := newFBEnv(t, false)
			e.seed("proj", map[string]store.Entry{"API_KEY": {
				Value: []byte("stored-api-value-1"), ResolvedAt: time.Now().Add(-time.Hour), VersionToken: "v-stored-7",
			}})
			e.setTeam(fakeProvider("vault.provider", "proj", nil, `fail_class = "`+tc.knob+`"`) + secretsTable("env.secrets", "", "API_KEY"))
			run := e.mustApply(false)
			assertEnvHas(t, e.envFile(), "API_KEY=stored-api-value-1")
			if got := e.tokens()["/API_KEY"]; got != "v-stored-7" {
				t.Errorf("state token = %q, want v-stored-7", got)
			}
			assertServed(t, run.notices, tc.reason, "proj")
		})
	}
}

// R16/R17/R20 per key: two stored keys are served and two unstored,
// non-required keys are omitted, with one served note and one miss for
// the identity.
func TestStoreFallbackPerKeyWithinOneIdentity(t *testing.T) {
	e := newFBEnv(t, false)
	e.seed("proj", map[string]store.Entry{
		"KEY_A": {Value: []byte("stored-a-value-1"), ResolvedAt: time.Now()},
		"KEY_B": {Value: []byte("stored-b-value-1"), ResolvedAt: time.Now()},
	})
	e.setTeam(fakeProvider("vault.provider", "proj", nil, `fail_class = "unauthenticated"`) +
		secretsTable("env.secrets", "", "KEY_A", "KEY_B", "KEY_C", "KEY_D"))
	run := e.mustApply(false)
	env := e.envFile()
	assertEnvHas(t, env, "KEY_A=stored-a-value-1", "KEY_B=stored-b-value-1")
	assertEnvLacks(t, env, "KEY_C", "KEY_D")
	assertServed(t, run.notices, fallbacknotice.ReasonLoggedOut, "proj")
	if m := run.notices.Misses(); len(m) != 1 || m[0] != fbNoticeIdentity("proj") {
		t.Fatalf("misses = %+v, want one for proj", m)
	}
}

// R18: served values count as supplied for strict mode and the
// required-key check.
func TestStoreFallbackServedKeysAreSupplied(t *testing.T) {
	e := newFBEnv(t, false)
	e.seed("proj", map[string]store.Entry{
		"REQ_KEY":   {Value: []byte("stored-required-1"), ResolvedAt: time.Now()},
		"OTHER_KEY": {Value: []byte("stored-other-val-1"), ResolvedAt: time.Now()},
	})
	e.setTeam(fakeProvider("vault.provider", "proj", nil, `fail_class = "unauthenticated"`) +
		secretsTable("env.secrets", "", "REQ_KEY", "OTHER_KEY") +
		"\n[env.secrets.required]\nREQ_KEY = \"a required key\"\n")
	run := e.mustApply(true)
	assertEnvHas(t, e.envFile(), "REQ_KEY=stored-required-1", "OTHER_KEY=stored-other-val-1")
	if report := run.keys.Report(); len(report) != 0 {
		t.Fatalf("key report lists served keys as missing: %+v", report)
	}
	assertServed(t, run.notices, fallbacknotice.ReasonLoggedOut, "proj")

	// The same required key with nothing stored still fails the run.
	e2 := anotherFBEnv(t, false)
	e2.setTeam(fakeProvider("vault.provider", "proj", nil, `fail_class = "unauthenticated"`) +
		secretsTable("env.secrets", "", "REQ_KEY") + "\n[env.secrets.required]\nREQ_KEY = \"a required key\"\n")
	if run := e2.apply(true); run.err == nil {
		t.Fatal("strict run with an unstored key succeeded")
	}
}

// R10/R16: a fallback run keeps the stored tokens and times, and the
// next healthy run stores the new values with new times and records
// no notices.
func TestStoreFallbackRecovery(t *testing.T) {
	e := newFBEnv(t, false)
	body := func(values map[string]string, knobs ...string) string {
		return fakeProvider("vault.provider", "proj", values, knobs...) + secretsTable("env.secrets", "", "TOKEN_A", "TOKEN_B")
	}
	e.setTeam(body(map[string]string{"TOKEN_A": "first-value-a-1", "TOKEN_B": "first-value-b-1"}))
	e.mustApply(false)
	firstStored := e.stored("proj")

	e.setTeam(body(nil, `fail_class = "unauthenticated"`))
	e.mustApply(false)
	tokens := e.tokens()
	if tokens["/TOKEN_A"] != fakeToken("first-value-a-1") || tokens["/TOKEN_B"] != fakeToken("first-value-b-1") {
		t.Errorf("fallback run state tokens = %v, want the stored ones", tokens)
	}
	for k, entry := range e.stored("proj") {
		if !entry.ResolvedAt.Equal(firstStored[k].ResolvedAt) {
			t.Errorf("%s resolved_at moved from %v to %v on a fallback run", k, firstStored[k].ResolvedAt, entry.ResolvedAt)
		}
	}

	e.setTeam(body(map[string]string{"TOKEN_A": "second-value-a-2", "TOKEN_B": "second-value-b-2"}))
	third := e.mustApply(false)
	if !third.notices.Empty() {
		t.Fatalf("a recovered run recorded notices: served %+v, misses %+v", third.notices.ServedNotes(), third.notices.Misses())
	}
	for k, want := range map[string]string{"TOKEN_A": "second-value-a-2", "TOKEN_B": "second-value-b-2"} {
		got := e.stored("proj")[k]
		if string(got.Value) != want || got.VersionToken != fakeToken(want) || !got.ResolvedAt.After(firstStored[k].ResolvedAt) {
			t.Errorf("%s after recovery = %q token %q at %v", k, got.Value, got.VersionToken, got.ResolvedAt)
		}
	}
}

// R12/R20: providers on one API domain with different projects keep
// separate stores.
func TestStoreFallbackIdentitySeparation(t *testing.T) {
	config := func() string {
		return fakeProvider("vault.providers.alpha", "proj-alpha", nil, `fail_class = "unauthenticated"`) +
			fakeProvider("vault.providers.beta", "proj-beta", nil, `fail_class = "unauthenticated"`) +
			"\n[env.secrets]\nALPHA_KEY = \"vault://alpha/SHARED\"\nBETA_KEY = \"vault://beta/SHARED\"\n"
	}

	e := newFBEnv(t, false)
	e.seed("proj-alpha", map[string]store.Entry{"SHARED": {Value: []byte("alpha-stored-val-1"), ResolvedAt: time.Now()}})
	e.setTeam(config())
	run := e.mustApply(false)
	env := e.envFile()
	assertEnvHas(t, env, "ALPHA_KEY=alpha-stored-val-1")
	assertEnvLacks(t, env, "BETA_KEY")
	assertServed(t, run.notices, fallbacknotice.ReasonLoggedOut, "proj-alpha")

	e2 := anotherFBEnv(t, false)
	e2.seed("proj-alpha", map[string]store.Entry{"SHARED": {Value: []byte("alpha-stored-val-1"), ResolvedAt: time.Now()}})
	e2.seed("proj-beta", map[string]store.Entry{"SHARED": {Value: []byte("beta-stored-val-1"), ResolvedAt: time.Now()}})
	e2.setTeam(config())
	run = e2.mustApply(false)
	assertEnvHas(t, e2.envFile(), "ALPHA_KEY=alpha-stored-val-1", "BETA_KEY=beta-stored-val-1")
	assertServed(t, run.notices, fallbacknotice.ReasonLoggedOut, "proj-alpha", "proj-beta")
}

// R10/R16: an empty stored token is served back empty. Instance state
// records a vault source only for a non-empty token (a value resolved
// with an empty token is recorded by content hash today), so an empty
// served token shows up as no vault source for the key. Storing a
// resolved value's empty token is covered in the storefallback tests;
// the fake backend always returns one.
func TestStoreFallbackEmptyToken(t *testing.T) {
	e := newFBEnv(t, false)
	e.seed("proj", map[string]store.Entry{"API_KEY": {Value: []byte("stored-api-value-1"), ResolvedAt: time.Now()}})
	e.setTeam(fakeProvider("vault.provider", "proj", nil, `fail_class = "unauthenticated"`) + secretsTable("env.secrets", "", "API_KEY"))
	e.mustApply(false)
	assertEnvHas(t, e.envFile(), "API_KEY=stored-api-value-1")
	if tok, ok := e.tokens()["/API_KEY"]; ok {
		t.Errorf("state recorded vault token %q for a value served with an empty token", tok)
	}
	if got := e.stored("proj")["API_KEY"]; got.VersionToken != "" {
		t.Errorf("stored token = %q, want empty", got.VersionToken)
	}
}

// R10/R12: only requested keys are stored, with their times and tokens,
// in a 0600 file inside a 0700 directory under XDG_STATE_HOME.
func TestStoreFallbackStoresOnlyRequestedKeys(t *testing.T) {
	e := newFBEnv(t, false)
	e.setTeam(fakeProvider("vault.provider", "proj", map[string]string{
		"KEY_A": "value-a-resolved-1", "KEY_B": "value-b-resolved-1", "KEY_C": "value-c-never-asked",
	}) + secretsTable("env.secrets", "", "KEY_A", "KEY_B"))
	before := time.Now()
	e.mustApply(false)

	info, err := os.Stat(e.storeDir)
	if err != nil {
		t.Fatalf("store directory: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("store directory mode = %o, want 0700", info.Mode().Perm())
	}
	files, _ := filepath.Glob(filepath.Join(e.storeDir, "*.json"))
	if len(files) != 1 {
		t.Fatalf("data files = %v, want one", files)
	}
	if fi, _ := os.Stat(files[0]); fi.Mode().Perm() != 0o600 {
		t.Errorf("data file mode = %o, want 0600", fi.Mode().Perm())
	}
	stored := e.stored("proj")
	if len(stored) != 2 {
		t.Fatalf("stored keys = %d, want KEY_A and KEY_B only", len(stored))
	}
	for k, v := range map[string]string{"KEY_A": "value-a-resolved-1", "KEY_B": "value-b-resolved-1"} {
		got := stored[k]
		if string(got.Value) != v || got.VersionToken != fakeToken(v) || got.ResolvedAt.Before(before.Add(-time.Second)) {
			t.Errorf("%s = %q token %q at %v", k, got.Value, got.VersionToken, got.ResolvedAt)
		}
	}
	raw, _ := os.ReadFile(files[0])
	if strings.Contains(string(raw), "KEY_C") {
		t.Error("the store holds a key the run never requested")
	}
}

// R11: a key the provider reports missing is evicted; an answered 403
// or 404 evicts every key of the identity.
func TestStoreFallbackEviction(t *testing.T) {
	values := map[string]string{"KEY_A": "value-a-resolved-1", "KEY_B": "value-b-resolved-1"}
	refs := secretsTable("env.secrets", "", "KEY_A", "KEY_B")
	lapsed := fakeProvider("vault.provider", "proj", nil, `fail_class = "unauthenticated"`) + refs

	t.Run("missing key", func(t *testing.T) {
		e := newFBEnv(t, false)
		e.setTeam(fakeProvider("vault.provider", "proj", values) + refs)
		e.mustApply(false)
		e.setTeam(fakeProvider("vault.provider", "proj", map[string]string{"KEY_B": "value-b-resolved-1"}) + refs)
		e.mustApply(false)
		if _, ok := e.stored("proj")["KEY_A"]; ok {
			t.Fatal("KEY_A still stored after the provider reported it missing")
		}
		e.setTeam(lapsed)
		e.mustApply(false)
		env := e.envFile()
		assertEnvHas(t, env, "KEY_B=value-b-resolved-1")
		assertEnvLacks(t, env, "KEY_A")
	})

	for _, status := range []int{403, 404} {
		t.Run(fmt.Sprintf("answered %d", status), func(t *testing.T) {
			e := newFBEnv(t, false)
			e.setTeam(fakeProvider("vault.provider", "proj", values) + refs)
			e.mustApply(false)
			e.setTeam(fakeProvider("vault.provider", "proj", nil, `fail_class = "answered"`, fmt.Sprintf("fail_status = %d", status)) + refs)
			if run := e.apply(false); run.err == nil {
				t.Fatal("an answered failure did not fail the run")
			}
			if got := e.stored("proj"); len(got) != 0 {
				t.Fatalf("stored keys after an answered %d = %d, want none", status, len(got))
			}
			e.setTeam(lapsed)
			run := e.mustApply(false)
			assertEnvLacks(t, e.envFile(), "KEY_A", "KEY_B")
			if len(run.notices.ServedNotes()) != 0 {
				t.Fatalf("served after eviction: %+v", run.notices.ServedNotes())
			}
		})
	}
}

// R10: credential sync never touches the store. Its provider is a
// store-identified fake, so only the absence of wrapping keeps it out.
func TestStoreFallbackCredentialSyncIsNotWrapped(t *testing.T) {
	var authHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		authHits++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"accessToken": "test-jwt"})
	}))
	defer srv.Close()

	body := "client_id = \"cid-synced\"\nclient_secret = \"csecret-synced\"\napi_url = \"" + srv.URL + "\"\n"
	team := "\n[vault.provider]\nkind = \"infisical\"\nproject = \"team-proj\"\n"
	syncTable := "global.vault.provider"
	syncIdentity := vault.Identity{Kind: "fake", APIDomain: fbDomain, ProjectID: "sync-proj", Environment: "dev", FolderPath: CredentialSyncPathPrefix + "infisical"}

	t.Run("successful lookup writes nothing", func(t *testing.T) {
		e := newFBEnv(t, false)
		e.setTeam(team)
		e.setGlobal(fakeProvider(syncTable, "sync-proj", map[string]string{"p-team-proj": body}, `project = "sync-proj"`))
		authHits = 0
		e.mustApply(false)
		if authHits != 1 {
			t.Fatalf("universal-auth hits = %d, want 1 (the synced credential was used)", authHits)
		}
		if _, err := os.Stat(e.storeDir); !os.IsNotExist(err) {
			t.Fatalf("credential sync wrote the store (stat err %v)", err)
		}
	})

	t.Run("lapsed lookup reads nothing", func(t *testing.T) {
		e := newFBEnv(t, false)
		if err := store.Update(e.storeDir, syncIdentity, map[string]store.Entry{"p-team-proj": {Value: []byte(body), ResolvedAt: time.Now()}}, nil, false); err != nil {
			t.Fatal(err)
		}
		e.setTeam(team)
		e.setGlobal(fakeProvider(syncTable, "sync-proj", nil, `project = "sync-proj"`, `fail_class = "unauthenticated"`))
		authHits = 0
		run := e.mustApply(false)
		if authHits != 0 {
			t.Fatalf("universal-auth hits = %d: the stored credential body was synced", authHits)
		}
		if !run.notices.Empty() {
			t.Fatalf("credential sync recorded notices: %+v", run.notices.ServedNotes())
		}
	})
}

// R19/R20: a value stored 30 days before the run is served (stored values
// have no age ceiling), and the warning's age reads "30 days".
func TestStoreFallbackServesAThirtyDayOldValue(t *testing.T) {
	e := newFBEnv(t, false)
	e.seed("proj", map[string]store.Entry{"OLD_KEY": {
		Value: []byte("stored-old-value-1"), ResolvedAt: time.Now().Add(-30 * 24 * time.Hour), VersionToken: "v-old",
	}})
	e.setTeam(fakeProvider("vault.provider", "proj", nil, `fail_class = "unauthenticated"`) + secretsTable("env.secrets", "", "OLD_KEY"))
	run := e.mustApply(false)
	assertEnvHas(t, e.envFile(), "OLD_KEY=stored-old-value-1")
	text := run.notices.RenderText()
	if !strings.Contains(text, "may be stale") || !strings.Contains(text, "the oldest value is 30 days old") {
		t.Errorf("served warning does not read 30 days:\n%s", text)
	}
}

// A caller that leaves Applier.Notices nil still hears about a served
// value: the run collects the notices itself and writes them through
// the reporter.
func TestStoreFallbackUnwiredNoticesStillRender(t *testing.T) {
	e := newFBEnv(t, false)
	e.unwired = true
	e.seed("proj", map[string]store.Entry{"API_KEY": {
		Value: []byte("stored-api-value-1"), ResolvedAt: time.Now().Add(-time.Hour), VersionToken: "v-stored-7",
	}})
	e.setTeam(fakeProvider("vault.provider", "proj", nil, `fail_class = "unauthenticated"`) + secretsTable("env.secrets", "", "API_KEY"))
	run := e.mustApply(false)
	assertEnvHas(t, e.envFile(), "API_KEY=stored-api-value-1")
	if got := run.output.String(); !strings.Contains(got, "using stored values that may be stale for fake project proj") {
		t.Errorf("reporter output has no served warning:\n%s", got)
	}
}
