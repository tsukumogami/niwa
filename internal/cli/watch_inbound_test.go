package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/testutil/claudestub"
	"github.com/tsukumogami/niwa/internal/watch"
)

// watch_inbound_test.go holds the one test that says a `niwa watch` review
// session never starts accepting messages from other sessions, however the
// machine is configured.
//
// The exclusion matters because a review session reads an untrusted diff. The
// dispatch path's own tests cannot speak for it: they assert what runDispatch
// builds, and `niwa watch` builds its own launch through the same launcher
// without going anywhere near runDispatch. So this test observes the argv the
// claude binary is actually handed at BOTH of watch's sites -- the stage's
// launch, with `dispatchLaunch` left pointing at the real launcher and neither
// realDispatchLaunch nor buildLaunchArgs replaced, and the continuation's wake
// through the real retask core. The copy-rule stub `claude`
// (internal/testutil/claudestub) first on PATH records what it received, and
// saves the stage's options with the job the way Claude Code does, so the
// continuation wakes a session the stage really launched.
//
// Everything it does replace sits outside the launch and was already a seam:
// provisioning, the session-id capture (answered from the stub's own job
// entry), and instance destruction. If stageReview, continueReview,
// realDispatchLaunch, buildLaunchArgs or the wake ever starts putting the key
// in, this fails.

// watchInboundSourceRepo builds a one-commit git repository the hardened PR-head
// fetch can pull from, and returns its path and head SHA. The two uploadpack
// settings are what let a fetch ask for a bare SHA over the local transport,
// which is exactly what FetchPRHead does.
func watchInboundSourceRepo(t *testing.T) (repoDir, sha string) {
	t.Helper()
	repoDir = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
			// The developer's own git config is shut out, the way gitRunInCLI
			// does it. Without this a global commit.gpgsign, hooksPath, or
			// signing key turns this fixture into a failure the developer sees
			// and CI never does.
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet")
	run("config", "uploadpack.allowReachableSHA1InWant", "true")
	run("config", "uploadpack.allowAnySHA1InWant", "true")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("under review\n"), 0o644); err != nil {
		t.Fatalf("writing the fixture file: %v", err)
	}
	run("add", "-A")
	run("commit", "--quiet", "-m", "fixture")
	return repoDir, run("rev-parse", "HEAD")
}

// watchInboundGitHub serves the one GitHub call both sites make.
func watchInboundGitHub(t *testing.T, cloneURL, sha string) *github.APIClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/pulls/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"head": map[string]any{
				"sha":  sha,
				"repo": map[string]any{"clone_url": cloneURL},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return &github.APIClient{HTTPClient: srv.Client(), BaseURL: srv.URL}
}

