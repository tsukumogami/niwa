package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/testutil/claudestub"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The continuation tests run continueReview against the real retask core, with
// the copy-rule stub (internal/testutil/claudestub) first on PATH as `claude`:
// the listing, the stop, the build check and the wake all go through it. Only
// the head fetch, provisioning, the stage's launch and capture, and instance
// destruction are fakes. They mutate package globals, so none runs in parallel.

const (
	// watchContinueInstanceName is the review instance the fixture stages.
	watchContinueInstanceName = "test-ws+watch_acme_widget_7-4e33acfa"
	watchContinueHandle       = "watch_acme_widget_7"
	watchContinueReapKey      = "abababab-abab-4bab-abab-abababababab"
	// The first line of the review session's transcript, as the stage left it.
	watchContinueStagePrompt = "STAGE"
)

// watchContinueSetup is what a test changes before the fixture stages its
// review. The fixture fills each field's default before calling mod.
type watchContinueSetup struct {
	// Unparseable makes the Claude trust config one claudetrust must skip, so
	// a sandboxed review takes the hard-deny posture.
	Unparseable bool
	// BeforeApply runs before the stage's ApplyReviewSettings and digest, so
	// what it writes is part of what the stage recorded.
	BeforeApply func(t *testing.T, inst string)
	// Session is the review session's job entry in the stub. A nil Respawn
	// is filled with the options watchReviewLaunch passes for the plan.
	Session claudestub.Session
	// Record is the staged record saved. An empty SettingsDigest is filled
	// with the stage's digest unless OmitDigest is set.
	Record     watch.StagedRecord
	OmitDigest bool
}

// watchContinueFixture is a staged review whose session is live and idle at
// the old head, with the GitHub head moved to a new one. Chunk-level tests for
// the gate and the pass build on it.
type watchContinueFixture struct {
	root, home, inst string
	plan             reviewPlan
	stub             *claudestub.Stub
	client           *github.APIClient
	// ghURL is the fake GitHub's base URL, for NIWA_GITHUB_API_URL.
	ghURL string
	// fakes records the stage-side seams: fetches, launches, destroys.
	fakes *watchCharFakes
	// stageDigest is the digest the stage recorded (before OmitDigest).
	stageDigest string

	mu       sync.Mutex
	head     string
	events   []string
	captures int
	wakeEnvs [][]string
	// fetchErr is what the fetch fake returns; fetchHook runs inside it, after
	// the fetch is recorded.
	fetchErr  error
	fetchHook func()
	// github serves any request other than the pull lookup; nil fails the test.
	github http.HandlerFunc
}

