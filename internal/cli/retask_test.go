package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/testutil/claudestub"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The retask tests run `niwa retask` against the resume tests' scratch
// workspace, with the copy-rule stub (internal/testutil/claudestub) first on
// PATH and Claude Code's listing read through the stub rather than a seam.
// The worker is the resume fixture's record, mapped under the short id
// retaskTestShort, live and idle unless a test says otherwise.

const (
	retaskTestShort    = "11111111"
	retaskTestOtherSID = "22222222-2222-4222-8222-222222222222"
	retaskTestPrompt   = "FOLLOW-UP"
)

type retaskFixture struct {
	*resumeFixture
	stub    *claudestub.Stub
	rec     workspace.LaunchRecord
	mapping workspace.SessionMapping
	// before holds the bytes of every file under the session store and the
	// launch store once the fixture is written.
	before map[string][]byte
}

// retaskSetup is what a test changes before the fixture is written. A nil
// Session starts the stub with no job entry for the worker.
type retaskSetup struct {
	Record  workspace.LaunchRecord
	Mapping workspace.SessionMapping
	Session *claudestub.Session
}

// recordRespawn is the saved options Claude Code keeps for a worker
// dispatched with rec.
func recordRespawn(rec workspace.LaunchRecord) []string {
	var out []string
	for _, f := range []struct{ flag, value string }{
		{"--permission-mode", rec.PermissionMode},
		{"--name", rec.SessionName},
		{"--model", rec.Model},
		{"--settings", rec.Settings},
	} {
		if f.value != "" {
			out = append(out, f.flag, f.value)
		}
	}
	return out
}

func newRetaskFixture(t *testing.T, mod func(*retaskFixture, *retaskSetup)) *retaskFixture {
	t.Helper()
	rf := &retaskFixture{resumeFixture: newResumeFixture(t, "")}
	f := rf.resumeFixture

	// The real listing, through the stub, replaces the resume fixture's seam;
	// its cleanup puts the original back.
	listClaudeSessionsFunc = realListClaudeSessions
	stub, err := claudestub.New(t.TempDir(), f.home)
	if err != nil {
		t.Fatal(err)
	}
	rf.stub = stub
	t.Setenv("PATH", stub.BinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, kv := range stub.Env() {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	shortenRetaskBounds(t, 5*time.Second, 5*time.Second)

	rec := f.record()
	setup := &retaskSetup{
		Record: rec,
		Mapping: workspace.SessionMapping{
			SessionID:    resumeTestSessionID,
			InstanceName: resumeTestInstance,
			InstancePath: f.instancePath(),
			Agent:        string(agent.AgentClaude),
			Ephemeral:    true,
			Origin:       workspace.MappingOriginDispatch,
			SessionName:  resumeTestName,
			DispatchID:   resumeTestDispatchID,
			Handle:       retaskTestShort,
			Created:      time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		},
		Session: &claudestub.Session{
			Short:     retaskTestShort,
			SessionID: resumeTestSessionID,
			Cwd:       f.instancePath(),
			Alive:     true,
			Status:    "idle",
		},
	}
	if mod != nil {
		mod(rf, setup)
	}
	rf.rec, rf.mapping = setup.Record, setup.Mapping
	f.writeRecord(t, setup.Record)
	f.writeTranscript(t, resumeTestSessionID)
	f.presentInstance(t)
	if err := workspace.WriteSessionMapping(f.root, setup.Mapping); err != nil {
		t.Fatal(err)
	}
	if s := setup.Session; s != nil {
		if s.Respawn == nil {
			s.Respawn = recordRespawn(setup.Record)
		}
		if err := stub.AddSession(*s); err != nil {
			t.Fatal(err)
		}
	}
	rf.before = rf.storeBytes(t)
	return rf
}

// shortenRetaskBounds makes the polls fast and sets the bounds for a test.
func shortenRetaskBounds(t *testing.T, settle, verify time.Duration) {
	t.Helper()
	prev := [4]time.Duration{retaskSettlePoll, retaskSettleBound, retaskVerifyPoll, retaskVerifyBound}
	retaskSettlePoll, retaskSettleBound = 5*time.Millisecond, settle
	retaskVerifyPoll, retaskVerifyBound = 5*time.Millisecond, verify
	t.Cleanup(func() {
		retaskSettlePoll, retaskSettleBound, retaskVerifyPoll, retaskVerifyBound = prev[0], prev[1], prev[2], prev[3]
	})
}

// storeBytes reads every file under the session store and the launch store.
func (rf *retaskFixture) storeBytes(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, dir := range []string{filepath.Join(rf.root, workspace.StateDir, "sessions"), workspace.LaunchDir(rf.root)} {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				t.Fatal(rerr)
			}
			out[p] = data
			return nil
		})
	}
	return out
}

// assertStoresUnchanged fails when any file under the two stores changed,
// appeared or went away.
func (rf *retaskFixture) assertStoresUnchanged(t *testing.T) {
	t.Helper()
	after := rf.storeBytes(t)
	if len(after) != len(rf.before) {
		t.Errorf("the stores hold %d files, want the %d they held before", len(after), len(rf.before))
	}
	for p, b := range rf.before {
		if !bytes.Equal(after[p], b) {
			t.Errorf("%s changed:\n before %s\n after  %s", p, b, after[p])
		}
	}
}

func (rf *retaskFixture) run(t *testing.T, target, prompt string) (string, string, error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetContext(context.Background())
	err := runRetask(cmd, []string{target, prompt})
	return outBuf.String(), errBuf.String(), err
}

func (rf *retaskFixture) stubCalls(t *testing.T) [][]string {
	t.Helper()
	calls, err := rf.stub.Calls()
	if err != nil {
		t.Fatal(err)
	}
	return calls
}

// resetLog empties the stub log, so a test can read one run's calls.
func (rf *retaskFixture) resetLog(t *testing.T) {
	t.Helper()
	if err := os.Remove(rf.stub.Log); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

// actionCount counts the calls that act on a session: stop, rm and a wake.
func actionCount(calls [][]string) (stops, rms, wakes int) {
	return claudestub.Count(calls, "stop"), claudestub.Count(calls, "rm"), claudestub.Count(calls, "--bg")
}

// assertRefusal checks a refusal's exact prefix and that it is non-nil.
func assertRefusal(t *testing.T, err error, target, state string) string {
	t.Helper()
	if err == nil {
		t.Fatalf("retask succeeded; want a %s refusal", state)
	}
	want := `niwa: error: cannot retask "` + target + `": ` + state + `:`
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error %q does not start with %q", err, want)
	}
	return err.Error()
}

// assertNoAction checks the stub log holds no stop, rm or wake.
func (rf *retaskFixture) assertNoAction(t *testing.T) {
	t.Helper()
	calls := rf.stubCalls(t)
	if s, r, w := actionCount(calls); s+r+w != 0 {
		t.Errorf("stub log holds %d stop, %d rm and %d wake calls, want none:\n%s", s, r, w, claudestub.Quote(calls))
	}
}