// TestWatchReviewLaunchesNeverCarryCrossSessionInbound drives both `niwa watch`
// sites with the machine setting on and the --accept-session-messages flag
// variable on, and reads what the claude binary was handed.
func TestWatchReviewLaunchesNeverCarryCrossSessionInbound(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// The two inputs that decide the dispatch path's answer, both set to the
	// most demanding value: a machine setting that says yes, and a flag that
	// says yes. Watch must ignore both.
	configHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configHome, "niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "niwa", "config.toml"),
		[]byte("[global]\naccept_session_messages_on_dispatch = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", configHome)
	home := canonicalTempDir(t)
	t.Setenv("HOME", home)
	resetDispatchFlags(t)
	on := true
	dispatchAcceptSessionMessages = &on

	stub, err := claudestub.New(t.TempDir(), home)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub.BinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, kv := range stub.Env() {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	shortenRetaskBounds(t, 5*time.Second, 5*time.Second)
	prevList := listClaudeSessionsFunc
	listClaudeSessionsFunc = realListClaudeSessions
	t.Cleanup(func() { listClaudeSessionsFunc = prevList })

	repoDir, sha := watchInboundSourceRepo(t)
	client := watchInboundGitHub(t, repoDir, sha)

	// Canonical, because the stub records a session's cwd with symlinks
	// resolved, and the liveness checks compare it with the instance path.
	root := canonicalTempDir(t)
	pr := github.PRRef{Owner: "acme", Repo: "widgets", Number: 7, URL: "https://example.test/pr/7"}
	// The uncontained plan: resolveAskPosture returns false straight away, so
	// nothing reaches the real ~/.claude.json trust file. Containment is not
	// what this test is about -- the argv is.
	plan := reviewPlan{sandbox: false}

	// Seams outside the launch.
	prevProvision := provisionInstanceFunc
	prevCapture := watchCapture
	prevDestroy := destroyInstanceFunc
	t.Cleanup(func() {
		provisionInstanceFunc = prevProvision
		watchCapture = prevCapture
		destroyInstanceFunc = prevDestroy
	})

	instancePath := filepath.Join(root, "review-instance")
	provisionInstanceFunc = func(_ context.Context, _, _, namePrefix, sep string, _ int) (provisionResult, error) {
		// The retask core reads the instance state, as it does any instance.
		if err := writeMinimalInstanceState(instancePath); err != nil {
			return provisionResult{}, err
		}
		return provisionResult{Name: "review" + sep + namePrefix, Path: instancePath}, nil
	}
	// The capture answers with the session the stub's launch minted.
	watchCapture = func(_ agentplan.SessionRecords, _, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
		shorts, err := stub.JobEntries()
		if err != nil || len(shorts) != 1 {
			return "", "", errors.New("the stub holds no single job entry")
		}
		sid, err := stub.SessionID(shorts[0])
		return sid, shorts[0], err
	}
	destroyInstanceFunc = func(string) error { return nil }

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	// --- Site 1: the fresh stage.
	if err := stageReview(cmd, root, root, "", client, pr, plan); err != nil {
		t.Fatalf("stageReview: %v", err)
	}
	stageArgv := onlyBackgroundLaunch(t, stub, "the fresh stage")
	assertNoCrossSessionInbound(t, stageArgv, "the fresh stage")
	assertLastElementIs(t, stageArgv, watch.BuildReviewPrompt(pr, watch.DefaultCloneRelDir, watch.DefaultDraftRelPath), "the fresh stage")

	// --- Site 2: the continuation, of the record the stage saved.
	if err := os.Remove(stub.Log); err != nil {
		t.Fatalf("clearing the recordings between sites: %v", err)
	}
	handles, err := watch.ListStagedHandles(root)
	if err != nil || len(handles) != 1 {
		t.Fatalf("staged handles = %q (err %v), want the one the stage saved", handles, err)
	}
	rec, err := watch.LoadStagedRecord(root, handles[0])
	if err != nil {
		t.Fatalf("loading the staged record: %v", err)
	}
	if rec.SessionID == "" || rec.SettingsDigest == "" {
		t.Fatalf("the stage saved no session id or settings digest: %+v", rec)
	}

	out := &strings.Builder{}
	cmd.SetOut(out)
	if err := continueReview(cmd, root, root, "", client, pr, rec, plan); err != nil {
		t.Fatalf("continueReview: %v", err)
	}
	wakeArgv := onlyBackgroundLaunch(t, stub, "the continuation")
	want := []string{"--bg", "--resume", rec.SessionID, "--", watch.BuildResumePrompt(watch.DefaultCloneRelDir, watch.DefaultDraftRelPath)}
	if !slices.Equal(wakeArgv, want) {
		t.Fatalf("the continuation handed claude %q, want exactly %q\nstdout:\n%s", wakeArgv, want, out)
	}
	assertNoCrossSessionInbound(t, wakeArgv, "the continuation")

	// The argv is one of two ways the behavior could reach a review session.
	// The other is the instance's own settings file, which ApplyReviewSettings
	// writes and re-writes from inside both functions above, so a regression
	// there would be invisible to every assertion so far.
	settingsPath := filepath.Join(instancePath, ".claude", "settings.json")
	settings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("reading the review instance settings %s: %v", settingsPath, err)
	}
	if strings.Contains(string(settings), "crossSessionInbound") {
		t.Errorf("the review instance's settings file carries crossSessionInbound:\n%s", settings)
	}
}

// onlyBackgroundLaunch asserts the stub recorded exactly one --bg invocation
// and returns its argv. More than one would mean the site launched twice; none
// would mean the recording is not the real launch and every assertion below it
// is vacuous.
func onlyBackgroundLaunch(t *testing.T, stub *claudestub.Stub, site string) []string {
	t.Helper()
	calls, err := stub.Calls()
	if err != nil {
		t.Fatal(err)
	}
	bg := wakeCalls(calls)
	if len(bg) != 1 {
		t.Fatalf("%s recorded %d --bg invocations, want exactly 1; all recordings:\n%s", site, len(bg), claudestub.Quote(calls))
	}
	return bg[0]
}

// assertNoCrossSessionInbound is the point of the file: no element of the argv
// the binary received names the key, whatever the machine and the flag say.
func assertNoCrossSessionInbound(t *testing.T, argv []string, site string) {
	t.Helper()
	for i, e := range argv {
		if strings.Contains(e, "crossSessionInbound") {
			t.Errorf("%s launched claude with crossSessionInbound in argv element %d (%q); a review session reads an untrusted diff and must never accept messages from other sessions.\nfull argv: %q",
				site, i, e, argv)
		}
	}
}

// assertLastElementIs checks the argv ends with the site's own prompt, which is
// what says the recording is that site's real launch rather than some other
// invocation that happened to land in the log.
func assertLastElementIs(t *testing.T, argv []string, want, site string) {
	t.Helper()
	if len(argv) == 0 {
		t.Fatalf("%s recorded an empty argv", site)
	}
	if got := argv[len(argv)-1]; got != want {
		t.Errorf("%s: last argv element = %q; want the site's prompt %q", site, got, want)
	}
}
