package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/testutil/claudestub"
	"github.com/tsukumogami/niwa/internal/watch"
)

// The pass tests run whole `niwa watch --once` passes (runWatchOnce) over the
// continuation fixture (watch_continue_test.go): the GitHub fake answers the
// login, the review-requested search, the pull lookup and the compare, the
// workspace watches the acme org, and the copy-rule stub is `claude`. Each pass
// prunes, sweeps, decides and continues or stages exactly as production does;
// only provisioning, the head fetch, the stage's launch and capture, and
// instance destruction are fakes.

// The PR heads the pass tests push after the fixture's new head.
const (
	watchPassHeadB = "fedcba9876543210fedcba9876543210fedcba98"
	watchPassHeadC = "00112233445566778899aabbccddeeff00112233"
)

// watchPassFixture is a continuation fixture runWatchOnce can run passes on.
type watchPassFixture struct {
	*watchContinueFixture

	pmu sync.Mutex
	// compare is the status the compare endpoint reports: "behind" (the
	// dispatched head is an ancestor of the new one) unless a test sets
	// "diverged" for a force-push.
	compare string
	// provisions counts provisionInstanceFunc calls, one per fresh stage.
	provisions int
}

// newWatchPassFixture builds the continuation fixture and the rest of what a
// pass needs: a workspace config watching the acme org, a global config whose
// watch_sandbox matches sandbox (with the capability probe stubbed to pass when
// it is required), the GitHub fake as the API base with a token, a counted
// provision seam, and the workspace root as the working directory. The handled
// set holds the old head, as the fixture leaves it.
func newWatchPassFixture(t *testing.T, sandbox bool, mod func(*watchContinueFixture, *watchContinueSetup)) *watchPassFixture {
	t.Helper()
	p := &watchPassFixture{watchContinueFixture: newWatchContinueFixture(t, sandbox, mod), compare: "behind"}
	f := p.watchContinueFixture

	appendWorkspaceConfig(t, f.root, "\n[[sources]]\norg = \"acme\"\n")
	mode := "off"
	if sandbox {
		mode = "required"
		prev := sandboxCapabilityCheck
		sandboxCapabilityCheck = func(context.Context) error { return nil }
		t.Cleanup(func() { sandboxCapabilityCheck = prev })
	}
	writeTestFile(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "niwa", "config.toml"),
		fmt.Sprintf("[global]\nwatch_sandbox = %q\n", mode))

	t.Setenv("NIWA_GITHUB_API_URL", f.ghURL)
	t.Setenv("GITHUB_TOKEN", "test-token")
	f.mu.Lock()
	f.github = p.serveGitHub(t)
	f.mu.Unlock()

	prevProvision := provisionInstanceFunc
	provisionInstanceFunc = func(ctx context.Context, root, cwd, namePrefix, sep string, n int) (provisionResult, error) {
		p.provisions++
		return prevProvision(ctx, root, cwd, namePrefix, sep, n)
	}
	t.Cleanup(func() { provisionInstanceFunc = prevProvision })

	chdir(t, f.root)
	return p
}