func (rf *retaskFixture) transcriptPrompts(t *testing.T, sessionID string) []string {
	t.Helper()
	p := sessionattach.TranscriptPath(rf.home, rf.instancePath(), sessionID)
	got, err := claudestub.TranscriptPrompts(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return got
}

// wakeCalls returns the stub's wake calls.
func wakeCalls(calls [][]string) [][]string {
	var out [][]string
	for _, c := range calls {
		if claudestub.HasPrefix(c, "--bg") {
			out = append(out, c)
		}
	}
	return out
}

func TestRetaskStubModelsCopyRule(t *testing.T) {
	home := canonicalTempDir(t)
	t.Setenv("HOME", home)
	cwd := canonicalTempDir(t)
	const sid = "33333333-3333-4333-8333-333333333333"

	setup := func(t *testing.T, alive bool) *claudestub.Stub {
		stub, err := claudestub.New(t.TempDir(), home)
		if err != nil {
			t.Fatal(err)
		}
		if err := stub.AddSession(claudestub.Session{Short: "33333333", SessionID: sid, Cwd: cwd, Alive: alive, Status: "idle"}); err != nil {
			t.Fatal(err)
		}
		return stub
	}
	run := func(t *testing.T, stub *claudestub.Stub, args ...string) {
		t.Helper()
		cmd := exec.Command(filepath.Join(stub.BinDir, "claude"), args...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), stub.Env()...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("claude %q: %v\n%s", args, err, out)
		}
	}
	entries := func(t *testing.T, stub *claudestub.Stub) []string {
		t.Helper()
		got, err := stub.JobEntries()
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	t.Run("a resume carrying a flag copies", func(t *testing.T) {
		stub := setup(t, false)
		run(t, stub, "--bg", "--resume", sid, "--model", "haiku", "--", "p")
		if got := entries(t, stub); len(got) != 2 {
			t.Errorf("job entries %q, want the original and a copy", got)
		}
	})
	t.Run("a resume right after stop copies", func(t *testing.T) {
		stub := setup(t, true)
		run(t, stub, "stop", "33333333")
		run(t, stub, "--bg", "--resume", sid, "--", "p")
		if got := entries(t, stub); len(got) != 2 {
			t.Errorf("job entries %q, want the original and a copy", got)
		}
	})
	t.Run("a resume of a live row copies", func(t *testing.T) {
		stub := setup(t, true)
		run(t, stub, "--bg", "--resume", sid, "--", "p")
		if got := entries(t, stub); len(got) != 2 {
			t.Errorf("job entries %q, want the original and a copy", got)
		}
	})
	t.Run("a flagless resume after the stop settles continues the session", func(t *testing.T) {
		stub := setup(t, true)
		run(t, stub, "stop", "33333333")
		run(t, stub, "agents", "--json", "--all")
		run(t, stub, "agents", "--json", "--all")
		run(t, stub, "--bg", "--resume", sid, "--", "p")
		if got := entries(t, stub); len(got) != 1 || got[0] != "33333333" {
			t.Errorf("job entries %q, want only the original", got)
		}
		prompts, err := claudestub.TranscriptPrompts(sessionattach.TranscriptPath(home, cwd, sid))
		if err != nil {
			t.Fatal(err)
		}
		if len(prompts) != 1 || prompts[0] != "p" {
			t.Errorf("original transcript prompts %q, want [p]", prompts)
		}
	})
}

func TestRetaskCommandSurface(t *testing.T) {
	rf := newRetaskFixture(t, nil)
	found := false
	for _, c := range rootCmd.Commands() {
		if c == retaskCmd {
			found = true
		}
	}
	if !found {
		t.Fatal("retask is not registered on the root command")
	}
	if retaskCmd.Flags().HasAvailableFlags() {
		t.Errorf("retask defines flags; it takes none")
	}
	for _, args := range [][]string{
		{"retask", "a"},
		{"retask", "a", "b", "c"},
		{"retask", "a", "b", "--model", "haiku"},
	} {
		var out, errOut bytes.Buffer
		rootCmd.SetArgs(args)
		rootCmd.SetOut(&out)
		rootCmd.SetErr(&errOut)
		err := rootCmd.Execute()
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		if err == nil {
			t.Errorf("niwa %q succeeded; want a usage error", args)
		}
	}
	if calls := rf.stubCalls(t); len(calls) != 0 {
		t.Errorf("a usage error invoked claude:\n%s", claudestub.Quote(calls))
	}
}

func TestRetaskPromptChecks(t *testing.T) {
	rf := newRetaskFixture(t, nil)
	for name, prompt := range map[string]string{
		"whitespace":   " \t\n ",
		"one too long": strings.Repeat("a", maxArgStringBytes+1),
		"NUL":          "a\x00b",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := rf.run(t, resumeTestInstance, prompt)
			if err == nil {
				t.Fatal("retask accepted the prompt")
			}
			if !strings.HasPrefix(err.Error(), `niwa: error: cannot retask "`+resumeTestInstance+`":`) {
				t.Errorf("error %q is not a retask refusal", err)
			}
			if calls := rf.stubCalls(t); len(calls) != 0 {
				t.Errorf("claude was invoked:\n%s", claudestub.Quote(calls))
			}
		})
	}
}

