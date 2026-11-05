package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/agentplan"
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

// testRecordDigest stands in for a recorded settings digest; recordContinuable
// only requires one to be there.
const testRecordDigest = `{"v":1,"f":{}}`

// testRespawn is the saved options of an unsandboxed review stage: what
// watchReviewLaunch passes, minus the sandbox's --strict-mcp-config.
var testRespawn = []any{"--name", "watch_acme_api_42", "--settings", `{"env":{"NIWA_REAP_KEY":"k"}}`}

// idleJob is the job state of a detached-idle review session with saved
// options respawn.
func idleJob(respawn []any) map[string]any {
	return map[string]any{"state": "done", "tempo": "idle", "inFlight": map[string]any{"tasks": 0}, "respawnFlags": respawn}
}

func TestRecordContinuable(t *testing.T) {
	now := time.Now()

	newSetup := func(t *testing.T, extra map[string]any) (jobsDir string, rec watch.StagedRecord) {
		jobsDir = t.TempDir()
		inst := t.TempDir()
		if _, ok := extra["respawnFlags"]; !ok {
			extra["respawnFlags"] = testRespawn
		}
		writeJobStateFile(t, jobsDir, "aaaaaaaa", testConvID, inst, extra)
		rec = watch.StagedRecord{SessionID: testConvID, ShortID: "aaaaaaaa", InstancePath: inst, SettingsDigest: testRecordDigest}
		return jobsDir, rec
	}

	t.Run("detached-idle with valid ids is continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, idleJob(testRespawn))
		if !recordContinuable(jobsDir, rec, now, false) {
			t.Fatal("a live detached-idle record with valid ids must be continuable")
		}
	})

	// `claude stop` leaves the job state alone, so a review a gate refusal left
	// stopped reads exactly like this, and is continued once the cause is gone.
	t.Run("stopped with its job entry is continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, map[string]any{"state": "done", "tempo": "idle"})
		if !recordContinuable(jobsDir, rec, now, false) {
			t.Fatal("a stopped review whose job entry is still listed must be continuable")
		}
	})

	t.Run("sandboxed with --strict-mcp-config saved is continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, idleJob(append([]any{"--strict-mcp-config"}, testRespawn...)))
		if !recordContinuable(jobsDir, rec, now, true) {
			t.Fatal("a sandboxed review saved with --strict-mcp-config must be continuable")
		}
	})

	t.Run("sandboxed without --strict-mcp-config saved is not continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, idleJob(testRespawn))
		if recordContinuable(jobsDir, rec, now, true) {
			t.Fatal("a sandboxed review whose saved options lack --strict-mcp-config must not be continuable")
		}
	})

	t.Run("halted record is not continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, idleJob(testRespawn))
		rec.ContinuationHalted = "0123456789abcdef0123456789abcdef01234567"
		if recordContinuable(jobsDir, rec, now, false) {
			t.Fatal("a record carrying continuation_halted must not be continuable")
		}
	})

	t.Run("empty settings digest is not continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, idleJob(testRespawn))
		rec.SettingsDigest = ""
		if recordContinuable(jobsDir, rec, now, false) {
			t.Fatal("a record without a settings digest must not be continuable")
		}
	})

	t.Run("refused saved option is not continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, idleJob(append([]any{"--permission-mode", "bypassPermissions"}, testRespawn...)))
		if recordContinuable(jobsDir, rec, now, false) {
			t.Fatal("a review whose saved options carry --permission-mode must not be continuable")
		}
	})

	t.Run("unreadable saved options are not continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, map[string]any{"state": "done", "tempo": "idle", "respawnFlags": nil})
		if recordContinuable(jobsDir, rec, now, false) {
			t.Fatal("a review whose saved options can't be read must not be continuable")
		}
	})

	t.Run("busy session is not continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, map[string]any{"state": "working", "tempo": "active", "inFlight": map[string]any{"tasks": 5}})
		if recordContinuable(jobsDir, rec, now, false) {
			t.Fatal("a busy session must not be continuable")
		}
	})

	t.Run("attached (awaiting answer) session is not continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, map[string]any{"state": "done", "tempo": "idle", "needs": "answer: ?"})
		if recordContinuable(jobsDir, rec, now, false) {
			t.Fatal("an attached/awaiting session must not be continuable")
		}
	})

	t.Run("missing short id is not continuable (cannot stop)", func(t *testing.T) {
		jobsDir, rec := newSetup(t, map[string]any{"state": "done", "tempo": "idle"})
		rec.ShortID = ""
		if recordContinuable(jobsDir, rec, now, false) {
			t.Fatal("a record without a safe short id must not be continuable")
		}
	})

	t.Run("invalid session id is not continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, map[string]any{"state": "done", "tempo": "idle"})
		rec.SessionID = "not-a-uuid"
		if recordContinuable(jobsDir, rec, now, false) {
			t.Fatal("a record without a valid resume id must not be continuable")
		}
	})

	t.Run("no live job in instance is not continuable", func(t *testing.T) {
		jobsDir, rec := newSetup(t, map[string]any{"state": "done", "tempo": "idle"})
		rec.InstancePath = t.TempDir() // different dir: no job rooted here
		if recordContinuable(jobsDir, rec, now, false) {
			t.Fatal("a record whose instance has no live job must not be continuable")
		}
	})

	t.Run("dead session (no job entry) is not continuable", func(t *testing.T) {
		jobsDir := t.TempDir() // empty jobs dir
		rec := watch.StagedRecord{SessionID: testConvID, ShortID: "aaaaaaaa", InstancePath: t.TempDir(), SettingsDigest: testRecordDigest}
		if recordContinuable(jobsDir, rec, now, false) {
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
	writeJobStateFile(t, realJobs, "11111111", "11111111-aaaa-bbbb-cccc-dddddddddddd", inst1, idleJob(testRespawn))
	writeJobStateFile(t, realJobs, "22222222", "22222222-aaaa-bbbb-cccc-dddddddddddd", inst2, idleJob(testRespawn))

	mustSave := func(handle, sid, short, inst string) {
		rec := watch.StagedRecord{
			Handle: handle, Owner: "acme", Repo: "api", Number: 42,
			SessionID: sid, ShortID: short, InstancePath: inst,
			SettingsDigest: testRecordDigest,
		}
		if err := watch.SaveStagedRecord(root, rec); err != nil {
			t.Fatal(err)
		}
	}
	mustSave("h1", "11111111-aaaa-bbbb-cccc-dddddddddddd", "11111111", inst1)
	mustSave("h2", "22222222-aaaa-bbbb-cccc-dddddddddddd", "22222222", inst2)

	id := watch.HandledIdentity("acme", "api", 42)
	// A record alone is continuable, so the refusal below is the ambiguity's
	// and not a gate's.
	if !recordContinuable(realJobs, watch.StagedRecord{SessionID: "11111111-aaaa-bbbb-cccc-dddddddddddd", ShortID: "11111111", InstancePath: inst1, SettingsDigest: testRecordDigest}, time.Now(), false) {
		t.Fatal("precondition: each record must be continuable on its own")
	}
	live, continuable, err := liveStagedSessions(root, false)
	if err != nil {
		t.Fatal(err)
	}
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
		watchCapture = func(_ agentplan.SessionRecords, _, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
			return testConvID, "aaaaaaaa", nil
		}
		sid, short := captureReviewSession(t.TempDir())
		if sid != testConvID || short != "aaaaaaaa" {
			t.Fatalf("expected the captured ids, got %q %q", sid, short)
		}
	})

	t.Run("capture miss degrades to empty", func(t *testing.T) {
		watchCapture = func(_ agentplan.SessionRecords, _, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
			return "", "", errors.New("timed out")
		}
		if sid, short := captureReviewSession(t.TempDir()); sid != "" || short != "" {
			t.Fatalf("a capture miss must yield empty ids, got %q %q", sid, short)
		}
	})

	t.Run("invalid captured id is rejected", func(t *testing.T) {
		watchCapture = func(_ agentplan.SessionRecords, _, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
			return "not-a-uuid", "aaaaaaaa", nil
		}
		if sid, short := captureReviewSession(t.TempDir()); sid != "" || short != "" {
			t.Fatalf("an invalid captured UUID must be rejected, got %q %q", sid, short)
		}
	})
}
