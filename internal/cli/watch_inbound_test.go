package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/github"
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
// claude binary is actually handed at BOTH of watch's launch sites, with
// `dispatchLaunch` left pointing at the real launcher and neither
// realDispatchLaunch nor buildLaunchArgs replaced. A fake `claude` first on
// PATH records what it received.
//
// Everything it does replace sits outside the launch and was already a seam:
// provisioning, the session-id capture, the `claude stop` that precedes a
// resume, and instance destruction. If stageReview, continueReview,
// realDispatchLaunch, or buildLaunchArgs ever starts putting the key in, this
// fails.

// watchFakeClaudeScript is the fake `claude` the test puts first on PATH. Every
// invocation records its argv NUL-separated into a fresh file under a recording
// directory beside the binary; a --bg invocation also writes the Claude Code job
// state the capture path correlates by cwd, the way the functional fake does,
// and exits 0. Anything else exits non-zero, so an unexpected invocation is
// loud rather than silently recorded as a launch.
const watchFakeClaudeScript = `#!/bin/sh
d=$(dirname "$0")
recdir="$d/recorded"
mkdir -p "$recdir"
rec=$(mktemp "$recdir/argv-XXXXXX")
printf '%s\0' "$@" > "$rec"
case "$1" in
  --bg)
    sid="aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    short=$(printf '%s' "$sid" | cut -c1-8)
    jobdir="$HOME/.claude/jobs/$short"
    mkdir -p "$jobdir"
    cwd=$(pwd)
    printf '{"sessionId":"%s","template":"bg","state":"running","cwd":"%s"}\n' "$sid" "$cwd" > "$jobdir/state.json"
    exit 0
    ;;
  *)
    echo "fake claude: unsupported invocation: $*" >&2
    exit 1
    ;;
esac
`

// watchFakeClaude installs the fake claude first on PATH and returns the
// directory its recordings land in.
func watchFakeClaude(t *testing.T) (recordDir string) {
	t.Helper()
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(watchFakeClaudeScript), 0o755); err != nil {
		t.Fatalf("writing the fake claude: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return filepath.Join(binDir, "recorded")
}

// recordedLaunches reads every argv the fake claude recorded, newest last, each
// split back into its elements.
func recordedLaunches(t *testing.T, recordDir string) [][]string {
	t.Helper()
	entries, err := os.ReadDir(recordDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading the recordings: %v", err)
	}
	var out [][]string
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(recordDir, e.Name()))
		if err != nil {
			t.Fatalf("reading recording %s: %v", e.Name(), err)
		}
		elems := strings.Split(string(data), "\x00")
		if n := len(elems); n > 0 && elems[n-1] == "" {
			elems = elems[:n-1]
		}
		out = append(out, elems)
	}
	return out
}

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

