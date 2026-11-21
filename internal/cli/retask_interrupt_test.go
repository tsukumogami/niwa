package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/testutil/claudestub"
)

// The --interrupt tests: a worker in the middle of a turn is refused as busy
// unless the caller passes --interrupt, which stops the turn and wakes the
// session in place under the same checks as an idle worker. The check after
// the wake compares the woken session's saved options with the launch record.

// runInterrupt runs retask the way `niwa retask --interrupt` does.
func (rf *retaskFixture) runInterrupt(t *testing.T, target, prompt string) (string, string, error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.Flags().Bool(retaskInterruptFlag, false, "")
	if err := cmd.Flags().Set(retaskInterruptFlag, "true"); err != nil {
		t.Fatal(err)
	}
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetContext(context.Background())
	err := runRetask(cmd, []string{target, prompt})
	return outBuf.String(), errBuf.String(), err
}

// busyWorker makes the fixture's worker live and in the middle of a turn.
func busyWorker(_ *retaskFixture, s *retaskSetup) {
	s.Session.Status, s.Session.State, s.Session.Tempo = "busy", "working", "active"
}

// savedOptions reads the options the stub saved for a session's job.
func (rf *retaskFixture) savedOptions(t *testing.T, short string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(rf.home, ".claude", "jobs", short, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var js struct {
		RespawnFlags []string `json:"respawnFlags"`
	}
	if err := json.Unmarshal(data, &js); err != nil {
		t.Fatal(err)
	}
	return js.RespawnFlags
}

// rowRunning reports whether the stub lists the session with a live process.
func (rf *retaskFixture) rowRunning(t *testing.T, sessionID string) (listed, running bool) {
	t.Helper()
	rows, err := realListClaudeSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	row, ok := findSessionRow(rows, sessionID)
	return ok, ok && row.running()
}

func TestRetaskBusyNeedsInterrupt(t *testing.T) {
	rf := newRetaskFixture(t, busyWorker)
	_, _, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
	msg := assertRefusal(t, err, resumeTestInstance, retaskBusy)
	if !strings.Contains(msg, "niwa retask --interrupt "+resumeTestInstance) {
		t.Errorf("the busy refusal doesn't name --interrupt: %s", msg)
	}
	rf.assertNoAction(t)
}

func TestRetaskInterruptBusy(t *testing.T) {
	for _, c := range []struct {
		name string
		mod  func(*retaskFixture, *retaskSetup)
	}{
		{"busy row and job state", busyWorker},
		{"busy row alone", func(_ *retaskFixture, s *retaskSetup) {
			s.Session.Status, s.Session.State, s.Session.Tempo = "busy", "working", "idle"
			s.Session.InFlight = []string{"session_cron"}
		}},
		{"busy job state alone", func(_ *retaskFixture, s *retaskSetup) {
			s.Session.State, s.Session.Tempo = "working", "active"
			s.Session.InFlight = []string{"local_bash"}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			rf := newRetaskFixture(t, c.mod)
			stdout, stderr, err := rf.runInterrupt(t, resumeTestInstance, retaskTestPrompt)
			if err != nil {
				t.Fatalf("retask --interrupt: %v\nstderr:\n%s", err, stderr)
			}
			calls := rf.stubCalls(t)
			stops, rms, wakes := actionCount(calls)
			if stops != 1 || wakes != 1 || rms != 0 {
				t.Fatalf("stub log holds %d stop, %d wake and %d rm calls, want 1, 1 and 0:\n%s", stops, wakes, rms, claudestub.Quote(calls))
			}
			// The wake waits for the stop to settle, as it does for an idle
			// worker: the stub keeps the stopped row's pid for two listings.
			stopAt, wakeAt := -1, -1
			for i, cl := range calls {
				switch {
				case claudestub.HasPrefix(cl, "stop"):
					stopAt = i
				case claudestub.HasPrefix(cl, "--bg"):
					wakeAt = i
					want := []string{"--bg", "--resume", resumeTestSessionID, "--", retaskTestPrompt}
					if !slices.Equal(cl, want) {
						t.Errorf("wake argv = %q, want %q", cl, want)
					}
				}
			}
			if n := claudestub.Count(calls[stopAt:wakeAt], "agents"); n < 3 {
				t.Errorf("%d listings between the stop and the wake, want at least 3:\n%s", n, claudestub.Quote(calls))
			}
			if !strings.Contains(stdout, resumeTestSessionID) {
				t.Errorf("stdout lacks the session id:\n%s", stdout)
			}
			if got := rf.transcriptPrompts(t, resumeTestSessionID); len(got) != 1 || got[0] != retaskTestPrompt {
				t.Errorf("transcript prompts %q, want [%s]", got, retaskTestPrompt)
			}
			if got, want := rf.savedOptions(t, retaskTestShort), recordRespawn(rf.rec); !slices.Equal(got, want) {
				t.Errorf("saved options after the wake = %q, want the launch record's %q", got, want)
			}
			rf.assertStoresUnchanged(t)
		})
	}
}

func TestRetaskInterruptStillRefusesAwaitingInput(t *testing.T) {
	rf := newRetaskFixture(t, nil)
	writeJob(t, rf, map[string]any{"sessionId": resumeTestSessionID, "state": "working", "tempo": "active", "needs": "an answer", "respawnFlags": recordRespawn(rf.rec)})
	rf.before = rf.storeBytes(t)
	_, _, err := rf.runInterrupt(t, resumeTestInstance, retaskTestPrompt)
	assertRefusal(t, err, resumeTestInstance, retaskAwaitingInput)
	rf.assertNoAction(t)
}

func TestRetaskInterruptStillChecksOptionsFirst(t *testing.T) {
	rf := newRetaskFixture(t, func(rf *retaskFixture, s *retaskSetup) {
		busyWorker(rf, s)
		s.Session.Respawn = []string{"--model", "haiku"}
	})
	_, _, err := rf.runInterrupt(t, resumeTestInstance, retaskTestPrompt)
	assertRefusal(t, err, resumeTestInstance, retaskOptionsDrift)
	rf.assertNoAction(t)
}

func TestRetaskInterruptBusyWithoutProcess(t *testing.T) {
	rf := newRetaskFixture(t, func(_ *retaskFixture, s *retaskSetup) {
		s.Session.Alive = false
		s.Session.State, s.Session.Tempo = "working", "active"
		s.Session.InFlight = []string{"local_bash"}
	})
	_, _, err := rf.runInterrupt(t, resumeTestInstance, retaskTestPrompt)
	assertRefusal(t, err, resumeTestInstance, retaskStateUnknown)
	rf.assertNoAction(t)
}

func TestRetaskInterruptCopyRemoved(t *testing.T) {
	rf := newRetaskFixture(t, busyWorker)
	if err := rf.stub.SetKnob("ALWAYS_COPY", "1"); err != nil {
		t.Fatal(err)
	}
	_, _, err := rf.runInterrupt(t, resumeTestInstance, retaskTestPrompt)
	assertRefusal(t, err, resumeTestInstance, retaskCopyStarted)
	stops, rms, wakes := actionCount(rf.stubCalls(t))
	if stops != 2 || rms != 1 || wakes != 1 {
		t.Errorf("stub log holds %d stop, %d rm and %d wake calls, want 2 (worker, copy), 1 and 1", stops, rms, wakes)
	}
	rows, err := realListClaudeSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SessionID != resumeTestSessionID {
		t.Errorf("listing after the copy's removal = %+v, want only the worker", rows)
	}
}

func TestRetaskWokeWithDrift(t *testing.T) {
	for _, c := range []struct {
		name      string
		mod       func(*retaskFixture, *retaskSetup)
		interrupt bool
		// respawn builds what the wake leaves as saved options.
		respawn func(base []string) []string
		flag    string
	}{
		{"idle worker, model replaced", nil, false, func(b []string) []string {
			out := slices.Clone(b)
			out[slices.Index(out, "--model")+1] = "fable"
			return out
		}, "--model"},
		{"interrupted worker, model replaced", busyWorker, true, func(b []string) []string {
			out := slices.Clone(b)
			out[slices.Index(out, "--model")+1] = "fable"
			return out
		}, "--model"},
		{"idle worker, only a default model left", nil, false, func([]string) []string {
			return []string{"--model", "fable"}
		}, "--permission-mode"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rf := newRetaskFixture(t, c.mod)
			after, err := json.Marshal(c.respawn(recordRespawn(rf.rec)))
			if err != nil {
				t.Fatal(err)
			}
			if err := rf.stub.SetKnob("WAKE_RESPAWN", string(after)); err != nil {
				t.Fatal(err)
			}
			run := rf.run
			if c.interrupt {
				run = rf.runInterrupt
			}
			_, _, err = run(t, resumeTestInstance, retaskTestPrompt)
			msg := assertRefusal(t, err, resumeTestInstance, retaskWokeWithDrift)
			if !strings.Contains(msg, c.flag) {
				t.Errorf("refusal doesn't name %s: %s", c.flag, msg)
			}
			if strings.Contains(msg, "fable") {
				t.Errorf("refusal prints a saved value: %s", msg)
			}
			// The woken session is the worker's own: stopped again, kept,
			// never removed.
			stops, rms, wakes := actionCount(rf.stubCalls(t))
			if wakes != 1 || rms != 0 {
				t.Errorf("stub log holds %d wake and %d rm calls, want 1 and 0", wakes, rms)
			}
			// One stop before the wake (the worker is live, idle or busy),
			// one after it for the drift.
			if stops != 2 {
				t.Errorf("stub log holds %d stop calls, want 2", stops)
			}
			listed, running := rf.rowRunning(t, resumeTestSessionID)
			if !listed || running {
				t.Errorf("after the refusal the worker is listed=%v running=%v, want listed and stopped", listed, running)
			}
		})
	}
}

