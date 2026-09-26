package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/watch"
)

// These tests characterize what `niwa watch` forwards to the review agent it
// launches, at staging (stageReview) and at resume (continueReview). Every
// expectation is a string literal on purpose: computing it through the slug
// sanitizer, the passthrough builder or the write-posture helper would make the
// assertion quote the code under test, so a change inside those helpers that
// altered what watch forwards would pass unnoticed.
//
// They mutate package globals (the seams and the dispatch flag variables), so
// none of them runs in parallel.

const (
	watchCharHeadSHA   = "0123456789abcdef0123456789abcdef01234567"
	watchCharOldSHA    = "89abcdef0123456789abcdef0123456789abcdef"
	watchCharSessionID = "5f0c2d1e-7a3b-4c9d-8e2f-1a2b3c4d5e6f"
	watchCharShortID   = "5f0c2d1e"
	watchCharCloneURL  = "https://github.com/acme/widget.git"
	watchCharPRURL     = "https://github.com/acme/widget/pull/7"
)

var watchCharPR = github.PRRef{Owner: "acme", Repo: "widget", Number: 7, URL: watchCharPRURL}

// newPullHeadServer serves GET /repos/acme/widget/pulls/7 (the path
// GetPullHead builds) and returns a client pointed at it. Any other request
// fails the test.
func newPullHeadServer(t *testing.T) *github.APIClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/widget/pulls/7" {
			t.Errorf("unexpected GitHub request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"head":{"sha":%q,"repo":{"clone_url":%q}}}`, watchCharHeadSHA, watchCharCloneURL)
	}))
	t.Cleanup(srv.Close)
	return &github.APIClient{HTTPClient: srv.Client(), BaseURL: srv.URL}
}

type watchCharFetch struct {
	url, sha, dir, token string
}

type watchCharProvision struct {
	root, namePrefix, sep, path string
}

// watchCharFakes records what the watch seams were asked to do.
type watchCharFakes struct {
	launches    []launchRequest
	fetches     []watchCharFetch
	stops       []string
	provisioned watchCharProvision
	fetchErr    error
	destroyed   *[]string
}

// installWatchCharFakes stubs every seam stageReview/continueReview reach past
// the GitHub client, and forces the dispatch flag globals the passthrough
// builder reads to their zero values. Everything is restored on cleanup.
func installWatchCharFakes(t *testing.T) *watchCharFakes {
	t.Helper()
	f := &watchCharFakes{}

	prevFetch := fetchPRHeadFunc
	prevProvision := provisionInstanceFunc
	prevLaunch := dispatchLaunch
	prevCapture := watchCapture
	prevStop := stopSessionFunc
	prevPerm := dispatchPermissionMode
	prevAgent := dispatchAgent
	t.Cleanup(func() {
		fetchPRHeadFunc = prevFetch
		provisionInstanceFunc = prevProvision
		dispatchLaunch = prevLaunch
		watchCapture = prevCapture
		stopSessionFunc = prevStop
		dispatchPermissionMode = prevPerm
		dispatchAgent = prevAgent
	})

	dispatchPermissionMode = ""
	dispatchAgent = ""

	fetchPRHeadFunc = func(_ context.Context, remoteURL, sha, checkoutDir, token string) error {
		f.fetches = append(f.fetches, watchCharFetch{url: remoteURL, sha: sha, dir: checkoutDir, token: token})
		return f.fetchErr
	}
	provisionInstanceFunc = func(_ context.Context, root, _, namePrefix, sep string, _ int) (provisionResult, error) {
		name := "test-ws" + sep + namePrefix
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(dir, ".niwa"), 0o755); err != nil {
			return provisionResult{}, err
		}
		f.provisioned = watchCharProvision{root: root, namePrefix: namePrefix, sep: sep, path: dir}
		return provisionResult{Name: name, Path: dir}, nil
	}
	dispatchLaunch = func(_ context.Context, req launchRequest) error {
		f.launches = append(f.launches, req)
		return nil
	}
	// A miss, so neither test waits out the capture timeout or reads real job
	// state.
	watchCapture = func(_ agentplan.SessionRecords, _, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
		return "", "", errors.New("capture miss")
	}
	stopSessionFunc = func(_ context.Context, shortID string) error {
		f.stops = append(f.stops, shortID)
		return nil
	}
	f.destroyed = stubDestroyAll(t)
	return f
}

// setupWatchCharEnv points HOME (and every agent store niwa consults) at a
// fresh temp dir and returns a canonical workspace root plus that home.
func setupWatchCharEnv(t *testing.T) (root, home string) {
	t.Helper()
	home = canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	root = setupDispatchWorkspace(t)
	return root, home
}

func newWatchCharCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetContext(context.Background())
	return cmd, &stdout, &stderr
}

// assertStdoutLine requires one line of out to equal want exactly, so a
// trailing posture suffix is pinned rather than tolerated.
func assertStdoutLine(t *testing.T, out, want string) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if line == want {
			return
		}
	}
	t.Errorf("stdout has no line equal to\n  %q\nstdout:\n%s", want, out)
}

// assertSingleNameValue requires the display-name flag to appear once and to
// carry exactly one value, want.
func assertSingleNameValue(t *testing.T, passthrough []string, want string) {
	t.Helper()
	count, idx := 0, -1
	for i, a := range passthrough {
		if a == "--name" {
			count++
			idx = i
		}
	}
	if count != 1 {
		t.Fatalf("--name appears %d times in %q, want exactly once", count, passthrough)
	}
	if idx+1 >= len(passthrough) || passthrough[idx+1] != want {
		t.Fatalf("--name value in %q, want %q", passthrough, want)
	}
	if idx+2 < len(passthrough) && !strings.HasPrefix(passthrough[idx+2], "--") {
		t.Errorf("--name carries a second value %q in %q", passthrough[idx+2], passthrough)
	}
}

func TestWatchCharacterization_StageReview(t *testing.T) {
	cases := []struct {
		name            string
		plan            reviewPlan
		seedErr         error
		wantPassthrough []string
		wantLine        string
		wantSeeded      bool
	}{
		{
			name:            "unsandboxed",
			plan:            reviewPlan{},
			wantPassthrough: []string{"--name", "watch_acme_widget_7"},
			wantLine:        "niwa watch: staged review for acme/widget#7 (handle watch_acme_widget_7)",
			wantSeeded:      false,
		},
		{
			name:            "sandboxed_ask",
			plan:            reviewPlan{sandbox: true},
			wantPassthrough: []string{"--name", "watch_acme_widget_7", "--strict-mcp-config"},
			wantLine:        "niwa watch: staged review for acme/widget#7 (handle watch_acme_widget_7) -- out-of-instance writes surface an operator approval",
			wantSeeded:      true,
		},
		{
			name:            "sandboxed_deny",
			plan:            reviewPlan{sandbox: true},
			seedErr:         errors.New("seed refused"),
			wantPassthrough: []string{"--name", "watch_acme_widget_7", "--strict-mcp-config"},
			wantLine:        "niwa watch: staged review for acme/widget#7 (handle watch_acme_widget_7) -- out-of-instance writes are hard-denied",
			wantSeeded:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, home := setupWatchCharEnv(t)
			fakes := installWatchCharFakes(t)
			stub := stubAskPostureSeams(t, home, tc.seedErr)
			client := newPullHeadServer(t)
			cmd, stdout, _ := newWatchCharCmd()

			// Watch mints its instance token through the same source as
			// dispatch, so a fixed source pins the whole instance name.
			stubDispatchRand(t, constByteReader(0xab))

			if err := stageReview(cmd, root, root, "", client, watchCharPR, tc.plan); err != nil {
				t.Fatalf("stageReview: %v", err)
			}

			if len(fakes.launches) != 1 {
				t.Fatalf("launches = %d, want 1", len(fakes.launches))
			}
			launch := fakes.launches[0]
			if !reflect.DeepEqual(launch.Passthrough, tc.wantPassthrough) {
				t.Errorf("passthrough = %q, want %q", launch.Passthrough, tc.wantPassthrough)
			}
			assertSingleNameValue(t, launch.Passthrough, "watch_acme_widget_7")

			// Watch instance naming: <config>+watch_acme_widget_7-<8hex>.
			if fakes.provisioned.root != root {
				t.Errorf("provisioned under %q, want %q", fakes.provisioned.root, root)
			}
			if fakes.provisioned.sep != "+" {
				t.Errorf("provision sep = %q, want %q", fakes.provisioned.sep, "+")
			}
			if fakes.provisioned.namePrefix != "watch_acme_widget_7-abababab" {
				t.Errorf("provision namePrefix = %q, want %q", fakes.provisioned.namePrefix, "watch_acme_widget_7-abababab")
			}
			instancePath := fakes.provisioned.path
			if launch.InstanceDir != instancePath {
				t.Errorf("launch InstanceDir = %q, want %q", launch.InstanceDir, instancePath)
			}

			if len(fakes.fetches) != 1 {
				t.Fatalf("fetches = %d, want 1", len(fakes.fetches))
			}
			fetch := fakes.fetches[0]
			if fetch.url != "https://github.com/acme/widget.git" {
				t.Errorf("fetch url = %q", fetch.url)
			}
			if fetch.sha != "0123456789abcdef0123456789abcdef01234567" {
				t.Errorf("fetch sha = %q", fetch.sha)
			}
			if want := filepath.Join(instancePath, "pr-clone"); fetch.dir != want {
				t.Errorf("fetch dir = %q, want %q", fetch.dir, want)
			}
			if fetch.token != "" {
				t.Errorf("fetch token = %q, want empty", fetch.token)
			}

			rec, err := watch.LoadStagedRecord(root, "watch_acme_widget_7")
			if err != nil {
				t.Fatalf("LoadStagedRecord: %v", err)
			}
			if rec.Handle != "watch_acme_widget_7" {
				t.Errorf("record Handle = %q, want %q", rec.Handle, "watch_acme_widget_7")
			}
			if rec.DispatchedSHA != "0123456789abcdef0123456789abcdef01234567" {
				t.Errorf("record DispatchedSHA = %q", rec.DispatchedSHA)
			}
			if rec.InstancePath != instancePath {
				t.Errorf("record InstancePath = %q, want %q", rec.InstancePath, instancePath)
			}

			assertStdoutLine(t, stdout.String(), tc.wantLine)

			if len(*fakes.destroyed) != 0 {
				t.Errorf("a successful stage destroyed %q", *fakes.destroyed)
			}
			if stub.seeded != tc.wantSeeded {
				t.Errorf("trust seeded = %v, want %v", stub.seeded, tc.wantSeeded)
			}
			if stub.removed {
				t.Errorf("a successful stage removed the trust seed")
			}
		})
	}

	// The fetch error keeps its wrapping, launches nothing, and destroys the
	// instance it provisioned.
	t.Run("fetch_error_wrapping", func(t *testing.T) {
		root, home := setupWatchCharEnv(t)
		fakes := installWatchCharFakes(t)
		fakes.fetchErr = errors.New("boom")
		stubAskPostureSeams(t, home, nil)
		client := newPullHeadServer(t)
		cmd, _, _ := newWatchCharCmd()

		err := stageReview(cmd, root, root, "", client, watchCharPR, reviewPlan{})
		if err == nil {
			t.Fatal("stageReview succeeded, want the fetch error")
		}
		if err.Error() != "fetching PR head: boom" {
			t.Errorf("error = %q, want %q", err.Error(), "fetching PR head: boom")
		}
		if len(fakes.launches) != 0 {
			t.Errorf("launches = %d, want 0", len(fakes.launches))
		}
		if got, want := *fakes.destroyed, []string{fakes.provisioned.path}; !reflect.DeepEqual(got, want) {
			t.Errorf("destroyed = %q, want %q", got, want)
		}
	})
}

