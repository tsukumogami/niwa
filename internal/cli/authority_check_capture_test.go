package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/lineage"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The before-mapping scenarios: a dispatch has written the instance's record
// and its lineage entry and launched the worker, and has not written the
// worker's mapping yet. Records are Claude job-state files under a test HOME.

// captureWorkspace is boundWorkspace without the mapping: showDispatchA's
// record grants merge-pr, and its lineage entry names the instance with no
// session id yet.
func captureWorkspace(t *testing.T) (*showWorkspace, string, string) {
	t.Helper()
	w := newShowWorkspace(t, "")
	inst := w.instance(t, "ws-1")
	writeShowRecord(t, inst, showRecord(showDispatchA, authority.Autonomous, authority.LayerWorkspace, authority.MergePR))
	writeLineage(t, w.root, showDispatchA, "ws-1", "")
	home := t.TempDir()
	prev := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = prev })
	return w, inst, home
}

func writeLineage(t *testing.T, root, dispatchID, instance, sessionID string) {
	t.Helper()
	e := lineage.Entry{
		DispatchID:     dispatchID,
		Instance:       instance,
		Agent:          "claude",
		Origin:         lineage.OriginDispatch,
		WrittenAt:      "2026-10-24T05:00:00Z",
		ParentSession:  lineage.Link{Source: lineage.SourceNone},
		ParentDispatch: lineage.Link{Source: lineage.SourceNone},
		Principal:      lineage.Principal{Source: lineage.SourceNone, Trust: lineage.TrustObserved},
	}
	if err := workspace.CreateLineageEntry(root, e); err != nil {
		t.Fatal(err)
	}
	if sessionID != "" {
		if err := workspace.CompleteLineageEntry(root, dispatchID, sessionID); err != nil {
			t.Fatal(err)
		}
	}
}

// writePending opens the capture window for dispatchID, naming pid and a
// launch time.
func writePending(t *testing.T, inst, dispatchID string, pid int, launched time.Time) {
	t.Helper()
	if err := workspace.WriteCapturePending(inst, workspace.CapturePending{DispatchID: dispatchID, Agent: "claude", PID: pid, LaunchedAt: launched}); err != nil {
		t.Fatal(err)
	}
}

// openWindow is a pending capture held by this test process, launched now.
func openWindow(t *testing.T, inst string) {
	t.Helper()
	writePending(t, inst, showDispatchA, os.Getpid(), time.Now())
}

// writeClaudeRecord writes a Claude job-state file claiming cwd for
// sessionID, as the agent writes one when a session starts.
func writeClaudeRecord(t *testing.T, home, sessionID, cwd string) {
	t.Helper()
	doc, _ := json.Marshal(map[string]string{"sessionId": sessionID, "template": "bg", "state": "running", "cwd": cwd})
	writeShowFile(t, filepath.Join(home, ".claude", "jobs", sessionID[:8], "state.json"), string(doc), 0o600)
}

func checkState(t *testing.T, inst, sessionID string) map[string]any {
	t.Helper()
	r := runAuthorityCmd(t, inst, "check", "--json", "--session", sessionID, "-C", inst, "--point", "merge-pr").mustSucceed(t)
	var m map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &m); err != nil {
		t.Fatalf("%v\n%s", err, r.stdout)
	}
	return m
}

func wantAnswer(t *testing.T, got map[string]any, state, detail string) {
	t.Helper()
	key := "binding"
	if state != string(authority.StateBound) {
		key = "reason"
	}
	if got["state"] != state || got[key] != detail {
		t.Errorf("state %v %s %v, want %s %s", got["state"], key, got[key], state, detail)
	}
}

func TestCheckBindsThroughTheLineageEntry(t *testing.T) {
	w, inst, _ := captureWorkspace(t)
	writeLineage(t, w.root, showDispatchB, "ws-other", showSessionB)
	if err := workspace.CompleteLineageEntry(w.root, showDispatchA, showSessionA); err != nil {
		t.Fatal(err)
	}
	got := checkState(t, inst, showSessionA)
	wantAnswer(t, got, "bound", "lineage")
	if g, _ := got["grant"].([]any); len(g) != 1 {
		t.Errorf("grant = %v, want merge-pr", got["grant"])
	}
	// Another session in the same instance, and the session another
	// instance's entry names, aren't bound here.
	wantAnswer(t, checkState(t, inst, showSessionB), "not-dispatched", "no-mapping")
}

