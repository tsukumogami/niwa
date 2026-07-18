package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/watch"
)

const testConvID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

// writeJobStateFile writes a fake ~/.claude/jobs/<short>/state.json with the
// given fields so the continuation readers (readJobState / instanceHasLiveJob)
// resolve it. Only the fields the classifier reads are set from js; sessionId and
// cwd are always written so both the prefix scan and the cwd scan match.
func writeJobStateFile(t *testing.T, jobsDir, short, sessionID, cwd string, extra map[string]any) {
	t.Helper()
	dir := filepath.Join(jobsDir, short)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := map[string]any{"sessionId": sessionID, "cwd": cwd, "template": "bg"}
	for k, v := range extra {
		m[k] = v
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRecordJobActivity(t *testing.T) {
	jobsDir := t.TempDir()
	inst := t.TempDir()
	writeJobStateFile(t, jobsDir, "aaaaaaaa", testConvID, inst, map[string]any{
		"state":    "done",
		"tempo":    "idle",
		"inFlight": map[string]any{"tasks": 0},
	})
	rec := watch.StagedRecord{SessionID: testConvID, ShortID: "aaaaaaaa", InstancePath: inst}
	got := recordJobActivity(jobsDir, rec)
	if !got.Readable || got.State != "done" || got.Tempo != "idle" || got.InFlightTasks != 0 || got.AwaitingInput {
		t.Fatalf("unexpected activity projection: %+v", got)
	}

	// A pending block/needs surfaces AwaitingInput.
	writeJobStateFile(t, jobsDir, "aaaaaaaa", testConvID, inst, map[string]any{
		"state": "blocked", "tempo": "blocked",
		"block": map[string]any{"questions": []any{map[string]any{"question": "?"}}},
		"needs": "answer: ?",
	})
	got = recordJobActivity(jobsDir, rec)
	if !got.AwaitingInput {
		t.Fatalf("expected AwaitingInput for a blocked session, got %+v", got)
	}

	// An invalid session id yields an unreadable activity (fail-closed).
	bad := watch.StagedRecord{SessionID: "not-a-uuid", ShortID: "aaaaaaaa", InstancePath: inst}
	if recordJobActivity(jobsDir, bad).Readable {
		t.Fatal("invalid session id must classify unreadable")
	}

	// A colliding-prefix job whose full sessionId differs is not our session.
	writeJobStateFile(t, jobsDir, "aaaaaaaa", "ffffffff-1111-2222-3333-444444444444", inst, map[string]any{"state": "done", "tempo": "idle"})
	if recordJobActivity(jobsDir, rec).Readable {
		t.Fatal("session-id mismatch must classify unreadable")
	}
}

func TestRecordContinuable(t *testing.T) {
	now := time.Now()

	newSetup := func(t *testing.T, extra map[string]any) (jobsDir string, rec watch.StagedRecord) {
		jobsDir = t.TempDir()
		inst := t.TempDir()
		writeJobStateFile(t, jobsDir, "aaaaaaaa", testConvID, inst, extra)
		rec = watch.StagedRecord{SessionID: testConvID, ShortID: "aaaaaaaa", InstancePath: inst}
		return jobsDir, rec
	}

	t.Run("detached-idle with valid ids is continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, map[string]any{"state": "done", "tempo": "idle", "inFlight": map[string]any{"tasks": 0}})
		if !recordContinuable(jobsDir, rec, now) {
			t.Fatal("a live detached-idle record with valid ids must be continuable")
		}
	})

	t.Run("busy session is not continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, map[string]any{"state": "working", "tempo": "active", "inFlight": map[string]any{"tasks": 5}})
		if recordContinuable(jobsDir, rec, now) {
			t.Fatal("a busy session must not be continuable")
		}
	})

	t.Run("attached (awaiting answer) session is not continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, map[string]any{"state": "done", "tempo": "idle", "needs": "answer: ?"})
		if recordContinuable(jobsDir, rec, now) {
			t.Fatal("an attached/awaiting session must not be continuable")
		}
	})

	t.Run("missing short id is not continuable (cannot stop)", func(t *testing.T) {
		jobsDir, rec := newSetup(t, map[string]any{"state": "done", "tempo": "idle"})
		rec.ShortID = ""
		if recordContinuable(jobsDir, rec, now) {
			t.Fatal("a record without a safe short id must not be continuable")
		}
	})

	t.Run("invalid session id is not continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, map[string]any{"state": "done", "tempo": "idle"})
		rec.SessionID = "not-a-uuid"
		if recordContinuable(jobsDir, rec, now) {
			t.Fatal("a record without a valid resume id must not be continuable")
		}
	})

	t.Run("no live job in instance is not continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, map[string]any{"state": "done", "tempo": "idle"})
		rec.InstancePath = t.TempDir() // different dir: no job rooted here
		if recordContinuable(jobsDir, rec, now) {
			t.Fatal("a record whose instance has no live job must not be continuable")
		}
	})

	t.Run("dead session (no job entry) is not continuable", func(t *testing.T) {
		jobsDir := t.TempDir() // empty jobs dir
		rec := watch.StagedRecord{SessionID: testConvID, ShortID: "aaaaaaaa", InstancePath: t.TempDir()}
		if recordContinuable(jobsDir, rec, now) {
			t.Fatal("a dead session must not be continuable")
		}
	})
}