// newWatchContinueFixture stages a review the way stageReview leaves one: an
// instance with its review settings applied and digested, a live idle session
// in the stub rooted at the instance with the options the stage launch saves,
// its transcript, the staged record at the old head with its ids, reap key and
// digest, and the handled set at the old head. The GitHub fake reports
// watchCharHeadSHA as the new head.
//
// The seams it installs record one event sequence (eventLog): "list:<row>"
// for every listing ("running", "stopped" or "absent" for the review
// session's row), "stop:<short>", "fetch" and "wake". The listing, the stop
// and the wake are the real ones, run through the stub.
func newWatchContinueFixture(t *testing.T, sandbox bool, mod func(*watchContinueFixture, *watchContinueSetup)) *watchContinueFixture {
	t.Helper()
	root, home := setupWatchCharEnv(t)
	f := &watchContinueFixture{
		root: root,
		home: home,
		inst: filepath.Join(root, watchContinueInstanceName),
		plan: reviewPlan{sandbox: sandbox},
		head: watchCharHeadSHA,
	}
	f.fakes = installWatchCharFakes(t)

	stub, err := claudestub.New(t.TempDir(), home)
	if err != nil {
		t.Fatal(err)
	}
	f.stub = stub
	t.Setenv("PATH", stub.BinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, kv := range stub.Env() {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	shortenRetaskBounds(t, 2*time.Second, 2*time.Second)
	f.installSeams(t)

	setup := &watchContinueSetup{
		Session: claudestub.Session{
			Short:     watchCharShortID,
			SessionID: watchCharSessionID,
			Cwd:       f.inst,
			Alive:     true,
			Status:    "idle",
		},
		Record: watch.StagedRecord{
			Handle:        watchContinueHandle,
			Owner:         "acme",
			Repo:          "widget",
			Number:        7,
			URL:           watchCharPRURL,
			DraftPath:     filepath.Join(f.inst, watch.DefaultDraftRelPath),
			InstancePath:  f.inst,
			DispatchedSHA: watchCharOldSHA,
			SessionID:     watchCharSessionID,
			ShortID:       watchCharShortID,
			ReapKey:       watchContinueReapKey,
		},
	}
	if mod != nil {
		mod(f, setup)
	}
	stubWatchTrust(t, home, setup.Unparseable)

	// The stage, as stageReview runs it: the posture, the settings, the digest.
	if err := writeMinimalInstanceState(f.inst); err != nil {
		t.Fatal(err)
	}
	if setup.BeforeApply != nil {
		setup.BeforeApply(t, f.inst)
	}
	ask := resolveAskPosture(io.Discard, f.inst, sandbox)
	if err := watch.ApplyReviewSettings(f.inst, sandbox, ask); err != nil {
		t.Fatalf("staging the review settings: %v", err)
	}
	f.stageDigest, err = watch.ReviewSettingsDigest(f.inst)
	if err != nil {
		t.Fatal(err)
	}
	if setup.Record.SettingsDigest == "" && !setup.OmitDigest {
		setup.Record.SettingsDigest = f.stageDigest
	}

	if setup.Session.Respawn == nil {
		setup.Session.Respawn = watchReviewLaunch(f.inst, watchContinueHandle, "p", sandbox, watchLaunchEnv(root, setup.Record.ReapKey)).Passthrough
	}
	if err := stub.AddSession(setup.Session); err != nil {
		t.Fatal(err)
	}
	transcript := sessionattach.TranscriptPath(home, f.inst, watchCharSessionID)
	writeTestFile(t, transcript, `{"type":"user","prompt":"`+watchContinueStagePrompt+`"}`+"\n")

	if err := watch.SaveStagedRecord(root, setup.Record); err != nil {
		t.Fatal(err)
	}
	if err := watch.AppendHandled(root, "acme", "widget", 7, setup.Record.DispatchedSHA); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget/pulls/7" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"head":{"sha":%q,"repo":{"clone_url":%q}}}`, f.currentHead(), watchCharCloneURL)
			return
		}
		if h := f.githubHandler(); h != nil {
			h(w, r)
			return
		}
		t.Errorf("unexpected GitHub request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	f.ghURL = srv.URL
	f.client = &github.APIClient{HTTPClient: srv.Client(), BaseURL: srv.URL}
	return f
}

// installSeams wraps the listing, the stop and the wake around the real ones
// to record events, and replaces the head fetch and the stage's capture.
// installWatchCharFakes' cleanup restores the fetch, capture and stop seams.
func (f *watchContinueFixture) installSeams(t *testing.T) {
	prevList, prevWake := listClaudeSessionsFunc, retaskWakeFunc
	t.Cleanup(func() { listClaudeSessionsFunc, retaskWakeFunc = prevList, prevWake })

	listClaudeSessionsFunc = func(ctx context.Context) ([]claudeSessionRow, error) {
		rows, err := realListClaudeSessions(ctx)
		state := "absent"
		if row, ok := findSessionRow(rows, watchCharSessionID); ok {
			state = "stopped"
			if row.running() {
				state = "running"
			}
		}
		f.event("list:" + state)
		return rows, err
	}
	stopSessionFunc = func(ctx context.Context, short string) error {
		f.event("stop:" + short)
		return realStopSession(ctx, short)
	}
	retaskWakeFunc = func(ctx context.Context, bin string, args []string, dir string, env []string) error {
		f.event("wake")
		f.mu.Lock()
		f.wakeEnvs = append(f.wakeEnvs, slices.Clone(env))
		f.mu.Unlock()
		return realRetaskWake(ctx, bin, args, dir, env)
	}
	fetchPRHeadFunc = func(_ context.Context, remoteURL, sha, checkoutDir, token string) error {
		f.event("fetch")
		f.mu.Lock()
		f.fakes.fetches = append(f.fakes.fetches, watchCharFetch{url: remoteURL, sha: sha, dir: checkoutDir, token: token})
		hook, err := f.fetchHook, f.fetchErr
		f.mu.Unlock()
		if hook != nil {
			hook()
		}
		return err
	}
	watchCapture = func(_ agentplan.SessionRecords, _, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
		f.mu.Lock()
		f.captures++
		f.mu.Unlock()
		return "", "", errors.New("capture miss")
	}
}

func (f *watchContinueFixture) event(e string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}

// eventLog returns the events recorded so far.
func (f *watchContinueFixture) eventLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.events)
}