func TestWatchCharacterization_ContinueReview(t *testing.T) {
	cases := []struct {
		name            string
		plan            reviewPlan
		seedErr         error
		wantPassthrough []string
		wantLine        string
		wantSeeded      bool
	}{
		{
			name:            "unsandboxed",
			plan:            reviewPlan{},
			wantPassthrough: []string{"--name", "watch_acme_widget_7", "--resume", "5f0c2d1e-7a3b-4c9d-8e2f-1a2b3c4d5e6f"},
			wantLine:        "niwa watch: continued review for acme/widget#7 (handle watch_acme_widget_7)",
			wantSeeded:      false,
		},
		{
			name:            "sandboxed_ask",
			plan:            reviewPlan{sandbox: true},
			wantPassthrough: []string{"--name", "watch_acme_widget_7", "--resume", "5f0c2d1e-7a3b-4c9d-8e2f-1a2b3c4d5e6f", "--strict-mcp-config"},
			wantLine:        "niwa watch: continued review for acme/widget#7 (handle watch_acme_widget_7) -- out-of-instance writes surface an operator approval",
			wantSeeded:      true,
		},
		{
			name:            "sandboxed_deny",
			plan:            reviewPlan{sandbox: true},
			seedErr:         errors.New("seed refused"),
			wantPassthrough: []string{"--name", "watch_acme_widget_7", "--resume", "5f0c2d1e-7a3b-4c9d-8e2f-1a2b3c4d5e6f", "--strict-mcp-config"},
			wantLine:        "niwa watch: continued review for acme/widget#7 (handle watch_acme_widget_7) -- out-of-instance writes are hard-denied",
			wantSeeded:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, home := setupWatchCharEnv(t)
			fakes := installWatchCharFakes(t)
			stub := stubAskPostureSeams(t, home, tc.seedErr)
			client := newPullHeadServer(t)
			cmd, stdout, _ := newWatchCharCmd()

			inst := filepath.Join(root, "test-ws+watch_acme_widget_7-4e33acfa")
			if err := os.MkdirAll(filepath.Join(inst, ".niwa"), 0o755); err != nil {
				t.Fatal(err)
			}
			jobsDir := filepath.Join(home, ".claude", "jobs")
			writeJobStateFile(t, jobsDir, watchCharShortID, watchCharSessionID, inst, nil)

			rec := watch.StagedRecord{
				Handle:        "watch_acme_widget_7",
				Owner:         "acme",
				Repo:          "widget",
				Number:        7,
				URL:           watchCharPRURL,
				InstancePath:  inst,
				DispatchedSHA: watchCharOldSHA,
				SessionID:     watchCharSessionID,
				ShortID:       watchCharShortID,
			}
			if err := watch.SaveStagedRecord(root, rec); err != nil {
				t.Fatal(err)
			}

			// Both liveness checks must pass, or continueReview silently takes
			// its "skipping continuation" no-op and the test pins nothing.
			if got := defaultJobsDir(); got != jobsDir {
				t.Fatalf("defaultJobsDir = %q, want %q", got, jobsDir)
			}
			if !sessionLive(jobsDir, watchCharSessionID, time.Now()) {
				t.Fatal("precondition: sessionLive is false")
			}
			if !instanceHasLiveJob(jobsDir, inst) {
				t.Fatal("precondition: instanceHasLiveJob is false")
			}

			if err := continueReview(cmd, root, root, "", client, watchCharPR, rec, tc.plan); err != nil {
				t.Fatalf("continueReview: %v", err)
			}
			out := stdout.String()
			if strings.Contains(out, "skipping continuation") {
				t.Fatalf("continuation was skipped:\n%s", out)
			}

			if len(fakes.launches) != 1 {
				t.Fatalf("launches = %d, want 1", len(fakes.launches))
			}
			launch := fakes.launches[0]
			if !reflect.DeepEqual(launch.Passthrough, tc.wantPassthrough) {
				t.Errorf("passthrough = %q, want %q", launch.Passthrough, tc.wantPassthrough)
			}
			assertSingleNameValue(t, launch.Passthrough, "watch_acme_widget_7")
			if launch.InstanceDir != inst {
				t.Errorf("launch InstanceDir = %q, want %q", launch.InstanceDir, inst)
			}

			if !reflect.DeepEqual(fakes.stops, []string{"5f0c2d1e"}) {
				t.Errorf("stops = %q, want [5f0c2d1e]", fakes.stops)
			}
			if len(fakes.fetches) != 1 {
				t.Fatalf("fetches = %d, want 1", len(fakes.fetches))
			}
			fetch := fakes.fetches[0]
			if fetch.sha != "0123456789abcdef0123456789abcdef01234567" {
				t.Errorf("fetch sha = %q", fetch.sha)
			}
			if want := filepath.Join(inst, "pr-clone"); fetch.dir != want {
				t.Errorf("fetch dir = %q, want %q", fetch.dir, want)
			}

			assertStdoutLine(t, out, tc.wantLine)

			got, err := watch.LoadStagedRecord(root, "watch_acme_widget_7")
			if err != nil {
				t.Fatalf("LoadStagedRecord: %v", err)
			}
			if got.Handle != "watch_acme_widget_7" {
				t.Errorf("record Handle = %q, want %q", got.Handle, "watch_acme_widget_7")
			}
			if got.DispatchedSHA != "0123456789abcdef0123456789abcdef01234567" {
				t.Errorf("record DispatchedSHA = %q", got.DispatchedSHA)
			}
			// The re-capture misses (stubbed), so today the ids are cleared and
			// the record stops being continuable.
			if got.SessionID != "" || got.ShortID != "" {
				t.Errorf("record ids = (%q, %q), want both cleared by the capture miss", got.SessionID, got.ShortID)
			}

			if stub.seeded != tc.wantSeeded {
				t.Errorf("trust seeded = %v, want %v", stub.seeded, tc.wantSeeded)
			}
			if len(*fakes.destroyed) != 0 {
				t.Errorf("a continuation destroyed %q", *fakes.destroyed)
			}
		})
	}
}