// watchInboundGitHub serves the one GitHub call both launch sites make.
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
// launch sites with the machine setting on and the --accept-session-messages
// flag variable on, and reads what the claude binary was handed.
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetDispatchFlags(t)
	on := true
	dispatchAcceptSessionMessages = &on

	recordDir := watchFakeClaude(t)
	repoDir, sha := watchInboundSourceRepo(t)
	client := watchInboundGitHub(t, repoDir, sha)

	root := t.TempDir()
	pr := github.PRRef{Owner: "acme", Repo: "widgets", Number: 7, URL: "https://example.test/pr/7"}
	// The uncontained plan: resolveAskPosture returns false straight away, so
	// nothing reaches the real ~/.claude.json trust file. Containment is not
	// what this test is about -- the launch argv is.
	plan := reviewPlan{sandbox: false}

	// Seams outside the launch. The capture would otherwise scan the jobs dir
	// and the stop would run `claude stop`, and neither is part of what the
	// worker binary is handed.
	prevProvision := provisionInstanceFunc
	prevCapture := watchCapture
	prevStop := stopSessionFunc
	prevDestroy := destroyInstanceFunc
	t.Cleanup(func() {
		provisionInstanceFunc = prevProvision
		watchCapture = prevCapture
		stopSessionFunc = prevStop
		destroyInstanceFunc = prevDestroy
	})

	instancePath := filepath.Join(root, "review-instance")
	provisionInstanceFunc = func(_ context.Context, _, _, namePrefix, sep string, _ int) (provisionResult, error) {
		if err := os.MkdirAll(filepath.Join(instancePath, ".niwa"), 0o755); err != nil {
			return provisionResult{}, err
		}
		return provisionResult{Name: "review" + sep + namePrefix, Path: instancePath}, nil
	}
	const capturedSession = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	watchCapture = func(_ agentplan.SessionRecords, _, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
		return capturedSession, capturedSession[:8], nil
	}
	destroyInstanceFunc = func(string) error { return nil }
	stopSessionFunc = func(context.Context, string) error { return nil }

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())

	// --- Site 1: the fresh stage.
	if err := stageReview(cmd, root, root, "", client, pr, plan); err != nil {
		t.Fatalf("stageReview: %v", err)
	}
	stageArgv := onlyBackgroundLaunch(t, recordedLaunches(t, recordDir), "the fresh stage")
	assertNoCrossSessionInbound(t, stageArgv, "the fresh stage")
	assertLastElementIs(t, stageArgv, watch.BuildReviewPrompt(pr, watch.DefaultCloneRelDir, watch.DefaultDraftRelPath), "the fresh stage")

	// --- Site 2: the resume.
	if err := os.RemoveAll(recordDir); err != nil {
		t.Fatalf("clearing the recordings between sites: %v", err)
	}
	rec := watch.StagedRecord{
		Handle:        "watch-acme-widgets-7",
		Owner:         pr.Owner,
		Repo:          pr.Repo,
		Number:        pr.Number,
		URL:           pr.URL,
		DraftPath:     filepath.Join(instancePath, watch.DefaultDraftRelPath),
		InstancePath:  instancePath,
		DispatchedSHA: sha,
		SessionID:     capturedSession,
		ShortID:       capturedSession[:8],
	}
	seedLiveJob(t, home, rec.ShortID, capturedSession, instancePath)

	if err := continueReview(cmd, root, root, "", client, pr, rec, plan); err != nil {
		t.Fatalf("continueReview: %v", err)
	}
	resumeArgv := onlyBackgroundLaunch(t, recordedLaunches(t, recordDir), "the resume")
	assertNoCrossSessionInbound(t, resumeArgv, "the resume")
	assertLastElementIs(t, resumeArgv, watch.BuildResumePrompt(watch.DefaultCloneRelDir, watch.DefaultDraftRelPath), "the resume")
	assertResumesSession(t, resumeArgv, capturedSession)

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

// seedLiveJob writes the Claude Code job state that makes continueReview's
// two-way liveness cross-check pass: an entry for the session id, rooted in the
// instance directory.
func seedLiveJob(t *testing.T, home, shortID, sessionID, instancePath string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "jobs", shortID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"sessionId":%q,"template":"bg","state":"running","cwd":%q}`, sessionID, instancePath)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// onlyBackgroundLaunch asserts the site produced exactly one --bg invocation and
// returns its argv. More than one would mean the site launched twice; none would
// mean the recording is not the real launch and every assertion below it is
// vacuous.
func onlyBackgroundLaunch(t *testing.T, launches [][]string, site string) []string {
	t.Helper()
	var bg [][]string
	for _, argv := range launches {
		if len(argv) > 0 && argv[0] == "--bg" {
			bg = append(bg, argv)
		}
	}
	if len(bg) != 1 {
		t.Fatalf("%s recorded %d --bg invocations, want exactly 1; all recordings: %q", site, len(bg), launches)
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
// invocation that happened to land in the directory.
func assertLastElementIs(t *testing.T, argv []string, want, site string) {
	t.Helper()
	if len(argv) == 0 {
		t.Fatalf("%s recorded an empty argv", site)
	}
	if got := argv[len(argv)-1]; got != want {
		t.Errorf("%s: last argv element = %q; want the site's prompt %q", site, got, want)
	}
}

// assertResumesSession checks the resume site carried --resume followed by the
// staged session id, the other half of "this is the real launch".
func assertResumesSession(t *testing.T, argv []string, sessionID string) {
	t.Helper()
	for i, e := range argv {
		if e != "--resume" {
			continue
		}
		if i+1 < len(argv) && argv[i+1] == sessionID {
			return
		}
		t.Fatalf("the resume carried --resume followed by %q; want the staged session id %q\nfull argv: %q",
			argv[i+1:], sessionID, argv)
	}
	t.Fatalf("the resume did not carry --resume at all; full argv: %q", argv)
}