func TestRetaskWokeWithDriftNotCheckedWhenOptionsHold(t *testing.T) {
	rf := newRetaskFixture(t, nil)
	// A wake that rewrites the saved options with the same values, as Claude
	// Code does when it writes back the model it resolved, passes.
	same, err := json.Marshal(recordRespawn(rf.rec))
	if err != nil {
		t.Fatal(err)
	}
	if err := rf.stub.SetKnob("WAKE_RESPAWN", string(same)); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := rf.run(t, resumeTestInstance, retaskTestPrompt); err != nil {
		t.Fatalf("retask: %v\nstderr:\n%s", err, stderr)
	}
	if listed, running := rf.rowRunning(t, resumeTestSessionID); !listed || !running {
		t.Errorf("the woken worker is listed=%v running=%v, want running", listed, running)
	}
}

func TestRetaskDelivererBusyCLIOnly(t *testing.T) {
	d := resumeWakeDeliverer{}
	if !d.Accepts(liveBusy, retaskCallerCLI) {
		t.Error("the wake deliverer refuses a live busy worker for the CLI")
	}
	if d.Accepts(liveBusy, retaskCallerWatch) {
		t.Error("the wake deliverer accepts a live busy worker for watch, which never interrupts a turn")
	}
	if !liveBusy.stopsFirst() || !liveIdle.stopsFirst() || stoppedWithEntry.stopsFirst() {
		t.Error("stopsFirst must hold for live idle and live busy only")
	}
}