func (f *watchContinueFixture) currentHead() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.head
}

// setHead moves the PR's head on the GitHub fake.
func (f *watchContinueFixture) setHead(sha string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.head = sha
}

func (f *watchContinueFixture) githubHandler() http.HandlerFunc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.github
}

// run continues the review with the record as a sweep would read it, and
// returns what the pass printed.
func (f *watchContinueFixture) run(t *testing.T) (stdout, stderr string, err error) {
	t.Helper()
	cmd, out, errOut := newWatchCharCmd()
	err = continueReview(cmd, f.root, f.root, "", f.client, watchCharPR, f.loadRecord(t), f.plan)
	return out.String(), errOut.String(), err
}

func (f *watchContinueFixture) loadRecord(t *testing.T) watch.StagedRecord {
	t.Helper()
	rec, err := watch.LoadStagedRecord(f.root, watchContinueHandle)
	if err != nil {
		t.Fatalf("LoadStagedRecord: %v", err)
	}
	return rec
}

// recordBytes is the staged record's file as it is on disk.
func (f *watchContinueFixture) recordBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root, ".niwa", "watch", watchContinueHandle+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// handledHead is the head the handled set records for the PR.
func (f *watchContinueFixture) handledHead(t *testing.T) string {
	t.Helper()
	handled, err := watch.LoadHandledSet(f.root)
	if err != nil {
		t.Fatal(err)
	}
	return handled[watch.HandledIdentity("acme", "widget", 7)]
}

func (f *watchContinueFixture) calls(t *testing.T) [][]string {
	t.Helper()
	calls, err := f.stub.Calls()
	if err != nil {
		t.Fatal(err)
	}
	return calls
}

// wantWake is the only argv a continuation may hand the binary.
func (f *watchContinueFixture) wantWake() []string {
	return []string{"--bg", "--resume", watchCharSessionID, "--", watch.BuildResumePrompt(watch.DefaultCloneRelDir, watch.DefaultDraftRelPath)}
}

// assertNothingDone checks no stop, fetch or wake happened and the staged
// record and handled set are as the fixture left them.
func (f *watchContinueFixture) assertNothingDone(t *testing.T, recBefore []byte) {
	t.Helper()
	if stops, rms, wakes := actionCount(f.calls(t)); stops+rms+wakes != 0 {
		t.Errorf("stub log holds %d stop, %d rm and %d wake calls, want none:\n%s", stops, rms, wakes, claudestub.Quote(f.calls(t)))
	}
	if slices.Contains(f.eventLog(), "fetch") {
		t.Errorf("the head was fetched: events %q", f.eventLog())
	}
	if got := f.recordBytes(t); string(got) != string(recBefore) {
		t.Errorf("the staged record changed:\n before %s\n after  %s", recBefore, got)
	}
	if got := f.handledHead(t); got != watchCharOldSHA {
		t.Errorf("handled head = %q, want the old head %q", got, watchCharOldSHA)
	}
}

// assertDeferLine requires exactly one stdout line deferring the PR's
// continuation in state, with no retask wording anywhere, and returns it.
func assertDeferLine(t *testing.T, out, state string) string {
	t.Helper()
	prefix := "niwa watch: deferring continuation of acme/widget#7: " + state + ": "
	var found []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "niwa watch: deferring continuation of") {
			found = append(found, line)
		}
	}
	if len(found) != 1 || !strings.HasPrefix(found[0], prefix) {
		t.Fatalf("defer lines %q, want one starting %q\nstdout:\n%s", found, prefix, out)
	}
	if strings.Contains(out, "retask") {
		t.Errorf("watch printed retask wording:\n%s", out)
	}
	return found[0]
}