// serveGitHub answers every request a pass makes besides the pull lookup,
// which the fixture serves with its mutable head.
func (p *watchPassFixture) serveGitHub(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/user":
			fmt.Fprint(w, `{"login":"reviewer"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/search/issues":
			repoURL := p.ghURL + "/repos/acme/widget"
			fmt.Fprintf(w, `{"total_count":1,"items":[{"number":7,"html_url":%q,"created_at":"2026-01-01T00:00:00Z","repository_url":%q,"pull_request":{"url":%q}}]}`,
				watchCharPRURL, repoURL, repoURL+"/pulls/7")
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/acme/widget/compare/"):
			p.pmu.Lock()
			status := p.compare
			p.pmu.Unlock()
			fmt.Fprintf(w, `{"status":%q}`, status)
		default:
			t.Errorf("unexpected GitHub request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}
}

func (p *watchPassFixture) setCompare(status string) {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	p.compare = status
}

// pass runs one `niwa watch --once` pass and returns what it printed.
func (p *watchPassFixture) pass(t *testing.T) (string, error) {
	t.Helper()
	cmd, out, _ := newWatchCharCmd()
	err := runWatchOnce(cmd, nil)
	return out.String(), err
}

// appendWorkspaceConfig appends body to the workspace config.
func appendWorkspaceConfig(t *testing.T, root, body string) {
	t.Helper()
	fh, err := os.OpenFile(workspaceConfigPath(root), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if _, err := fh.WriteString(body); err != nil {
		t.Fatal(err)
	}
}

// dismiss removes the review session's job entry the way a user would: its
// process goes away, then `claude rm <short>` through the stub.
func (f *watchContinueFixture) dismiss(t *testing.T) {
	t.Helper()
	if err := f.stub.SetAlive(watchCharShortID, false, ""); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(filepath.Join(f.stub.BinDir, "claude"), "rm", watchCharShortID).CombinedOutput(); err != nil {
		t.Fatalf("claude rm %s: %v\n%s", watchCharShortID, err, out)
	}
	if sessionLive(defaultJobsDir(), watchCharSessionID, timeNow()) {
		t.Fatal("the review session's job entry is still there after claude rm")
	}
}

// configureOnReap declares an on-reap command that logs each run, and returns
// the log.
func (f *watchContinueFixture) configureOnReap(t *testing.T) string {
	t.Helper()
	command, logPath := onReapLogCommand(t)
	appendOnReapConfig(t, f.root, fmt.Sprintf("command = %q", command))
	return logPath
}

// counts returns the stub's stop and wake calls so far.
func (f *watchContinueFixture) counts(t *testing.T) (stops, wakes int) {
	t.Helper()
	stops, _, wakes = actionCount(f.calls(t))
	return stops, wakes
}

// recordSaves wraps saveStagedRecordFunc to keep each record written, and
// fails the write numbered failAt (1-based; 0 never fails).
func recordSaves(t *testing.T, failAt int) *[]watch.StagedRecord {
	t.Helper()
	var saved []watch.StagedRecord
	prev := saveStagedRecordFunc
	saveStagedRecordFunc = func(root string, rec watch.StagedRecord) error {
		saved = append(saved, rec)
		if len(saved) == failAt {
			return errors.New("planted record write failure")
		}
		return prev(root, rec)
	}
	t.Cleanup(func() { saveStagedRecordFunc = prev })
	return &saved
}

// TestContinueReview_PendingMarker: PreWake writes continuation_halted naming
// the new head before the final re-read and the wake. A delivered wake clears
// it; a refusal after PreWake clears it and changes nothing else, so the next
// pass continues the review; a delivered wake whose record write fails leaves
// it set, so no later pass wakes the session again.
func TestContinueReview_PendingMarker(t *testing.T) {
	t.Run("delivered_clears", func(t *testing.T) {
		p := newWatchPassFixture(t, true, nil)
		saved := recordSaves(t, 0)

		out, err := p.pass(t)
		p.assertDelivered(t, out, err, 1, watchCharHeadSHA)
		if len(*saved) != 2 {
			t.Fatalf("record writes = %d, want 2 (the marker and the outcome)", len(*saved))
		}
		if got := (*saved)[0].ContinuationHalted; got != watchCharHeadSHA {
			t.Errorf("the first write's continuation_halted = %q, want the new head %q", got, watchCharHeadSHA)
		}
		if got := (*saved)[1].ContinuationHalted; got != "" {
			t.Errorf("the outcome write's continuation_halted = %q, want empty", got)
		}
	})

	t.Run("refusal_after_prewake_stays_continuable", func(t *testing.T) {
		p := newWatchPassFixture(t, true, nil)
		before := p.loadRecord(t)
		// The fetch runs inside PreWake, after the stop settled: the session
		// starting again there is what the core's final re-read finds.
		fired := false
		p.mu.Lock()
		p.fetchHook = func() {
			if fired {
				return
			}
			fired = true
			if err := p.stub.SetAlive(watchCharShortID, true, "idle"); err != nil {
				t.Error(err)
			}
		}
		p.mu.Unlock()

		out, err := p.pass(t)
		if err != nil {
			t.Fatalf("pass returned %v, want nil after a refusal\nstdout:\n%s", err, out)
		}
		if !fired {
			t.Fatal("the fetch never ran, so the refusal didn't come after PreWake")
		}
		assertDeferLine(t, out, retaskBusy)
		if wakes := wakeCalls(p.calls(t)); len(wakes) != 0 {
			t.Errorf("wake calls = %q, want none", wakes)
		}
		after := p.loadRecord(t)
		if !reflect.DeepEqual(after, before) {
			t.Errorf("record after the refusal = %+v\nwant it unchanged %+v", after, before)
		}
		if got := p.handledHead(t); got != watchCharOldSHA {
			t.Errorf("handled head = %q, want the old head %q", got, watchCharOldSHA)
		}
		if !recordContinuable(defaultJobsDir(), after, timeNow(), p.plan.sandbox) {
			t.Error("recordContinuable refused the record after a refusal before the wake")
		}

		out, err = p.pass(t)
		p.assertDelivered(t, out, err, 1, watchCharHeadSHA)
	})

	t.Run("postcommit_write_fails_stays_halted", func(t *testing.T) {
		p := newWatchPassFixture(t, true, nil)
		recordSaves(t, 2)

		out, err := p.pass(t)
		if err == nil {
			t.Fatalf("pass succeeded after the outcome write failed\nstdout:\n%s", out)
		}
		if !strings.Contains(err.Error(), "acme/widget#7") {
			t.Errorf("pass error %q doesn't name the PR", err)
		}
		if n := len(wakeCalls(p.calls(t))); n != 1 {
			t.Fatalf("wake calls = %d, want 1", n)
		}
		rec := p.loadRecord(t)
		if rec.ContinuationHalted != watchCharHeadSHA {
			t.Errorf("continuation_halted on disk = %q, want the new head %q", rec.ContinuationHalted, watchCharHeadSHA)
		}
		if recordContinuable(defaultJobsDir(), rec, timeNow(), p.plan.sandbox) {
			t.Error("recordContinuable accepted a record still halted")
		}

		// A further push: the review is live but halted, so the pass defers.
		p.setHead(watchPassHeadB)
		out, err = p.pass(t)
		if err != nil {
			t.Fatalf("pass after the halt returned %v\nstdout:\n%s", err, out)
		}
		if n := len(wakeCalls(p.calls(t))); n != 1 {
			t.Errorf("wake calls = %d, want still 1", n)
		}
		if p.provisions != 0 {
			t.Errorf("provision calls = %d, want 0", p.provisions)
		}
	})
}

// TestContinueReview_CopyOrUnconfirmedHalts: a wake that starts a copy, or
// that can't be confirmed, keeps continuation_halted at the new head, the ids
// and the handled set as they were, and fails the pass with an error naming
// the PR and the outcome word.
func TestContinueReview_CopyOrUnconfirmedHalts(t *testing.T) {
	cases := []struct {
		name, knob, value string
		words             []string
	}{
		{name: "copy", knob: "ALWAYS_COPY", value: "1", words: []string{retaskCopyStarted, retaskCopyLeft}},
		{name: "unconfirmed", knob: "WAKE", value: "dead", words: []string{retaskWakeUnconfirmed}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newWatchPassFixture(t, true, nil)
			if err := p.stub.SetKnob(tc.knob, tc.value); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = p.stub.ClearKnob(tc.knob) })
			before := p.loadRecord(t)

			out, err := p.pass(t)
			if err == nil {
				t.Fatalf("pass succeeded, want a halt\nstdout:\n%s", out)
			}
			msg := err.Error()
			if !strings.Contains(msg, "acme/widget#7") {
				t.Errorf("pass error doesn't name the PR: %s", msg)
			}
			if !slices.ContainsFunc(tc.words, func(w string) bool { return strings.Contains(msg, w) }) {
				t.Errorf("pass error names none of %q: %s", tc.words, msg)
			}
			if n := len(wakeCalls(p.calls(t))); n != 1 {
				t.Errorf("wake calls = %d, want 1", n)
			}
			rec := p.loadRecord(t)
			if rec.ContinuationHalted != watchCharHeadSHA {
				t.Errorf("continuation_halted = %q, want the new head %q", rec.ContinuationHalted, watchCharHeadSHA)
			}
			if rec.SessionID != before.SessionID || rec.ShortID != before.ShortID {
				t.Errorf("ids = (%q, %q), want the staged (%q, %q)", rec.SessionID, rec.ShortID, before.SessionID, before.ShortID)
			}
			if rec.DispatchedSHA != watchCharOldSHA {
				t.Errorf("DispatchedSHA = %q, want the old head %q", rec.DispatchedSHA, watchCharOldSHA)
			}
			if got := p.handledHead(t); got != watchCharOldSHA {
				t.Errorf("handled head = %q, want the old head %q", got, watchCharOldSHA)
			}
		})
	}
}

// newHaltedPassFixture is a pass fixture whose one pass halted the review: the
// wake ran but the session never showed running. Its job entry is still there.
// An on-reap command is declared; its log is returned.
func newHaltedPassFixture(t *testing.T) (*watchPassFixture, string) {
	t.Helper()
	p := newWatchPassFixture(t, false, nil)
	logPath := p.configureOnReap(t)
	if err := p.stub.SetKnob("WAKE", "dead"); err != nil {
		t.Fatal(err)
	}
	out, err := p.pass(t)
	if err == nil || !strings.Contains(err.Error(), retaskWakeUnconfirmed) {
		t.Fatalf("the halting pass returned %v, want %s\nstdout:\n%s", err, retaskWakeUnconfirmed, out)
	}
	if got := p.loadRecord(t).ContinuationHalted; got != watchCharHeadSHA {
		t.Fatalf("continuation_halted = %q after the halt, want %q", got, watchCharHeadSHA)
	}
	if !sessionLive(defaultJobsDir(), watchCharSessionID, timeNow()) {
		t.Fatal("the halted review's job entry is gone")
	}
	return p, logPath
}

// assertHaltedDeferred checks a pass over a halted record whose job entry is
// still there: no error, no stop or wake beyond the halting pass's, nothing
// provisioned, and the record kept as halted.
func (p *watchPassFixture) assertHaltedDeferred(t *testing.T, out string, err error, stopsBefore int) {
	t.Helper()
	if err != nil {
		t.Fatalf("pass returned %v, want nil while the halted review's job entry exists\nstdout:\n%s", err, out)
	}
	stops, wakes := p.counts(t)
	if stops != stopsBefore || wakes != 1 {
		t.Errorf("stops = %d, wakes = %d; want %d and 1, as the halting pass left them", stops, wakes, stopsBefore)
	}
	if p.provisions != 0 {
		t.Errorf("provision calls = %d, want 0 while the job entry exists", p.provisions)
	}
	if got := p.loadRecord(t).ContinuationHalted; got != watchCharHeadSHA {
		t.Errorf("continuation_halted = %q, want it kept at %q", got, watchCharHeadSHA)
	}
	if !strings.Contains(out, "its continuation is halted") {
		t.Errorf("the pass didn't say why it kept the review:\n%s", out)
	}
	if strings.Contains(out, "discarding staged review") {
		t.Errorf("the pass discarded the halted review:\n%s", out)
	}
}

// TestRunWatchOnce_HaltedRecordDefersUntilDismissed: after a halt, a pass with
// a head the handled set doesn't hold neither wakes nor stages while the
// review's job entry exists, even when the PR was force-pushed. Once the entry
// is removed, the next pass discards the record, runs the on-reap command with
// the record's session id and reap key, and stages a fresh review.
func TestRunWatchOnce_HaltedRecordDefersUntilDismissed(t *testing.T) {
	t.Run("moved_head", func(t *testing.T) {
		p, logPath := newHaltedPassFixture(t)
		stops, _ := p.counts(t)

		p.setHead(watchPassHeadC)
		out, err := p.pass(t)
		p.assertHaltedDeferred(t, out, err, stops)
		if runs := onReapRuns(t, logPath); len(runs) != 0 {
			t.Errorf("on-reap ran while the job entry exists: %q", runs)
		}

		p.dismiss(t)
		out, err = p.pass(t)
		if err != nil {
			t.Fatalf("pass after the dismissal returned %v\nstdout:\n%s", err, out)
		}
		if !strings.Contains(out, "discarding staged review for acme/widget#7") {
			t.Errorf("the pass didn't discard the dismissed review:\n%s", out)
		}
		want := watchCharSessionID + " " + watchContinueReapKey + " " + p.inst
		if runs := onReapRuns(t, logPath); len(runs) != 1 || runs[0] != want {
			t.Errorf("on-reap runs = %q, want one: %q", runs, want)
		}
		if p.provisions != 1 || len(p.fakes.launches) != 1 {
			t.Fatalf("provisions = %d, launches = %d; want a fresh stage (1 and 1)", p.provisions, len(p.fakes.launches))
		}
		if _, wakes := p.counts(t); wakes != 1 {
			t.Errorf("wakes = %d, want still 1: a fresh stage launches, it doesn't wake", wakes)
		}
		rec := p.loadRecord(t)
		if rec.InstancePath == p.inst || rec.ContinuationHalted != "" || rec.DispatchedSHA != watchPassHeadC {
			t.Errorf("record after the fresh stage = %+v, want a new instance at %s and no marker", rec, watchPassHeadC)
		}
		if got := p.handledHead(t); got != watchPassHeadC {
			t.Errorf("handled head = %q, want %q", got, watchPassHeadC)
		}
	})

	t.Run("force_push", func(t *testing.T) {
		p, logPath := newHaltedPassFixture(t)
		stops, _ := p.counts(t)

		// The halted record skips the freshness check: discarding it here
		// would stage a second review beside the job entry still there.
		p.setHead(watchPassHeadC)
		p.setCompare("diverged")
		out, err := p.pass(t)
		p.assertHaltedDeferred(t, out, err, stops)
		if runs := onReapRuns(t, logPath); len(runs) != 0 {
			t.Errorf("on-reap ran while the job entry exists: %q", runs)
		}
		if n := len(*p.fakes.destroyed); n != 0 {
			t.Errorf("instances destroyed = %q, want none", *p.fakes.destroyed)
		}
	})
}

// TestPruneStagedRecords_OnReapAfterContinuation: a record continued once
// whose session is then removed is discarded, and the on-reap command runs
// with that record's session id and reap key.
func TestPruneStagedRecords_OnReapAfterContinuation(t *testing.T) {
	f := newWatchContinueFixture(t, false, nil)
	logPath := f.configureOnReap(t)

	out, _, err := f.run(t)
	f.assertDelivered(t, out, err, 1, watchCharHeadSHA)
	f.dismiss(t)

	var buf bytes.Buffer
	requested := map[string]bool{watch.HandledIdentity("acme", "widget", 7): true}
	pruneStagedRecords(t.Context(), &buf, f.root, f.client, requested)

	want := watchCharSessionID + " " + watchContinueReapKey + " " + f.inst
	if runs := onReapRuns(t, logPath); len(runs) != 1 || runs[0] != want {
		t.Errorf("on-reap runs = %q, want one: %q\nprune output:\n%s", runs, want, buf.String())
	}
	if handles, err := watch.ListStagedHandles(f.root); err != nil || len(handles) != 0 {
		t.Errorf("staged records left = %v (err %v), want none", handles, err)
	}
	if got := *f.fakes.destroyed; !slices.Equal(got, []string{f.inst}) {
		t.Errorf("destroyed = %q, want the review instance", got)
	}
}

// TestLiveStagedSessions_SavedOptionsMismatchDefers: options saved with the
// review that the checker refuses keep the sweep from planning it continuable,
// so Decide gives Defer and the pass stops nothing and wakes nothing. A
// mismatch that persists never stops the review pass after pass.
func TestLiveStagedSessions_SavedOptionsMismatchDefers(t *testing.T) {
	p := newWatchPassFixture(t, false, func(f *watchContinueFixture, s *watchContinueSetup) {
		launch := watchReviewLaunch(f.inst, watchContinueHandle, "p", false, watchLaunchEnv(f.root, s.Record.ReapKey))
		s.Session.Respawn = append(slices.Clone(launch.Passthrough), "--permission-mode", "bypassPermissions")
	})
	before := p.recordBytes(t)
	id := watch.HandledIdentity("acme", "widget", 7)

	live, continuable, err := liveStagedSessions(p.root, p.plan.sandbox)
	if err != nil {
		t.Fatal(err)
	}
	if !live[id] {
		t.Fatalf("the sweep doesn't see the review live: %v", live)
	}
	if _, ok := continuable[id]; ok {
		t.Fatal("the sweep planned the review continuable despite its saved options")
	}
	handled, err := watch.LoadHandledSet(p.root)
	if err != nil {
		t.Fatal(err)
	}
	plans := watch.Decide([]github.PRRef{watchCharPR}, watch.NewWorkspaceScope(nil, []string{"acme"}), handled,
		live, map[string]bool{}, map[string]string{id: watchCharHeadSHA}, watch.DefaultPerRunBound)
	if len(plans) != 1 || plans[0].Kind != watch.Defer {
		t.Fatalf("plans = %+v, want one Defer", plans)
	}

	for i := 0; i < 2; i++ {
		out, err := p.pass(t)
		if err != nil {
			t.Fatalf("pass %d returned %v\nstdout:\n%s", i+1, err, out)
		}
	}
	if stops, wakes := p.counts(t); stops != 0 || wakes != 0 {
		t.Errorf("stops = %d, wakes = %d; want none:\n%s", stops, wakes, claudestub.Quote(p.calls(t)))
	}
	if p.provisions != 0 {
		t.Errorf("provision calls = %d, want 0", p.provisions)
	}
	if got := p.recordBytes(t); string(got) != string(before) {
		t.Errorf("the staged record changed:\n before %s\n after  %s", before, got)
	}
	if got := p.handledHead(t); got != watchCharOldSHA {
		t.Errorf("handled head = %q, want the old head %q", got, watchCharOldSHA)
	}
}

// TestContinueReview_NextPassAfterGateRefusal: a gate refusal that comes after
// the stop leaves the review stopped with its job entry. The next pass, with
// the cause still there, stops nothing and wakes nothing; once the cause is
// gone, the sweep accepts the stopped review and the pass continues it with
// the flagless wake, without a second stop.
func TestContinueReview_NextPassAfterGateRefusal(t *testing.T) {
	// The digest cause edits settings.json; clearing it puts back the bytes
	// the stage left.
	var stagedSettings []byte
	settingsPath := func(p *watchPassFixture) string { return filepath.Join(p.inst, ".claude", "settings.json") }
	cases := []struct {
		name  string
		state string
		// plant puts the cause in place right after the stop; clear removes it.
		plant, clear func(t *testing.T, p *watchPassFixture)
	}{
		{
			name:  "digest",
			state: watchDeferSettingsDigest,
			plant: func(t *testing.T, p *watchPassFixture) {
				data, err := os.ReadFile(settingsPath(p))
				if err != nil {
					t.Fatal(err)
				}
				stagedSettings = data
				editReviewSettings(t, p.inst, func(s map[string]any) { s["apiKeyHelper"] = "/bin/echo" })
			},
			clear: func(t *testing.T, p *watchPassFixture) {
				if err := os.WriteFile(settingsPath(p), stagedSettings, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "settings_local_json",
			state: watchDeferSettingsFile,
			plant: func(t *testing.T, p *watchPassFixture) {
				writeTestFile(t, filepath.Join(p.inst, ".claude", "settings.local.json"), "{}\n")
			},
			clear: func(t *testing.T, p *watchPassFixture) {
				if err := os.Remove(filepath.Join(p.inst, ".claude", "settings.local.json")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "saved_options",
			state: watchDeferSavedOptions,
			// set in the test body, which wraps readSavedOptionsFunc.
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newWatchPassFixture(t, true, nil)

			// The saved-options cause: once bad is set, the options read gain
			// a flag no review saves.
			var bad bool
			prevRead := readSavedOptionsFunc
			readSavedOptionsFunc = func(jobsDir, sid string) ([]string, error) {
				opts, err := prevRead(jobsDir, sid)
				if err == nil && bad {
					opts = append(slices.Clone(opts), "--dangerously-skip-permissions")
				}
				return opts, err
			}
			t.Cleanup(func() { readSavedOptionsFunc = prevRead })
			plant, clear := tc.plant, tc.clear
			if plant == nil {
				plant = func(*testing.T, *watchPassFixture) { bad = true }
				clear = func(*testing.T, *watchPassFixture) { bad = false }
			}

			planted := false
			prevStop := stopSessionFunc
			stopSessionFunc = func(ctx context.Context, short string) error {
				err := prevStop(ctx, short)
				if !planted {
					planted = true
					plant(t, p)
				}
				return err
			}
			t.Cleanup(func() { stopSessionFunc = prevStop })
			before := p.recordBytes(t)

			// Pass 1: the stop, then the gate refuses.
			out, err := p.pass(t)
			if err != nil {
				t.Fatalf("pass 1 returned %v\nstdout:\n%s", err, out)
			}
			assertDeferLine(t, out, tc.state)
			if stops, wakes := p.counts(t); stops != 1 || wakes != 0 {
				t.Fatalf("pass 1: stops = %d, wakes = %d; want 1 and 0", stops, wakes)
			}
			if !sessionLive(defaultJobsDir(), watchCharSessionID, timeNow()) {
				t.Fatal("the refused review lost its job entry")
			}

			// Pass 2: the cause is still there.
			out, err = p.pass(t)
			if err != nil {
				t.Fatalf("pass 2 returned %v\nstdout:\n%s", err, out)
			}
			if stops, wakes := p.counts(t); stops != 1 || wakes != 0 {
				t.Errorf("pass 2: stops = %d, wakes = %d; want still 1 and 0", stops, wakes)
			}
			if got := p.recordBytes(t); string(got) != string(before) {
				t.Errorf("the staged record changed on a refusal:\n before %s\n after  %s", before, got)
			}
			if got := p.handledHead(t); got != watchCharOldSHA {
				t.Errorf("handled head = %q, want the old head %q", got, watchCharOldSHA)
			}

			// Pass 3: the cause is gone, and the stopped review is continued.
			clear(t, p)
			if !recordContinuable(defaultJobsDir(), p.loadRecord(t), timeNow(), p.plan.sandbox) {
				t.Fatal("recordContinuable refused the stopped review once the cause was gone")
			}
			out, err = p.pass(t)
			p.assertDelivered(t, out, err, 1, watchCharHeadSHA)
			if stops, _ := p.counts(t); stops != 1 {
				t.Errorf("stops = %d, want still 1: the review was already stopped", stops)
			}
		})
	}
}

// TestContinueReview_ChainsAcrossTwoPushes: two pushes, a pass after each.
// Both passes wake the same session with the flagless wake, the record's ids
// never change, exactly one job entry is rooted at the review instance after
// each pass, and the handled set and DispatchedSHA follow the heads.
func TestContinueReview_ChainsAcrossTwoPushes(t *testing.T) {
	p := newWatchPassFixture(t, true, nil)
	staged := p.loadRecord(t)

	for i, head := range []string{watchCharHeadSHA, watchPassHeadB} {
		p.setHead(head)
		out, err := p.pass(t)
		p.assertDelivered(t, out, err, i+1, head)

		rec := p.loadRecord(t)
		if rec.SessionID != staged.SessionID || rec.ShortID != staged.ShortID {
			t.Errorf("pass %d: ids = (%q, %q), want the staged (%q, %q)", i+1, rec.SessionID, rec.ShortID, staged.SessionID, staged.ShortID)
		}
		rows, err := realListClaudeSessions(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		rooted := sessionsRootedAtInstance(rows, p.inst, "")
		if len(rooted) != 1 || rooted[0].SessionID != staged.SessionID {
			t.Errorf("pass %d: sessions rooted at the instance = %+v, want only the review session", i+1, rooted)
		}
		if p.provisions != 0 {
			t.Errorf("pass %d: provision calls = %d, want 0", i+1, p.provisions)
		}
	}
	if got := p.loadRecord(t).DispatchedSHA; got != watchPassHeadB {
		t.Errorf("DispatchedSHA = %q, want %q", got, watchPassHeadB)
	}
}