func TestRetaskResolveTarget(t *testing.T) {
	// The handle differs from the session id's first eight characters here,
	// so a prefix of the session id is told apart from the short id.
	const handle = "a1b2c3d4"
	rf := newRetaskFixture(t, func(_ *retaskFixture, s *retaskSetup) {
		s.Mapping.Handle = handle
		s.Session.Short = handle
	})
	want, err := resolveRetaskTarget(rf.root, resumeTestInstance)
	if err != nil {
		t.Fatal(err)
	}
	if want.SessionID != resumeTestSessionID || want.ShortID != handle || want.InstancePath != rf.instancePath() {
		t.Fatalf("view = %+v", want)
	}
	for _, id := range []string{resumeTestInstance, rf.instancePath(), resumeTestSessionID, resumeTestName, handle, resumeTestDispatchID} {
		got, err := resolveRetaskTarget(rf.root, id)
		if err != nil {
			t.Errorf("resolving %q: %v", id, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("resolving %q gave %+v, want %+v", id, got, want)
		}
	}

	refuses := func(t *testing.T, target, state string) error {
		t.Helper()
		_, err := resolveRetaskTarget(rf.root, target)
		assertRefusal(t, err, target, state)
		return err
	}
	outside := filepath.Join(canonicalTempDir(t), resumeTestInstance)
	for _, target := range []string{
		filepath.Join(rf.instancePath(), "tools"),
		filepath.Join(rf.instancePath(), "tools", resumeTestInstance),
		outside,
		resumeTestInstance[:8],
		resumeTestSessionID[:8],
	} {
		refuses(t, target, retaskUnknownTarget)
	}

	t.Run("ambiguous session name", func(t *testing.T) {
		rf := newRetaskFixture(t, nil)
		other := rf.rec
		other.DispatchID = "9b1c2d3e-4f50-4a6b-8c7d-8e9fa0b1c2d3"
		other.SessionID = retaskTestOtherSID
		other.InstanceName = "rs+work-89abcdef"
		rf.writeRecord(t, other)
		_, err := resolveRetaskTarget(rf.root, resumeTestName)
		msg := assertRefusal(t, err, resumeTestName, retaskAmbiguousTarget)
		for _, id := range []string{resumeTestDispatchID, other.DispatchID} {
			if !strings.Contains(msg, id) {
				t.Errorf("refusal does not list dispatch %s: %s", id, msg)
			}
		}
	})
	t.Run("record failing its checks", func(t *testing.T) {
		rf := newRetaskFixture(t, func(_ *retaskFixture, s *retaskSetup) { s.Record.Model = "--dangerously-skip-permissions" })
		_, err := resolveRetaskTarget(rf.root, resumeTestInstance)
		assertRefusal(t, err, resumeTestInstance, retaskInvalidRecord)
	})
	for name, mod := range map[string]func(*workspace.SessionMapping){
		"unsafe handle":      func(m *workspace.SessionMapping) { m.Handle = "a;b" },
		"leading dash":       func(m *workspace.SessionMapping) { m.Handle = "-rf" },
		"invalid session id": func(m *workspace.SessionMapping) { m.SessionID = "../x" },
	} {
		t.Run("mapping with "+name, func(t *testing.T) {
			rf := newRetaskFixture(t, nil)
			m := rf.mapping
			mod(&m)
			writeRawMapping(t, rf.root, resumeTestSessionID, m)
			_, err := resolveRetaskTarget(rf.root, resumeTestInstance)
			assertRefusal(t, err, resumeTestInstance, retaskInvalidRecord)
		})
	}
}

// writeRawMapping writes m into the mapping file for sessionID, whatever m's
// own session id says.
func writeRawMapping(t *testing.T, root, sessionID string, m workspace.SessionMapping) {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, workspace.StateDir, "sessions", sessionID+".json")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRetaskStateWordsAreStable(t *testing.T) {
	want := []string{
		"unknown-target", "ambiguous-target", "invalid-record", "instance-gone",
		"instance-occupied", "transcript-gone", "job-entry-gone", "state-unknown",
		"awaiting-input", "busy", "policy-tightened", "options-drift",
		"stray-session", "concurrency", "agent-unsupported", "build-forks",
		"stop-failed", "settle-timeout", "copy-started", "copy-left",
		"wake-unconfirmed",
	}
	if !slices.Equal(retaskStateWords, want) {
		t.Errorf("retask's state words are\n %q\nwant\n %q\nThey are a stable contract: change this test and the guide together with them.", retaskStateWords, want)
	}
}

func TestRetaskStateWords(t *testing.T) {
	type check struct {
		name     string
		target   string
		mod      func(*retaskFixture, *retaskSetup)
		before   func(t *testing.T, rf *retaskFixture)
		state    string
		contains []string
		// acted says the wake ran; stopped that a stop ran.
		stopped, acted bool
	}
	cases := []check{
		{name: "unknown-target", target: "rs+other-89abcdef", state: retaskUnknownTarget},
		{
			name: "ambiguous-target", target: resumeTestName, state: retaskAmbiguousTarget,
			before: func(t *testing.T, rf *retaskFixture) {
				other := rf.rec
				other.DispatchID = "9b1c2d3e-4f50-4a6b-8c7d-8e9fa0b1c2d3"
				other.SessionID = retaskTestOtherSID
				other.InstanceName = "rs+work-89abcdef"
				rf.writeRecord(t, other)
				rf.before = rf.storeBytes(t)
			},
			contains: []string{resumeTestDispatchID, "9b1c2d3e-4f50-4a6b-8c7d-8e9fa0b1c2d3"},
		},
		{name: "invalid-record", state: retaskInvalidRecord, mod: func(_ *retaskFixture, s *retaskSetup) { s.Record.SessionName = "-x" }},
		{
			name: "instance-gone", state: retaskInstanceGone, contains: []string{"niwa resume"},
			before: func(t *testing.T, rf *retaskFixture) {
				if err := os.RemoveAll(rf.instancePath()); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "instance-occupied", state: retaskInstanceOccupied,
			before: func(t *testing.T, rf *retaskFixture) {
				if err := os.RemoveAll(rf.instancePath()); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(rf.instancePath(), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "transcript-gone", state: retaskTranscriptGone,
			before: func(t *testing.T, rf *retaskFixture) {
				if err := os.Remove(sessionattach.TranscriptPath(rf.home, rf.instancePath(), resumeTestSessionID)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "job-entry-gone", state: retaskJobEntryGone,
			mod:      func(_ *retaskFixture, s *retaskSetup) { s.Session = nil },
			contains: []string{"niwa resume " + resumeTestInstance + " <prompt>"},
		},
		{
			name: "state-unknown/row without job state", state: retaskStateUnknown,
			before: func(t *testing.T, rf *retaskFixture) {
				if err := os.Remove(filepath.Join(rf.home, ".claude", "jobs", retaskTestShort, "state.json")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "state-unknown/job state for another session", state: retaskStateUnknown,
			before: func(t *testing.T, rf *retaskFixture) {
				writeJob(t, rf, map[string]any{"sessionId": retaskTestOtherSID, "state": "done", "tempo": "idle", "respawnFlags": recordRespawn(rf.rec)})
			},
		},
		{
			name: "awaiting-input", state: retaskAwaitingInput, contains: []string{"claude attach " + retaskTestShort},
			before: func(t *testing.T, rf *retaskFixture) {
				writeJob(t, rf, map[string]any{"sessionId": resumeTestSessionID, "state": "done", "tempo": "idle", "needs": "an answer", "respawnFlags": recordRespawn(rf.rec)})
			},
		},
		{
			name: "busy", state: retaskBusy,
			mod: func(_ *retaskFixture, s *retaskSetup) {
				s.Session.Status, s.Session.State, s.Session.Tempo = "busy", "working", "active"
			},
		},
		{
			name: "busy/from the row alone", state: retaskBusy,
			mod: func(_ *retaskFixture, s *retaskSetup) {
				s.Session.Status, s.Session.State, s.Session.Tempo = "busy", "working", "idle"
				s.Session.InFlight = []string{"session_cron"}
			},
		},
		{
			name: "policy-tightened", state: retaskPolicyTightened, contains: []string{"bypassPermissions", "plan"},
			mod: func(rf *retaskFixture, s *retaskSetup) {
				s.Record.PermissionMode = "bypassPermissions"
				rf.setHostMode(t, "plan")
			},
		},
		{
			name: "options-drift", state: retaskOptionsDrift, contains: []string{"--model"},
			mod: func(_ *retaskFixture, s *retaskSetup) {
				s.Session.Respawn = recordRespawn(s.Record)
				s.Session.Respawn[5] = "haiku"
			},
		},
		{
			name: "stray-session", state: retaskStraySession, contains: []string{"22222222"},
			before: func(t *testing.T, rf *retaskFixture) {
				addStray(t, rf, filepath.Join(rf.instancePath(), "tools", "app"))
			},
		},
		{
			name: "agent-unsupported", state: retaskAgentUnsupported,
			mod: func(_ *retaskFixture, s *retaskSetup) { s.Record.Agent = string(agent.AgentCodex) },
		},
		{
			name: "stop-failed", state: retaskStopFailed, stopped: true,
			before: func(t *testing.T, rf *retaskFixture) { setKnob(t, rf, "STOP_FAIL", "1") },
		},
		{
			name: "settle-timeout", state: retaskSettleTimeout, stopped: true,
			before: func(t *testing.T, rf *retaskFixture) {
				setKnob(t, rf, "SETTLE_LISTINGS", "100000")
				shortenRetaskBounds(t, 60*time.Millisecond, 5*time.Second)
			},
		},
		{
			name: "copy-started", state: retaskCopyStarted, stopped: true, acted: true, contains: []string{"c0de0001"},
			before: func(t *testing.T, rf *retaskFixture) { setKnob(t, rf, "ALWAYS_COPY", "1") },
		},
		{
			name: "copy-left", state: retaskCopyLeft, stopped: true, acted: true,
			contains: []string{"\nclaude stop c0de0001\n", "\nclaude rm c0de0001\n"},
			before: func(t *testing.T, rf *retaskFixture) {
				setKnob(t, rf, "ALWAYS_COPY", "1")
				setKnob(t, rf, "RM_FAIL", "1")
			},
		},
		{
			name: "wake-unconfirmed/no process after the wake", state: retaskWakeUnconfirmed, acted: true,
			before: func(t *testing.T, rf *retaskFixture) {
				setKnob(t, rf, "WAKE", "dead")
				shortenRetaskBounds(t, 5*time.Second, 100*time.Millisecond)
			},
		},
		{
			name: "wake-unconfirmed/row gone after the wake", state: retaskWakeUnconfirmed, acted: true,
			before: func(t *testing.T, rf *retaskFixture) { setKnob(t, rf, "WAKE", "vanish") },
		},
	}

	for _, c := range cases {
		for _, keepAlive := range []bool{false, true} {
			if keepAlive && !c.stopped {
				continue
			}
			name := c.name
			if keepAlive {
				name += "/keep-alive"
			}
			t.Run(name, func(t *testing.T) {
				rf := newRetaskFixture(t, func(rf *retaskFixture, s *retaskSetup) {
					s.Mapping.KeepAlive = keepAlive
					if c.mod != nil {
						c.mod(rf, s)
					}
				})
				if c.before != nil {
					c.before(t, rf)
				}
				target := c.target
				if target == "" {
					target = resumeTestInstance
				}
				_, _, err := rf.run(t, target, retaskTestPrompt)
				msg := assertRefusal(t, err, target, c.state)
				for _, w := range c.contains {
					if !strings.Contains(msg, w) {
						t.Errorf("refusal lacks %q: %s", w, msg)
					}
				}
				rf.assertStoresUnchanged(t)

				calls := rf.stubCalls(t)
				stops, rms, wakes := actionCount(calls)
				if !c.stopped && !c.acted && stops+rms+wakes != 0 {
					t.Errorf("stub log holds %d stop, %d rm and %d wake calls, want none:\n%s", stops, rms, wakes, claudestub.Quote(calls))
				}
				if c.stopped && stops == 0 {
					t.Errorf("no stop in the stub log:\n%s", claudestub.Quote(calls))
				}
				if c.state == retaskSettleTimeout && wakes != 0 {
					t.Errorf("a wake ran although the stop never settled:\n%s", claudestub.Quote(calls))
				}

				if c.stopped {
					for _, w := range []string{"may have been left stopped", "resumable with another niwa retask"} {
						if !strings.Contains(msg, w) {
							t.Errorf("refusal lacks %q: %s", w, msg)
						}
					}
					if got := strings.Contains(msg, "keep-alive self-wake is silent until the next wake"); got != keepAlive {
						t.Errorf("keep-alive sentence present = %v, want %v: %s", got, keepAlive, msg)
					}
				}
				if c.acted {
					for _, w := range []string{"may already have been acted on", "niwa list", "claude logs " + retaskTestShort} {
						if !strings.Contains(msg, w) {
							t.Errorf("refusal lacks %q: %s", w, msg)
						}
					}
				}
				if c.state == retaskInstanceGone {
					if _, err := os.Lstat(rf.instancePath()); !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("retask created something at %s (%v)", rf.instancePath(), err)
					}
				}
				if c.state == retaskCopyStarted {
					if got, _ := rf.stub.JobEntries(); len(got) != 1 || got[0] != retaskTestShort {
						t.Errorf("job entries after the copy's removal = %q, want only %s", got, retaskTestShort)
					}
				}
			})
		}
	}

	t.Run("concurrency", func(t *testing.T) {
		rf := newRetaskFixture(t, nil)
		holdRealInstanceLock(t, rf.home, rf.instancePath(), "test-holder")
		start := time.Now()
		_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
		elapsed := time.Since(start)
		msg := assertRefusal(t, err, resumeTestInstance, retaskConcurrency)
		if !strings.Contains(msg, "test-holder") {
			t.Errorf("refusal doesn't name the holder: %s", msg)
		}
		if elapsed >= time.Second {
			t.Errorf("retask took %s to refuse a held lock", elapsed)
		}
		if calls := rf.stubCalls(t); len(calls) != 0 {
			t.Errorf("claude was invoked under a held lock:\n%s", claudestub.Quote(calls))
		}
		rf.assertStoresUnchanged(t)
	})
	t.Run("concurrency/no flock", func(t *testing.T) {
		rf := newRetaskFixture(t, nil)
		prev := tryInstanceLockFunc
		tryInstanceLockFunc = func(string, string) (func(), error) { return nil, workspace.ErrLockUnsupported }
		t.Cleanup(func() { tryInstanceLockFunc = prev })
		_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
		assertRefusal(t, err, resumeTestInstance, retaskConcurrency)
		if calls := rf.stubCalls(t); len(calls) != 0 {
			t.Errorf("claude was invoked without a lock:\n%s", claudestub.Quote(calls))
		}
	})
	for name, version := range map[string]string{"old build": "2.1.313", "unparseable build": "banana"} {
		t.Run("build-forks/"+name, func(t *testing.T) {
			rf := newRetaskFixture(t, nil)
			setKnob(t, rf, "VERSION", version)
			_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
			assertRefusal(t, err, resumeTestInstance, retaskBuildForks)
			calls := rf.stubCalls(t)
			if len(calls) != 1 || !claudestub.HasPrefix(calls[0], "--version") {
				t.Errorf("stub log = %s, want --version and nothing after it", claudestub.Quote(calls))
			}
		})
	}
}

// writeJob replaces the worker's job state.
func writeJob(t *testing.T, rf *retaskFixture, doc map[string]any) {
	t.Helper()
	if err := rf.stub.WriteJobState(retaskTestShort, doc); err != nil {
		t.Fatal(err)
	}
}

func setKnob(t *testing.T, rf *retaskFixture, name, value string) {
	t.Helper()
	if err := rf.stub.SetKnob(name, value); err != nil {
		t.Fatal(err)
	}
}

// addStray starts a live session the worker didn't dispatch, rooted at cwd.
func addStray(t *testing.T, rf *retaskFixture, cwd string) {
	t.Helper()
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rf.stub.AddSession(claudestub.Session{Short: "22222222", SessionID: retaskTestOtherSID, Cwd: cwd, Alive: true, Status: "idle"}); err != nil {
		t.Fatal(err)
	}
}

func TestRetaskLiveIdle(t *testing.T) {
	rf := newRetaskFixture(t, nil)
	stdout, stderr, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
	if err != nil {
		t.Fatalf("retask: %v\nstderr:\n%s", err, stderr)
	}
	calls := rf.stubCalls(t)
	stops, rms, wakes := actionCount(calls)
	if stops != 1 || wakes != 1 || rms != 0 {
		t.Fatalf("stub log holds %d stop, %d wake and %d rm calls, want 1, 1 and 0:\n%s", stops, wakes, rms, claudestub.Quote(calls))
	}
	stopAt, wakeAt := -1, -1
	for i, c := range calls {
		if claudestub.HasPrefix(c, "stop") {
			stopAt = i
			if !slices.Equal(c, []string{"stop", retaskTestShort}) {
				t.Errorf("stop argv = %q, want exactly stop %s", c, retaskTestShort)
			}
		}
		if claudestub.HasPrefix(c, "--bg") {
			wakeAt = i
		}
	}
	// The stub keeps the stopped row's pid and status for two listings, so
	// a third listing between the stop and the wake is one that showed the
	// row free of both.
	if n := claudestub.Count(calls[stopAt:wakeAt], "agents"); n < 3 {
		t.Errorf("%d listings between the stop and the wake, want at least 3 (the row free of pid and status before the wake):\n%s", n, claudestub.Quote(calls))
	}
	for _, w := range []string{
		resumeTestSessionID,
		rf.instancePath(),
		"claude attach " + retaskTestShort,
		"claude logs " + retaskTestShort,
		"claude stop " + retaskTestShort,
	} {
		if !strings.Contains(stdout, w) {
			t.Errorf("stdout lacks %q:\n%s", w, stdout)
		}
	}
	if got := rf.transcriptPrompts(t, resumeTestSessionID); len(got) != 1 || got[0] != retaskTestPrompt {
		t.Errorf("transcript prompts %q, want [%s]", got, retaskTestPrompt)
	}
}

func TestRetaskStoppedWithEntry(t *testing.T) {
	rf := newRetaskFixture(t, func(_ *retaskFixture, s *retaskSetup) { s.Session.Alive = false })
	if _, stderr, err := rf.run(t, resumeTestInstance, retaskTestPrompt); err != nil {
		t.Fatalf("retask: %v\nstderr:\n%s", err, stderr)
	}
	calls := rf.stubCalls(t)
	if stops, _, wakes := actionCount(calls); stops != 0 || wakes != 1 {
		t.Errorf("stub log holds %d stop and %d wake calls, want 0 and 1:\n%s", stops, wakes, claudestub.Quote(calls))
	}
}

func TestRetaskKeepAliveArmedIdle(t *testing.T) {
	armed := func(kinds ...string) func(*retaskFixture, *retaskSetup) {
		return func(_ *retaskFixture, s *retaskSetup) {
			s.Mapping.KeepAlive = true
			s.Session.State, s.Session.Tempo = "working", "idle"
			s.Session.InFlight = kinds
		}
	}
	rf := newRetaskFixture(t, armed("session_cron"))
	if _, stderr, err := rf.run(t, resumeTestInstance, retaskTestPrompt); err != nil {
		t.Fatalf("an armed idle worker was refused: %v\nstderr:\n%s", err, stderr)
	}

	rf = newRetaskFixture(t, armed("session_cron", "local_bash"))
	_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
	assertRefusal(t, err, resumeTestInstance, retaskBusy)
	rf.assertNoAction(t)
}

func TestRetaskStopMatchesFullID(t *testing.T) {
	rf := newRetaskFixture(t, func(_ *retaskFixture, s *retaskSetup) {
		s.Session.SessionID = retaskTestOtherSID
		s.Session.Cwd = filepath.Join(s.Mapping.InstancePath, "..", "elsewhere")
	})
	_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
	assertRefusal(t, err, resumeTestInstance, retaskStateUnknown)
	rf.assertNoAction(t)
}

func TestRetaskBounds(t *testing.T) {
	if retaskSettleInterval != 250*time.Millisecond || retaskSettleWindow != 15*time.Second {
		t.Errorf("settle polls every %s for up to %s, want 250ms and 15s", retaskSettleInterval, retaskSettleWindow)
	}
	if retaskVerifyInterval != 500*time.Millisecond || retaskVerifyWindow != 20*time.Second {
		t.Errorf("verify polls every %s for up to %s, want 500ms and 20s", retaskVerifyInterval, retaskVerifyWindow)
	}
}

func TestRetaskFinalReread(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(rf *retaskFixture, rows []claudeSessionRow) []claudeSessionRow
		state  string
	}{
		{"job entry removed", func(_ *retaskFixture, rows []claudeSessionRow) []claudeSessionRow {
			var out []claudeSessionRow
			for _, r := range rows {
				if r.SessionID != resumeTestSessionID {
					out = append(out, r)
				}
			}
			return out
		}, retaskJobEntryGone},
		{"stray rooted at the instance", func(rf *retaskFixture, rows []claudeSessionRow) []claudeSessionRow {
			return append(rows, claudeSessionRow{ID: "22222222", SessionID: retaskTestOtherSID, Cwd: rf.instancePath(), PID: 1, Status: "idle"})
		}, retaskStraySession},
		{"mapped row running again", func(_ *retaskFixture, rows []claudeSessionRow) []claudeSessionRow {
			for i := range rows {
				if rows[i].SessionID == resumeTestSessionID {
					rows[i].PID, rows[i].Status = 4242, "idle"
				}
			}
			return rows
		}, retaskBusy},
	} {
		t.Run(c.name, func(t *testing.T) {
			rf := newRetaskFixture(t, nil)
			settled := false
			listClaudeSessionsFunc = func(ctx context.Context) ([]claudeSessionRow, error) {
				rows, err := realListClaudeSessions(ctx)
				if err != nil || settled {
					if settled {
						rows = c.mutate(rf, rows)
					}
					return rows, err
				}
				if claudestub.Count(rf.stubCalls(t), "stop") > 0 {
					if r, ok := findSessionRow(rows, resumeTestSessionID); ok && !r.running() {
						settled = true
					}
				}
				return rows, err
			}
			_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
			assertRefusal(t, err, resumeTestInstance, c.state)
			stops, _, wakes := actionCount(rf.stubCalls(t))
			if stops != 1 || wakes != 0 {
				t.Errorf("stub log holds %d stop and %d wake calls, want 1 and 0", stops, wakes)
			}
		})
	}
}

func TestRetaskOptionsDrift(t *testing.T) {
	rf := newRetaskFixture(t, nil)
	base := recordRespawn(rf.rec)
	with := func(i int, v string) []string {
		out := slices.Clone(base)
		out[i] = v
		return out
	}
	without := func(flag string) []string {
		var out []string
		for i := 0; i < len(base); i += 2 {
			if base[i] != flag {
				out = append(out, base[i], base[i+1])
			}
		}
		return out
	}
	otherSettings := strings.Replace(rf.rec.Settings, "anchor-recorded", "anchor-changed", 1)
	cases := []struct {
		name string
		opts []string
		flag string
	}{
		{"permission mode", with(1, "plan"), "--permission-mode"},
		{"name", with(3, "other-name"), "--name"},
		{"model", with(5, "haiku"), "--model"},
		{"settings by one env value", with(7, otherSettings), "--settings"},
		{"recorded but not saved", without("--model"), "--model"},
		{"unknown flag with a value", append(slices.Clone(base), "--mcp-config", "x"), "--mcp-config"},
		{"unknown flag", append(slices.Clone(base), "--dangerously-skip-permissions"), "--dangerously-skip-permissions"},
		{"joined spelling", append(without("--model"), "--model=haiku"), "--model"},
		{"unrecognized option", append(slices.Clone(base), "--BAD;rm"), "an unrecognized option"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prev := readSavedOptionsFunc
			readSavedOptionsFunc = func(string, string) ([]string, error) { return c.opts, nil }
			t.Cleanup(func() { readSavedOptionsFunc = prev })
			rf.resetLog(t)
			_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
			msg := assertRefusal(t, err, resumeTestInstance, retaskOptionsDrift)
			if !strings.Contains(msg, c.flag) {
				t.Errorf("refusal doesn't name %s: %s", c.flag, msg)
			}
			if strings.Contains(msg, "BAD;rm") {
				t.Errorf("refusal echoes an unrecognized option: %s", msg)
			}
			rf.assertNoAction(t)
		})
	}

	// The joined spelling is read as the same option.
	joined := []string{"--permission-mode=default", "--name=" + resumeTestName, "--model=opus", "--settings=" + rf.rec.Settings}
	if flag, why := savedOptionsDrift(joined, rf.rec); flag != "" {
		t.Errorf("joined spellings drift at %s (%s)", flag, why)
	}
	// Saved, but the record holds none.
	noModel := rf.rec
	noModel.Model = ""
	if flag, _ := savedOptionsDrift(base, noModel); flag != "--model" {
		t.Errorf("a saved model the record lacks drifts at %q, want --model", flag)
	}
}

func TestRetaskAllowedSavedOptions(t *testing.T) {
	spec, ok := agentplan.For(agent.AgentClaude).LaunchSpec()
	if !ok {
		t.Fatal("no Claude launch spec")
	}
	prevAgent := dispatchAgent
	dispatchAgent = "reviewer"
	t.Cleanup(func() { dispatchAgent = prevAgent })

	argv := slices.Clone(spec.LeadingArgs)
	argv = append(argv, spec.DetachedArgs...)
	argv = append(argv, formatWorkdirGrant(spec, "/w")...)
	argv = append(argv, buildDispatchPassthrough(spec.Flags, "work-0123abcd", "opus", "default")...)
	doc, ok := renderLaunchSettings(map[string]any{
		config.RemoteControlAtStartupKey: true,
		config.CrossSessionInboundKey:    crossSessionInboundAccept,
		config.EnabledMcpjsonServersKey:  mcpApprovalList{names: []string{"docs"}},
		"env":                            map[string]string{config.ReapKeyEnv: "x"},
	})
	if !ok {
		t.Fatal("no settings document rendered")
	}
	argv = append(argv, spec.Flags.Settings, doc)
	argv = append(argv, "--resume", resumeTestSessionID)

	for _, a := range argv {
		if !strings.HasPrefix(a, "--") {
			continue
		}
		name, _, _ := strings.Cut(a, "=")
		if _, listed := retaskAllowedSavedOptions[name]; !listed && !slices.Contains(retaskCheckedOptions, name) {
			t.Errorf("a dispatch can save %s, which is neither checked nor listed in retaskAllowedSavedOptions", name)
		}
	}
	for name, takesValue := range retaskAllowedSavedOptions {
		opts := []string{name}
		if takesValue {
			opts = append(opts, "value")
		}
		if flag, why := savedOptionsDrift(opts, workspace.LaunchRecord{}); flag != "" {
			t.Errorf("listed flag %s alone drifts at %s (%s)", name, flag, why)
		}
	}
}

func TestRetaskNeverPrintsSettings(t *testing.T) {
	const sentinel = "SENTINEL-7f3a"
	settings := `{"env":{"NIWA_REAP_KEY":"` + sentinel + `"}}`
	mod := func(saved string) func(*retaskFixture, *retaskSetup) {
		return func(_ *retaskFixture, s *retaskSetup) {
			s.Record.Settings = settings
			s.Record.Env = nil
			s.Session.Respawn = recordRespawn(s.Record)
			s.Session.Respawn[7] = saved
		}
	}
	check := func(t *testing.T, stdout, stderr string, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("retask succeeded; want a refusal")
		}
		for name, s := range map[string]string{"stdout": stdout, "stderr": stderr, "error": err.Error()} {
			if strings.Contains(s, sentinel) {
				t.Errorf("%s carries the settings value: %s", name, s)
			}
		}
	}

	rf := newRetaskFixture(t, mod(`{"env":{"NIWA_REAP_KEY":"other"}}`))
	stdout, stderr, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
	check(t, stdout, stderr, err)
	if msg := assertRefusal(t, err, resumeTestInstance, retaskOptionsDrift); !strings.Contains(msg, "--settings") {
		t.Errorf("refusal doesn't name --settings: %s", msg)
	}

	for name, before := range map[string]func(*testing.T, *retaskFixture){
		"stray":       func(t *testing.T, rf *retaskFixture) { addStray(t, rf, rf.instancePath()) },
		"copy":        func(t *testing.T, rf *retaskFixture) { setKnob(t, rf, "ALWAYS_COPY", "1") },
		"unconfirmed": func(t *testing.T, rf *retaskFixture) { setKnob(t, rf, "WAKE", "vanish") },
	} {
		t.Run(name, func(t *testing.T) {
			rf := newRetaskFixture(t, mod(settings))
			before(t, rf)
			stdout, stderr, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
			check(t, stdout, stderr, err)
		})
	}
}

func TestRetaskWakeArgv(t *testing.T) {
	// Read before the fixture moves the working directory.
	src, err := os.ReadFile("retask_deliver.go")
	if err != nil {
		t.Fatal(err)
	}
	rf := newRetaskFixture(t, nil)
	if _, stderr, err := rf.run(t, resumeTestInstance, retaskTestPrompt); err != nil {
		t.Fatalf("retask: %v\nstderr:\n%s", err, stderr)
	}
	wakes := wakeCalls(rf.stubCalls(t))
	if len(wakes) != 1 {
		t.Fatalf("%d wakes, want 1", len(wakes))
	}
	want := []string{"--bg", "--resume", resumeTestSessionID, "--", retaskTestPrompt}
	if !slices.Equal(wakes[0], want) {
		t.Errorf("wake argv %q, want %q", wakes[0], want)
	}
	if dir, err := rf.stub.LastWakeDir(); err != nil || workspace.CanonicalPath(dir) != workspace.CanonicalPath(rf.instancePath()) {
		t.Errorf("the wake ran in %q (%v), want the instance %s", dir, err, rf.instancePath())
	}

	for _, banned := range []string{"watchLaunchRequest", "buildDispatchPassthrough"} {
		if bytes.Contains(src, []byte(banned)) {
			t.Errorf("retask_deliver.go names %s, a launch path that adds flags", banned)
		}
	}
	if !bytes.Contains(src, []byte("reentryResumeArgs(")) {
		t.Errorf("retask_deliver.go doesn't build the wake through reentryResumeArgs")
	}
}

func TestRetaskPromptByteIdentical(t *testing.T) {
	rf := newRetaskFixture(t, nil)
	_, pwnedErr := os.Lstat("/tmp/pwned")
	pwnedBefore := pwnedErr == nil
	for _, prompt := range []string{
		"--model haiku",
		`it's "quoted"`,
		"$(touch /tmp/pwned) `id`",
		"line one\nline two\nline three",
		"résumé 日本語 Ωmega",
	} {
		rf.resetLog(t)
		if _, stderr, err := rf.run(t, resumeTestInstance, prompt); err != nil {
			t.Fatalf("retask %q: %v\nstderr:\n%s", prompt, err, stderr)
		}
		wakes := wakeCalls(rf.stubCalls(t))
		if len(wakes) != 1 || wakes[0][len(wakes[0])-1] != prompt || len(wakes[0]) != 5 {
			t.Errorf("prompt %q reached the wake as %q", prompt, wakes)
		}
		got := rf.transcriptPrompts(t, resumeTestSessionID)
		if len(got) == 0 || got[len(got)-1] != prompt {
			t.Errorf("prompt %q reached the transcript as %q", prompt, got)
		}
	}
	if _, err := os.Lstat("/tmp/pwned"); err == nil && !pwnedBefore {
		t.Error("a prompt was run through a shell: /tmp/pwned exists")
	}
}

// testDeliverer is a deliverer whose Accepts a test decides.
type testDeliverer struct {
	accepts   func(workerState, string) bool
	delivered *int
}

func (d testDeliverer) Accepts(s workerState, kind string) bool { return d.accepts(s, kind) }

func (d testDeliverer) Deliver(context.Context, workerSnapshot, string) error {
	*d.delivered++
	return nil
}

func TestRetaskDelivererAcceptsNarrowsOnly(t *testing.T) {
	var delivered int
	install := func(t *testing.T, accepts func(workerState, string) bool) {
		prev := retaskDelivererFor
		retaskDelivererFor = func(agentplan.LaunchSpec) deliverer { return testDeliverer{accepts: accepts, delivered: &delivered} }
		t.Cleanup(func() { retaskDelivererFor = prev })
	}

	rf := newRetaskFixture(t, nil)
	install(t, func(s workerState, _ string) bool { return s != liveIdle })
	_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
	assertRefusal(t, err, resumeTestInstance, retaskAgentUnsupported)
	rf.assertNoAction(t)

	rf = newRetaskFixture(t, func(_ *retaskFixture, s *retaskSetup) {
		s.Session.Status, s.Session.State, s.Session.Tempo = "busy", "working", "active"
	})
	install(t, func(workerState, string) bool { return true })
	_, _, err = rf.run(t, resumeTestInstance, retaskTestPrompt)
	assertRefusal(t, err, resumeTestInstance, retaskBusy)
	if delivered != 0 {
		t.Errorf("the deliverer was called %d times for a worker the core refused", delivered)
	}
	rf.assertNoAction(t)
}

func TestRetaskWritesNothing(t *testing.T) {
	rf := newRetaskFixture(t, nil)
	if _, stderr, err := rf.run(t, resumeTestInstance, retaskTestPrompt); err != nil {
		t.Fatalf("retask: %v\nstderr:\n%s", err, stderr)
	}
	rf.assertStoresUnchanged(t)
	raw, err := os.ReadFile(filepath.Join(rf.root, workspace.StateDir, "sessions", resumeTestSessionID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("keep_alive")) {
		t.Errorf("the mapping gained a keep_alive key: %s", raw)
	}
	wakes := wakeCalls(rf.stubCalls(t))
	if len(wakes) != 1 || wakes[0][len(wakes[0])-1] != retaskTestPrompt {
		t.Errorf("the wake's prompt isn't the caller's: %q", wakes)
	}
}

// scriptedList answers the listing seam from a script of answers, repeating
// the last one.
func scriptedList(t *testing.T, answers ...[]claudeSessionRow) *int {
	t.Helper()
	calls := 0
	prev := listClaudeSessionsFunc
	listClaudeSessionsFunc = func(context.Context) ([]claudeSessionRow, error) {
		i := calls
		calls++
		if i >= len(answers) {
			i = len(answers) - 1
		}
		return answers[i], nil
	}
	t.Cleanup(func() { listClaudeSessionsFunc = prev })
	return &calls
}

func TestRetaskVerify(t *testing.T) {
	inst := canonicalTempDir(t)
	v := workerView{SessionID: resumeTestSessionID, ShortID: retaskTestShort, InstancePath: inst}
	mapped := func(running bool) claudeSessionRow {
		r := claudeSessionRow{ID: retaskTestShort, SessionID: resumeTestSessionID, Cwd: inst}
		if running {
			r.PID, r.Status = 4242, "idle"
		}
		return r
	}
	cp := claudeSessionRow{ID: "c0de0001", SessionID: "c0de0001-0001-4001-8001-000000000001", Cwd: inst, PID: 1, Status: "idle"}
	before := map[string]bool{resumeTestSessionID: true}

	shortenRetaskBounds(t, time.Second, 10*time.Second)
	t.Run("delivered at the first running poll", func(t *testing.T) {
		calls := scriptedList(t, []claudeSessionRow{mapped(false)}, []claudeSessionRow{mapped(true)})
		r := &retaskRun{ctx: context.Background(), v: v}
		if got, _ := r.verify(before); got != "" {
			t.Errorf("verify = %q, want delivered", got)
		}
		if *calls != 2 {
			t.Errorf("verify listed %d times, want 2", *calls)
		}
	})
	t.Run("a new rooted id is a copy even when the mapped row runs", func(t *testing.T) {
		scriptedList(t, []claudeSessionRow{mapped(true), cp})
		r := &retaskRun{ctx: context.Background(), v: v}
		got, copies := r.verify(before)
		if got != retaskCopyStarted || len(copies) != 1 || copies[0].ID != cp.ID {
			t.Errorf("verify = %q %v, want copy-started with %s", got, copies, cp.ID)
		}
	})
	t.Run("a vanished row is unconfirmed at once", func(t *testing.T) {
		scriptedList(t, []claudeSessionRow{})
		r := &retaskRun{ctx: context.Background(), v: v}
		start := time.Now()
		if got, _ := r.verify(before); got != retaskWakeUnconfirmed {
			t.Errorf("verify = %q, want wake-unconfirmed", got)
		}
		if time.Since(start) > time.Second {
			t.Errorf("verify waited %s for a vanished row", time.Since(start))
		}
	})
	t.Run("the deadline is unconfirmed", func(t *testing.T) {
		shortenRetaskBounds(t, time.Second, 50*time.Millisecond)
		scriptedList(t, []claudeSessionRow{mapped(false)})
		r := &retaskRun{ctx: context.Background(), v: v}
		if got, _ := r.verify(before); got != retaskWakeUnconfirmed {
			t.Errorf("verify = %q, want wake-unconfirmed", got)
		}
	})
}

func TestRetaskCopyRemovalConditions(t *testing.T) {
	const copySID = "c0de0001-0001-4001-8001-000000000001"
	type cond struct {
		name   string
		copy   func(inst string) claudeSessionRow
		fresh  func(t *testing.T, inst string, cp claudeSessionRow) []claudeSessionRow
		before func(cp claudeSessionRow) map[string]bool
	}
	live := func(inst string) claudeSessionRow {
		return claudeSessionRow{ID: "c0de0001", SessionID: copySID, Cwd: inst, PID: 1, Status: "idle"}
	}
	same := func(_ *testing.T, _ string, cp claudeSessionRow) []claudeSessionRow { return []claudeSessionRow{cp} }
	conds := []cond{
		{name: "unsafe short id", copy: func(inst string) claudeSessionRow { c := live(inst); c.ID = "-rf"; return c }, fresh: same},
		{name: "invalid session id", copy: func(inst string) claudeSessionRow { c := live(inst); c.SessionID = "c0de0001;x"; return c }, fresh: same},
		{name: "missing from a fresh listing", copy: live, fresh: func(*testing.T, string, claudeSessionRow) []claudeSessionRow { return nil }},
		{name: "cwd in a sibling", copy: live, fresh: func(_ *testing.T, inst string, cp claudeSessionRow) []claudeSessionRow {
			cp.Cwd = inst + "x"
			return []claudeSessionRow{cp}
		}},
		{name: "cwd through a symlink out of the instance", copy: live, fresh: func(t *testing.T, inst string, cp claudeSessionRow) []claudeSessionRow {
			link := filepath.Join(inst, "out")
			if err := os.Symlink(canonicalTempDir(t), link); err != nil {
				t.Fatal(err)
			}
			cp.Cwd = link
			return []claudeSessionRow{cp}
		}},
		{name: "the mapped id", copy: func(inst string) claudeSessionRow { c := live(inst); c.SessionID = resumeTestSessionID; return c }, fresh: same},
		{name: "rooted before the wake", copy: live, fresh: same, before: func(cp claudeSessionRow) map[string]bool {
			return map[string]bool{resumeTestSessionID: true, cp.SessionID: true}
		}},
	}
	for _, c := range conds {
		t.Run(c.name, func(t *testing.T) {
			rf := newRetaskFixture(t, nil)
			inst := rf.instancePath()
			cp := c.copy(inst)
			scriptedList(t, c.fresh(t, inst, cp))
			before := map[string]bool{resumeTestSessionID: true}
			if c.before != nil {
				before = c.before(cp)
			}
			r := &retaskRun{ctx: context.Background(), v: workerView{SessionID: resumeTestSessionID, ShortID: retaskTestShort, InstancePath: inst}, target: resumeTestInstance}
			_, err := r.handleCopies([]claudeSessionRow{cp}, before)
			msg := assertRefusal(t, err, resumeTestInstance, retaskCopyLeft)
			if !strings.Contains(msg, "\nclaude stop ") || !strings.Contains(msg, "\nclaude rm ") {
				t.Errorf("copy-left lacks the manual commands: %s", msg)
			}
			if n := claudestub.Count(rf.stubCalls(t), "rm"); n != 0 {
				t.Errorf("claude rm ran %d times on a copy failing a removal condition", n)
			}
		})
	}

	t.Run("all five hold", func(t *testing.T) {
		rf := newRetaskFixture(t, nil)
		setKnob(t, rf, "ALWAYS_COPY", "1")
		_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
		assertRefusal(t, err, resumeTestInstance, retaskCopyStarted)
		calls := rf.stubCalls(t)
		wake := slices.IndexFunc(calls, func(c []string) bool { return claudestub.HasPrefix(c, "--bg") })
		stop := slices.IndexFunc(calls, func(c []string) bool { return slices.Equal(c, []string{"stop", "c0de0001"}) })
		rm := slices.IndexFunc(calls, func(c []string) bool { return slices.Equal(c, []string{"rm", "c0de0001"}) })
		if wake < 0 || stop < wake || rm < stop {
			t.Fatalf("want the wake, then stop c0de0001, then rm c0de0001:\n%s", claudestub.Quote(calls))
		}
		if claudestub.Count(calls[stop:rm], "agents") < 1 {
			t.Errorf("no settle listing between the copy's stop and its removal:\n%s", claudestub.Quote(calls))
		}
		if claudestub.Count(calls[rm:], "agents") < 1 {
			t.Errorf("no listing after the copy's removal:\n%s", claudestub.Quote(calls))
		}
		if got, _ := rf.stub.JobEntries(); len(got) != 1 {
			t.Errorf("job entries %q, want the worker's only", got)
		}
	})
}

func TestRetaskStrayNeverRemoved(t *testing.T) {
	rf := newRetaskFixture(t, nil)
	addStray(t, rf, filepath.Join(rf.instancePath(), "tools"))
	_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
	if msg := assertRefusal(t, err, resumeTestInstance, retaskStraySession); !strings.Contains(msg, "22222222") {
		t.Errorf("refusal doesn't name the stray: %s", msg)
	}
	rf.assertNoAction(t)
	if got, _ := rf.stub.JobEntries(); !slices.Contains(got, "22222222") {
		t.Errorf("the stray's job entry is gone: %q", got)
	}
}

func TestRetaskHostileIDs(t *testing.T) {
	hostile := []string{"a;rm -rf x", "-rf", "a/b", strings.Repeat("a", 200), "$(id)"}
	assertNoHostileArgv := func(t *testing.T, rf *retaskFixture, bad string) {
		t.Helper()
		rf.assertNoAction(t)
		for _, c := range rf.stubCalls(t) {
			if slices.Contains(c, bad) {
				t.Errorf("an argv carries the hostile id %q: %q", bad, c)
			}
		}
	}
	for _, bad := range hostile {
		t.Run("listing row "+bad[:min(len(bad), 8)], func(t *testing.T) {
			rf := newRetaskFixture(t, nil)
			listClaudeSessionsFunc = func(ctx context.Context) ([]claudeSessionRow, error) {
				return []claudeSessionRow{{ID: bad, SessionID: resumeTestSessionID, Cwd: rf.instancePath(), PID: 1, Status: "idle"}}, nil
			}
			_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
			assertRefusal(t, err, resumeTestInstance, retaskStateUnknown)
			assertNoHostileArgv(t, rf, bad)
		})
		t.Run("rooted row "+bad[:min(len(bad), 8)], func(t *testing.T) {
			rf := newRetaskFixture(t, nil)
			listClaudeSessionsFunc = func(ctx context.Context) ([]claudeSessionRow, error) {
				rows, err := realListClaudeSessions(ctx)
				return append(rows, claudeSessionRow{ID: bad, SessionID: bad, Cwd: rf.instancePath(), PID: 1, Status: "idle"}), err
			}
			_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
			msg := assertRefusal(t, err, resumeTestInstance, retaskStraySession)
			if strings.Contains(msg, bad) {
				t.Errorf("refusal prints the hostile id: %s", msg)
			}
			assertNoHostileArgv(t, rf, bad)
		})
		t.Run("job state "+bad[:min(len(bad), 8)], func(t *testing.T) {
			rf := newRetaskFixture(t, nil)
			writeJob(t, rf, map[string]any{"sessionId": bad, "state": "done", "tempo": "idle", "respawnFlags": recordRespawn(rf.rec)})
			_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
			assertRefusal(t, err, resumeTestInstance, retaskStateUnknown)
			assertNoHostileArgv(t, rf, bad)
		})
		t.Run("mapping handle "+bad[:min(len(bad), 8)], func(t *testing.T) {
			rf := newRetaskFixture(t, nil)
			m := rf.mapping
			m.Handle = bad
			writeRawMapping(t, rf.root, resumeTestSessionID, m)
			_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
			assertRefusal(t, err, resumeTestInstance, retaskInvalidRecord)
			assertNoHostileArgv(t, rf, bad)
		})
		t.Run("mapping session id "+bad[:min(len(bad), 8)], func(t *testing.T) {
			rf := newRetaskFixture(t, nil)
			m := rf.mapping
			m.SessionID = bad
			writeRawMapping(t, rf.root, resumeTestSessionID, m)
			_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
			assertRefusal(t, err, resumeTestInstance, retaskInvalidRecord)
			assertNoHostileArgv(t, rf, bad)
		})
	}
}

func TestRetaskCoreWatchCaller(t *testing.T) {
	watchView := func(rf *retaskFixture) workerView {
		spec, _ := agentplan.For(agent.AgentClaude).LaunchSpec()
		return workerView{
			SessionID:    resumeTestSessionID,
			ShortID:      retaskTestShort,
			InstancePath: rf.instancePath(),
			Staged:       &watch.StagedRecord{SessionID: resumeTestSessionID, ShortID: retaskTestShort, InstancePath: rf.instancePath()},
			Root:         rf.root,
			Spec:         spec,
		}
	}
	// mark records a hook with what the stub log held when it ran.
	type mark struct {
		hook         string
		stops, wakes int
		result       retaskResult
		options      []string
	}

	t.Run("hook order", func(t *testing.T) {
		rf := newRetaskFixture(t, nil)
		var marks []mark
		at := func(hook string, s workerSnapshot, r retaskResult) {
			stops, _, wakes := actionCount(rf.stubCalls(t))
			marks = append(marks, mark{hook: hook, stops: stops, wakes: wakes, result: r, options: s.Options})
		}
		res, err := retaskCore(context.Background(), watchView(rf), retaskCaller{
			Kind:       retaskCallerWatch,
			Prompt:     retaskTestPrompt,
			PreStop:    func(_ context.Context, s workerSnapshot) error { at("PreStop", s, retaskResult{}); return nil },
			PreWake:    func(_ context.Context, s workerSnapshot) error { at("PreWake", s, retaskResult{}); return nil },
			PostCommit: func(_ context.Context, s workerSnapshot, r retaskResult) error { at("PostCommit", s, r); return nil },
		})
		if err != nil || res.Outcome != retaskDelivered || !res.Stopped {
			t.Fatalf("core = %+v, %v; want delivered after a stop", res, err)
		}
		want := []mark{{hook: "PreStop"}, {hook: "PreWake", stops: 1}, {hook: "PostCommit", stops: 1, wakes: 1}}
		if len(marks) != 3 {
			t.Fatalf("hooks ran %+v, want PreStop, PreWake, PostCommit", marks)
		}
		for i, w := range want {
			if marks[i].hook != w.hook || marks[i].stops != w.stops || marks[i].wakes != w.wakes {
				t.Errorf("hook %d = %+v, want %+v", i, marks[i], w)
			}
		}
		if marks[2].result.Outcome != retaskDelivered {
			t.Errorf("PostCommit saw %+v, want delivered", marks[2].result)
		}
		if len(marks[1].options) == 0 {
			t.Errorf("PreWake saw no saved options")
		}
	})

	t.Run("a refusal after PreWake tells PostCommit", func(t *testing.T) {
		rf := newRetaskFixture(t, nil)
		posts := 0
		var got retaskResult
		_, err := retaskCore(context.Background(), watchView(rf), retaskCaller{
			Kind:   retaskCallerWatch,
			Prompt: retaskTestPrompt,
			PreWake: func(context.Context, workerSnapshot) error {
				addStray(t, rf, rf.instancePath())
				return nil
			},
			PostCommit: func(_ context.Context, _ workerSnapshot, r retaskResult) error { posts++; got = r; return nil },
		})
		assertRefusal(t, err, resumeTestSessionID, retaskStraySession)
		if posts != 1 || got.Outcome != retaskRefused || got.State != retaskStraySession {
			t.Errorf("PostCommit ran %d times with %+v, want once with a refused stray-session result", posts, got)
		}
		if _, _, wakes := actionCount(rf.stubCalls(t)); wakes != 0 {
			t.Errorf("a wake ran after the final re-read refused")
		}
	})

	t.Run("PreWake sees saved options read after the stop", func(t *testing.T) {
		rf := newRetaskFixture(t, nil)
		prev := readSavedOptionsFunc
		readSavedOptionsFunc = func(string, string) ([]string, error) {
			if claudestub.Count(rf.stubCalls(t), "stop") > 0 {
				return []string{"--after-stop"}, nil
			}
			return []string{"--before-stop"}, nil
		}
		t.Cleanup(func() { readSavedOptionsFunc = prev })
		var seen []string
		_, err := retaskCore(context.Background(), watchView(rf), retaskCaller{
			Kind:    retaskCallerWatch,
			Prompt:  retaskTestPrompt,
			PreWake: func(_ context.Context, s workerSnapshot) error { seen = s.Options; return nil },
		})
		if err != nil {
			t.Fatalf("core: %v", err)
		}
		if !slices.Equal(seen, []string{"--after-stop"}) {
			t.Errorf("PreWake saw %q, want the options read after the stop", seen)
		}
	})

	t.Run("build-forks is a result watch can map", func(t *testing.T) {
		rf := newRetaskFixture(t, nil)
		setKnob(t, rf, "VERSION", "2.1.300")
		res, err := retaskCore(context.Background(), watchView(rf), retaskCaller{Kind: retaskCallerWatch, Prompt: retaskTestPrompt})
		if err == nil || res.State != retaskBuildForks || res.Outcome != retaskRefused {
			t.Errorf("core = %+v, %v; want a refused build-forks result", res, err)
		}
	})
}