// TestContinueReview_FlaglessWake: the continuation hands the binary exactly
// `--bg --resume <id> -- <prompt>`, from the instance, sandboxed or not, and
// launches nothing through dispatchLaunch. The wake's own environment drops
// what a dispatch drops: the workspace's unset names, an inherited reap key
// and dispatch identity.
func TestContinueReview_FlaglessWake(t *testing.T) {
	for _, sandbox := range []bool{false, true} {
		t.Run(fmt.Sprintf("sandbox=%v", sandbox), func(t *testing.T) {
			f := newWatchContinueFixture(t, sandbox, func(f *watchContinueFixture, _ *watchContinueSetup) {
				writeDispatchEnvConfig(t, f.root, `"PASS_MARK"`, `"DROP_MARK"`)
			})
			t.Setenv("PASS_MARK", "carried-value")
			t.Setenv("DROP_MARK", "dispatcher-value")
			t.Setenv(config.ReapKeyEnv, "inherited-fake-key")
			t.Setenv(config.DispatchIDEnv, "inherited-id")
			t.Setenv(config.WorkspaceEnv, "inherited-ws")

			out, _, err := f.run(t)
			if err != nil {
				t.Fatalf("continueReview: %v\nstdout:\n%s", err, out)
			}
			wakes := wakeCalls(f.calls(t))
			if len(wakes) != 1 || !slices.Equal(wakes[0], f.wantWake()) {
				t.Fatalf("wake calls = %q, want exactly [%q]", wakes, f.wantWake())
			}
			if dir, err := f.stub.LastWakeDir(); err != nil || dir != f.inst {
				t.Errorf("the wake ran in %q (err %v), want the instance %q", dir, err, f.inst)
			}
			if n := len(f.fakes.launches); n != 0 {
				t.Errorf("dispatchLaunch called %d times, want 0", n)
			}
			if len(f.wakeEnvs) != 1 {
				t.Fatalf("wake environments recorded = %d, want 1", len(f.wakeEnvs))
			}
			for _, name := range []string{"DROP_MARK", config.ReapKeyEnv, config.DispatchIDEnv, config.WorkspaceEnv} {
				if v, ok := envValue(t, f.wakeEnvs[0], name); ok {
					t.Errorf("the wake's environment carries %s=%q", name, v)
				}
			}
		})
	}
}

// TestContinueReview_KeepsSessionIDs: the continuation captures nothing, and
// the record after a delivered wake differs from the one before only in
// DispatchedSHA (the new head) and an empty continuation_halted. The handled
// set moves to the new head, and the same session's transcript got the prompt.
func TestContinueReview_KeepsSessionIDs(t *testing.T) {
	f := newWatchContinueFixture(t, true, nil)
	before := f.loadRecord(t)

	if out, _, err := f.run(t); err != nil {
		t.Fatalf("continueReview: %v\nstdout:\n%s", err, out)
	}
	if f.captures != 0 {
		t.Errorf("watchCapture called %d times, want 0", f.captures)
	}
	after := f.loadRecord(t)
	if after.SessionID != before.SessionID || after.ShortID != before.ShortID {
		t.Errorf("ids = (%q, %q), want the staged (%q, %q)", after.SessionID, after.ShortID, before.SessionID, before.ShortID)
	}
	if after.SettingsDigest != before.SettingsDigest {
		t.Errorf("settings digest changed:\n before %s\n after  %s", before.SettingsDigest, after.SettingsDigest)
	}
	want := before
	want.DispatchedSHA = watchCharHeadSHA
	want.ContinuationHalted = ""
	if !reflect.DeepEqual(after, want) {
		t.Errorf("record = %+v\nwant %+v", after, want)
	}
	if got := f.handledHead(t); got != watchCharHeadSHA {
		t.Errorf("handled head = %q, want %q", got, watchCharHeadSHA)
	}
	prompts, err := claudestub.TranscriptPrompts(sessionattach.TranscriptPath(f.home, f.inst, watchCharSessionID))
	if err != nil {
		t.Fatal(err)
	}
	// The stub records the prompt through a shell command substitution,
	// which drops its trailing newline; the wake argv above is exact.
	if want := []string{watchContinueStagePrompt, strings.TrimRight(f.wantWake()[4], "\n")}; !slices.Equal(prompts, want) {
		t.Errorf("the session's transcript holds %q, want %q", prompts, want)
	}
}