func TestCheckLineageWithAnUnusableRecord(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		pending      bool
		setup        func(t *testing.T, inst string)
	}{
		{"record deleted, capture pending", authority.ReasonRecordMissing, true, func(t *testing.T, inst string) {
			os.Remove(workspace.AuthorityRecordPath(inst))
		}},
		{"record deleted, no capture pending", authority.ReasonRecordMissing, false, func(t *testing.T, inst string) {
			os.Remove(workspace.AuthorityRecordPath(inst))
		}},
		{"record not JSON", authority.ReasonRecordMalformed, false, func(t *testing.T, inst string) {
			writeRawRecord(t, inst, "{")
		}},
		{"record for another dispatch", authority.ReasonRecordOtherDispatch, false, func(t *testing.T, inst string) {
			writeShowRecord(t, inst, showRecord(showDispatchB, authority.Autonomous, authority.LayerWorkspace, authority.MergePR))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, inst, _ := captureWorkspace(t)
			if err := workspace.CompleteLineageEntry(w.root, showDispatchA, showSessionA); err != nil {
				t.Fatal(err)
			}
			if tc.pending {
				openWindow(t, inst)
			}
			tc.setup(t, inst)
			got := checkState(t, inst, showSessionA)
			wantAnswer(t, got, "record-unusable", tc.reason)
			if got["dispatch_id"] != showDispatchA {
				t.Errorf("dispatch_id = %v, want the lineage entry's", got["dispatch_id"])
			}
		})
	}
}

func TestCheckBindsThroughRecordsWhileCaptureIsPending(t *testing.T) {
	_, inst, home := captureWorkspace(t)
	openWindow(t, inst)
	writeClaudeRecord(t, home, showSessionA, inst)
	got := checkState(t, inst, showSessionA)
	wantAnswer(t, got, "bound", "records")
	if !strings.Contains(mustJSON(t, got["grant"]), "merge-pr") {
		t.Errorf("grant = %v, want merge-pr", got["grant"])
	}
	wantAnswer(t, checkState(t, inst, showSessionB), "not-dispatched", "no-mapping")
}

func TestCheckRecordsWithAnUnusableRecord(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		setup        func(t *testing.T, inst string)
	}{
		{"deleted", authority.ReasonRecordMissing, func(t *testing.T, inst string) { os.Remove(workspace.AuthorityRecordPath(inst)) }},
		{"not JSON", authority.ReasonRecordMalformed, func(t *testing.T, inst string) { writeRawRecord(t, inst, "{") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, inst, home := captureWorkspace(t)
			openWindow(t, inst)
			writeClaudeRecord(t, home, showSessionA, inst)
			tc.setup(t, inst)
			wantAnswer(t, checkState(t, inst, showSessionA), "record-unusable", tc.reason)
		})
	}
}

func TestCheckRecordsBindNothingOutsideAnOpenWindow(t *testing.T) {
	exited := exec.Command("true")
	if err := exited.Run(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, w *showWorkspace, inst, home string)
	}{
		{"no capture pending", func(t *testing.T, w *showWorkspace, inst, home string) {}},
		{"the dispatch process has exited", func(t *testing.T, w *showWorkspace, inst, home string) {
			writePending(t, inst, showDispatchA, exited.Process.Pid, time.Now())
		}},
		{"the process started after the launch time", func(t *testing.T, w *showWorkspace, inst, home string) {
			writePending(t, inst, showDispatchA, os.Getpid(), time.Now().Add(-24*time.Hour))
		}},
		{"a second record claims the instance", func(t *testing.T, w *showWorkspace, inst, home string) {
			openWindow(t, inst)
			writeClaudeRecord(t, home, showSessionB, inst)
		}},
		{"the pending file is malformed", func(t *testing.T, w *showWorkspace, inst, home string) {
			writeShowFile(t, workspace.CapturePendingPath(inst), "{", 0o600)
		}},
		{"the pending file names an agent niwa can't launch", func(t *testing.T, w *showWorkspace, inst, home string) {
			if err := workspace.WriteCapturePending(inst, workspace.CapturePending{DispatchID: showDispatchA, Agent: "nonesuch", PID: os.Getpid(), LaunchedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, inst, home := captureWorkspace(t)
			writeClaudeRecord(t, home, showSessionA, inst)
			tc.setup(t, w, inst, home)
			wantAnswer(t, checkState(t, inst, showSessionA), "not-dispatched", "no-mapping")
			wantAnswer(t, checkState(t, inst, showSessionB), "not-dispatched", "no-mapping")
		})
	}
}

// TestCheckRecordsStopOnceTheLineageNamesTheSession: once capture recorded
// a session id, the records no longer bind: the session it recorded is bound
// through the lineage entry, and the one whose record also claims the
// instance is not.
func TestCheckRecordsStopOnceTheLineageNamesTheSession(t *testing.T) {
	w, inst, home := captureWorkspace(t)
	openWindow(t, inst)
	writeClaudeRecord(t, home, showSessionA, inst)
	if err := workspace.CompleteLineageEntry(w.root, showDispatchA, showSessionB); err != nil {
		t.Fatal(err)
	}
	wantAnswer(t, checkState(t, inst, showSessionA), "not-dispatched", "no-mapping")
	wantAnswer(t, checkState(t, inst, showSessionB), "bound", "lineage")
}

// TestCheckMappingStillWinsWhileCaptureIsPending: once dispatch writes the
// mapping it decides, even before the pending file is removed.
func TestCheckMappingStillWinsWhileCaptureIsPending(t *testing.T) {
	w, inst, home := captureWorkspace(t)
	openWindow(t, inst)
	writeClaudeRecord(t, home, showSessionA, inst)
	checkMapping(t, w.root, showSessionA, inst, "dispatch", showDispatchA)
	wantAnswer(t, checkState(t, inst, showSessionA), "bound", "mapping")
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