// TestLiveStagedSessions_ContinuableAmbiguity: when two live records for the SAME
// PR both classify continuable, neither is returned as continuable (fail-closed),
// though the PR is still marked live.
func TestLiveStagedSessions_ContinuableAmbiguity(t *testing.T) {
	root := t.TempDir()

	// liveStagedSessions resolves its jobs dir via defaultJobsDir() (HOME/.claude/
	// jobs), so redirect HOME to a temp home and seed that layout.
	home := t.TempDir()
	t.Setenv("HOME", home)
	realJobs := filepath.Join(home, ".claude", "jobs")
	if err := os.MkdirAll(realJobs, 0o755); err != nil {
		t.Fatal(err)
	}

	inst1 := t.TempDir()
	inst2 := t.TempDir()
	writeJobStateFile(t, realJobs, "11111111", "11111111-aaaa-bbbb-cccc-dddddddddddd", inst1, map[string]any{"state": "done", "tempo": "idle"})
	writeJobStateFile(t, realJobs, "22222222", "22222222-aaaa-bbbb-cccc-dddddddddddd", inst2, map[string]any{"state": "done", "tempo": "idle"})

	mustSave := func(handle, sid, short, inst string) {
		rec := watch.StagedRecord{
			Handle: handle, Owner: "acme", Repo: "api", Number: 42,
			SessionID: sid, ShortID: short, InstancePath: inst,
		}
		if err := watch.SaveStagedRecord(root, rec); err != nil {
			t.Fatal(err)
		}
	}
	mustSave("h1", "11111111-aaaa-bbbb-cccc-dddddddddddd", "11111111", inst1)
	mustSave("h2", "22222222-aaaa-bbbb-cccc-dddddddddddd", "22222222", inst2)

	live, continuable, err := liveStagedSessions(root)
	if err != nil {
		t.Fatal(err)
	}
	id := watch.HandledIdentity("acme", "api", 42)
	if !live[id] {
		t.Fatal("PR with two live sessions must be marked live")
	}
	if _, ok := continuable[id]; ok {
		t.Fatal("two continuable records for one PR must be ambiguous -> not continuable")
	}
}

func TestCaptureReviewSession(t *testing.T) {
	orig := watchCapture
	t.Cleanup(func() { watchCapture = orig })

	t.Run("valid capture is returned", func(t *testing.T) {
		watchCapture = func(_, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
			return testConvID, "aaaaaaaa", nil
		}
		sid, short := captureReviewSession(t.TempDir())
		if sid != testConvID || short != "aaaaaaaa" {
			t.Fatalf("expected the captured ids, got %q %q", sid, short)
		}
	})

	t.Run("capture miss degrades to empty", func(t *testing.T) {
		watchCapture = func(_, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
			return "", "", errors.New("timed out")
		}
		if sid, short := captureReviewSession(t.TempDir()); sid != "" || short != "" {
			t.Fatalf("a capture miss must yield empty ids, got %q %q", sid, short)
		}
	})

	t.Run("invalid captured id is rejected", func(t *testing.T) {
		watchCapture = func(_, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
			return "not-a-uuid", "aaaaaaaa", nil
		}
		if sid, short := captureReviewSession(t.TempDir()); sid != "" || short != "" {
			t.Fatalf("an invalid captured UUID must be rejected, got %q %q", sid, short)
		}
	})
}