// TestContinueReview_FetchRunsAfterSettle: the head fetch into the clone comes
// after the stop and after a listing that shows the review session's row with
// no pid and no status, and before the wake. When the stop never settles,
// nothing is fetched and nothing woken.
func TestContinueReview_FetchRunsAfterSettle(t *testing.T) {
	t.Run("settled", func(t *testing.T) {
		f := newWatchContinueFixture(t, false, nil)
		if out, _, err := f.run(t); err != nil {
			t.Fatalf("continueReview: %v\nstdout:\n%s", err, out)
		}
		ev := f.eventLog()
		stop := slices.Index(ev, "stop:"+watchCharShortID)
		fetch := slices.Index(ev, "fetch")
		wake := slices.Index(ev, "wake")
		if stop < 0 || fetch < 0 || wake < 0 || !(stop < fetch && fetch < wake) {
			t.Fatalf("events %q, want a stop, then a fetch, then a wake", ev)
		}
		if !slices.Contains(ev[stop+1:fetch], "list:stopped") {
			t.Errorf("no listing between the stop and the fetch showed the row settled: events %q", ev)
		}
		if n := len(f.fakes.fetches); n != 1 {
			t.Fatalf("fetches = %d, want 1", n)
		}
		got := f.fakes.fetches[0]
		if got.sha != watchCharHeadSHA || got.dir != filepath.Join(f.inst, "pr-clone") || got.url != watchCharCloneURL {
			t.Errorf("fetch = %+v, want head %s into %s from %s", got, watchCharHeadSHA, filepath.Join(f.inst, "pr-clone"), watchCharCloneURL)
		}
	})

	t.Run("settle_timeout", func(t *testing.T) {
		f := newWatchContinueFixture(t, false, nil)
		if err := f.stub.SetKnob("SETTLE_LISTINGS", "1000"); err != nil {
			t.Fatal(err)
		}
		shortenRetaskBounds(t, 100*time.Millisecond, 2*time.Second)
		before := f.recordBytes(t)

		out, _, err := f.run(t)
		if err != nil {
			t.Fatalf("continueReview: %v", err)
		}
		assertDeferLine(t, out, retaskSettleTimeout)
		ev := f.eventLog()
		if slices.Contains(ev, "fetch") || slices.Contains(ev, "wake") {
			t.Errorf("events %q, want no fetch and no wake after a settle timeout", ev)
		}
		if wakes := wakeCalls(f.calls(t)); len(wakes) != 0 {
			t.Errorf("wake calls = %q, want none", wakes)
		}
		if got := f.recordBytes(t); string(got) != string(before) {
			t.Errorf("the staged record changed after a settle timeout:\n%s", got)
		}
	})
}

// TestContinueReview_LivenessMismatchDefers: the stub lists the session and
// classifies it idle, but its job is rooted outside the instance, so the
// cross-check in PreStop fails: no stop, no fetch, no wake, record unchanged,
// one defer line, no pass error.
func TestContinueReview_LivenessMismatchDefers(t *testing.T) {
	f := newWatchContinueFixture(t, false, func(_ *watchContinueFixture, s *watchContinueSetup) {
		s.Session.Cwd = canonicalTempDir(t)
	})
	before := f.recordBytes(t)

	out, _, err := f.run(t)
	if err != nil {
		t.Fatalf("continueReview: %v", err)
	}
	assertDeferLine(t, out, watchDeferLiveness)
	f.assertNothingDone(t, before)
}

// TestContinueReview_LockHeldDefers: the core takes the instance lock before
// anything else, and watch takes no lock of its own.
func TestContinueReview_LockHeldDefers(t *testing.T) {
	t.Run("held", func(t *testing.T) {
		f := newWatchContinueFixture(t, false, nil)
		release, err := workspace.TryFileLock(workspace.InstanceLockPath(f.home, f.inst), "someone")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		before := f.recordBytes(t)

		out, _, err := f.run(t)
		if err != nil {
			t.Fatalf("continueReview: %v", err)
		}
		line := assertDeferLine(t, out, retaskConcurrency)
		if !strings.Contains(line, "held by someone") {
			t.Errorf("defer line %q doesn't name the holder", line)
		}
		f.assertNothingDone(t, before)
		if calls := f.calls(t); len(calls) != 0 {
			t.Errorf("the stub was called with the lock held:\n%s", claudestub.Quote(calls))
		}
	})

	// The record writes after the wake happen while the core still holds the
	// lock, so no other niwa command can act on the instance in between.
	t.Run("write_under_lock", func(t *testing.T) {
		f := newWatchContinueFixture(t, false, nil)
		var probes []error
		prev := saveStagedRecordFunc
		saveStagedRecordFunc = func(root string, rec watch.StagedRecord) error {
			rel, perr := workspace.TryFileLock(workspace.InstanceLockPath(f.home, f.inst), "probe")
			if rel != nil {
				rel()
			}
			probes = append(probes, perr)
			return prev(root, rec)
		}
		t.Cleanup(func() { saveStagedRecordFunc = prev })

		if out, _, err := f.run(t); err != nil {
			t.Fatalf("continueReview: %v\nstdout:\n%s", err, out)
		}
		if len(probes) != 2 {
			t.Fatalf("record writes = %d, want 2 (the marker and the outcome)", len(probes))
		}
		for i, perr := range probes {
			if !errors.Is(perr, workspace.ErrLockHeld) || !strings.Contains(perr.Error(), "niwa watch (pid") {
				t.Errorf("write %d: lock probe = %v, want held by niwa watch (pid N)", i, perr)
			}
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		f := newWatchContinueFixture(t, false, nil)
		prev := tryInstanceLockFunc
		tryInstanceLockFunc = func(string, string) (func(), error) { return nil, workspace.ErrLockUnsupported }
		t.Cleanup(func() { tryInstanceLockFunc = prev })
		before := f.recordBytes(t)

		out, _, err := f.run(t)
		if err != nil {
			t.Fatalf("continueReview: %v", err)
		}
		line := assertDeferLine(t, out, retaskConcurrency)
		if !strings.Contains(line, "file locking") {
			t.Errorf("defer line %q doesn't name the missing file locking", line)
		}
		f.assertNothingDone(t, before)
	})
}

// TestContinueReview_BuildBelowFloorDefers: a Claude Code build older than the
// core's floor defers before anything is stopped.
func TestContinueReview_BuildBelowFloorDefers(t *testing.T) {
	f := newWatchContinueFixture(t, false, nil)
	if err := f.stub.SetKnob("VERSION", "2.1.300"); err != nil {
		t.Fatal(err)
	}
	before := f.recordBytes(t)

	out, _, err := f.run(t)
	if err != nil {
		t.Fatalf("continueReview: %v", err)
	}
	assertDeferLine(t, out, retaskBuildForks)
	f.assertNothingDone(t, before)
}

// TestWatchDeferReasonsCoverCoreStates: every core state word watch can defer
// on has watch's own text, none of it retask's; the halt words have none,
// since they fail the pass instead.
func TestWatchDeferReasonsCoverCoreStates(t *testing.T) {
	for _, w := range retaskStateWords {
		reason, ok := watchCoreDeferReasons[w]
		if watchHaltStates[w] {
			if ok {
				t.Errorf("halt word %s has a defer reason", w)
			}
			continue
		}
		if !ok || reason == "" {
			t.Errorf("core state word %s has no watch defer reason", w)
		}
		if strings.Contains(strings.ToLower(reason), "retask") {
			t.Errorf("the defer reason for %s speaks of retask: %q", w, reason)
		}
	}
	for w := range watchCoreDeferReasons {
		if !slices.Contains(retaskStateWords, w) {
			t.Errorf("defer reason for %s, which is no core state word", w)
		}
	}
	for _, w := range []string{retaskCopyStarted, retaskCopyLeft, retaskWakeUnconfirmed} {
		if !watchHaltStates[w] {
			t.Errorf("%s is not a halt word", w)
		}
	}
}
