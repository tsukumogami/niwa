package sessionattach

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/worktree"
)

// fakeSupervise returns a SuperviseFn that returns the given exit code
// without actually spawning anything.
func fakeSupervise(exitCode int) func(context.Context, SuperviseOptions) (int, error) {
	return func(context.Context, SuperviseOptions) (int, error) {
		return exitCode, nil
	}
}

// setupAttachableSession seeds a session with a transcript file the
// preflight check will accept, so AttachRun can proceed past validation.
// Returns the instance root, session id, and home dir used.
func setupAttachableSession(t *testing.T, status string) (instanceRoot, sessionID, homeDir string) {
	t.Helper()
	instanceRoot = t.TempDir()
	homeDir = t.TempDir()
	sessionID = "abcd1234"
	convID := "11111111-2222-3333-4444-555555555555"

	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	worktreePath := filepath.Join(instanceRoot, ".niwa", "worktrees", "niwa-"+sessionID)
	repoCWD := filepath.Join(worktreePath, "niwa")
	if err := os.MkdirAll(filepath.Join(worktreePath, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	if err := os.MkdirAll(repoCWD, 0o700); err != nil {
		t.Fatalf("mkdir repo cwd: %v", err)
	}

	state := worktree.SessionLifecycleState{
		V:                    1,
		SessionID:            sessionID,
		Repo:                 "niwa",
		Status:               status,
		WorktreePath:         worktreePath,
		ClaudeConversationID: convID,
	}
	if err := worktree.WriteSessionLifecycleState(sessionsDir, state); err != nil {
		t.Fatalf("write lifecycle state: %v", err)
	}

	// Seed a non-empty transcript so preflight passes.
	tpath := TranscriptPath(homeDir, repoCWD, convID)
	if err := os.MkdirAll(filepath.Dir(tpath), 0o700); err != nil {
		t.Fatalf("mkdir transcript: %v", err)
	}
	if err := os.WriteFile(tpath, []byte(`{"sessionId":"x"}`+"\n"), 0o600); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	return instanceRoot, sessionID, homeDir
}

func TestAttachRunStatusEndedRejects(t *testing.T) {
	root, sid, home := setupAttachableSession(t, worktree.SessionStatusEnded)
	err := AttachRun(context.Background(), Options{
		InstanceRoot: root,
		SessionID:    sid,
		HomeDir:      home,
		Stderr:       &bytes.Buffer{},
		SuperviseFn:  fakeSupervise(0),
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) || ece.Code != 1 {
		t.Fatalf("want ExitCodeError code 1, got %v", err)
	}
	if !strings.Contains(ece.Msg, "has status ended") {
		t.Errorf("missing 'has status ended': %q", ece.Msg)
	}
	if !strings.Contains(ece.Msg, "create a new session instead") {
		t.Errorf("missing recovery hint: %q", ece.Msg)
	}
}

func TestAttachRunSessionNotFound(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	_ = os.MkdirAll(sessionsDir, 0o700)
	err := AttachRun(context.Background(), Options{
		InstanceRoot: root, SessionID: "deadbeef",
		HomeDir:     t.TempDir(),
		Stderr:      &bytes.Buffer{},
		SuperviseFn: fakeSupervise(0),
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) || ece.Code != 1 {
		t.Fatalf("want ExitCodeError code 1, got %v", err)
	}
	if !strings.Contains(ece.Msg, "session deadbeef not found") {
		t.Errorf("missing not-found: %q", ece.Msg)
	}
}

func TestAttachRunPreflightCaseAEmptyConvID(t *testing.T) {
	root, sid, home := setupAttachableSession(t, worktree.SessionStatusActive)
	// Overwrite the lifecycle state to clear the conv id.
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	st, _ := worktree.ReadSessionLifecycleState(sessionsDir, sid)
	st.ClaudeConversationID = ""
	_ = worktree.WriteSessionLifecycleState(sessionsDir, st)
	err := AttachRun(context.Background(), Options{
		InstanceRoot: root, SessionID: sid, HomeDir: home,
		Stderr:      &bytes.Buffer{},
		SuperviseFn: fakeSupervise(0),
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) || ece.Code != 1 {
		t.Fatalf("want ExitCodeError code 1, got %v", err)
	}
	if !strings.Contains(ece.Msg, "no captured claude conversation id") {
		t.Errorf("missing case-A message: %q", ece.Msg)
	}
}

func TestAttachRunLockHeldByLiveProcess(t *testing.T) {
	root, sid, home := setupAttachableSession(t, worktree.SessionStatusActive)
	// Seed the sentinel pointing at our own PID (alive) and acquire the
	// flock from this test process so AttachRun encounters EWOULDBLOCK.
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	st, _ := worktree.ReadSessionLifecycleState(sessionsDir, sid)
	myPID := os.Getpid()
	myStart, _ := worktree.PIDStartTime(myPID)
	rfc3339 := "2026-05-10T14:32:11Z" // pinned so the substring assertion is exact
	if err := worktree.WriteAttachState(st.WorktreePath, worktree.AttachState{
		V: 1, OwnerPID: myPID, OwnerStartTime: myStart,
		StartedAt: rfc3339,
		LockPath:  ".niwa/attach.lock",
	}); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	lockPath := worktree.AttachLockPath(st.WorktreePath)
	heldFile, lockErr := acquireAttachLock(lockPath)
	if lockErr != nil {
		t.Fatalf("acquireAttachLock for setup: %v", lockErr)
	}
	t.Cleanup(func() { _ = heldFile.Close() })

	err := AttachRun(context.Background(), Options{
		InstanceRoot: root, SessionID: sid, HomeDir: home,
		Stderr:      &bytes.Buffer{},
		SuperviseFn: fakeSupervise(0),
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) || ece.Code != 3 {
		t.Fatalf("want ExitCodeError code 3, got %v", err)
	}
	// PRD AC10 requires the error message to contain the three substrings:
	// the holder PID, the start timestamp in RFC3339, and the recovery
	// command verbatim. Asserting all three here so a regression that drops
	// any of them fails CI.
	wantSubstrs := []string{
		"is already attached",
		"pid=" + itoa(myPID),
		"started=" + rfc3339,
		"`niwa session detach " + sid + " --force`",
	}
	for _, s := range wantSubstrs {
		if !strings.Contains(ece.Msg, s) {
			t.Errorf("missing %q in lock-held error: %q", s, ece.Msg)
		}
	}
}

func TestAttachRunHappyPathPropagatesClaudeExit(t *testing.T) {
	root, sid, home := setupAttachableSession(t, worktree.SessionStatusActive)
	var stderr bytes.Buffer
	err := AttachRun(context.Background(), Options{
		InstanceRoot: root, SessionID: sid, HomeDir: home,
		Stderr:      &stderr,
		SuperviseFn: fakeSupervise(0),
	})
	if err != nil {
		t.Fatalf("happy path: unexpected err %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "session: attached "+sid) {
		t.Errorf("missing 'session: attached': %q", out)
	}
	if !strings.Contains(out, "session: detached "+sid) {
		t.Errorf("missing 'session: detached': %q", out)
	}
	// Sentinel must be cleaned up after.
	st, _, _ := worktree.ReadAttachState(filepath.Join(root, ".niwa", "worktrees", "niwa-"+sid), false)
	if st != nil {
		t.Errorf("sentinel still present: %+v", st)
	}
}

func TestAttachRunNonZeroClaudeExitPropagated(t *testing.T) {
	root, sid, home := setupAttachableSession(t, worktree.SessionStatusActive)
	err := AttachRun(context.Background(), Options{
		InstanceRoot: root, SessionID: sid, HomeDir: home,
		Stderr:      &bytes.Buffer{},
		SuperviseFn: fakeSupervise(42),
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("want ExitCodeError, got %T", err)
	}
	if ece.Code != 42 {
		t.Errorf("Code = %d, want 42", ece.Code)
	}
}

// TestAttachRunEnvelopeQueuedDuringAttachSurvivesDetach covers PRD AC4a/AC4b
// at the AttachRun integration level: an envelope dropped into the worktree's
// role inbox WHILE the attach lock is held must remain in place across detach.
//
// The test uses the SuperviseFn injection point to drop an envelope into the
// inbox at the moment the attach lock is held -- this models a coordinator
// calling niwa_delegate during attach. After supervise returns (simulated
// claude exit), the cleanup defer runs and the test verifies the envelope
// file is still in the inbox, byte-identical.
func TestAttachRunEnvelopeQueuedDuringAttachSurvivesDetach(t *testing.T) {
	root, sid, home := setupAttachableSession(t, worktree.SessionStatusActive)
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	st, _ := worktree.ReadSessionLifecycleState(sessionsDir, sid)
	inboxDir := filepath.Join(st.WorktreePath, ".niwa", "roles", st.Repo, "inbox")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	queuedEnvelopePath := filepath.Join(inboxDir, "test-queued-envelope.json")
	queuedEnvelopeBody := []byte(`{"v":1,"id":"test-queued-envelope","body":{"action":"test"}}`)

	// During the supervise call (i.e. while the attach lock is held), drop an
	// envelope as if a coordinator had called niwa_delegate.
	dropEnvelopeDuringAttach := func(_ context.Context, _ SuperviseOptions) (int, error) {
		if err := os.WriteFile(queuedEnvelopePath, queuedEnvelopeBody, 0o600); err != nil {
			return 1, err
		}
		return 0, nil
	}

	err := AttachRun(context.Background(), Options{
		InstanceRoot: root, SessionID: sid, HomeDir: home,
		Stderr:      &bytes.Buffer{},
		SuperviseFn: dropEnvelopeDuringAttach,
	})
	if err != nil {
		t.Fatalf("AttachRun unexpected err: %v", err)
	}

	// After detach: the queued envelope must still be in the inbox,
	// byte-identical (AttachRun must not touch the inbox).
	got, readErr := os.ReadFile(queuedEnvelopePath)
	if readErr != nil {
		t.Fatalf("queued envelope missing after detach: %v", readErr)
	}
	if string(got) != string(queuedEnvelopeBody) {
		t.Errorf("queued envelope corrupted after detach: got %q, want %q", got, queuedEnvelopeBody)
	}
	// And the attach sentinel must be gone (cleanup defer ran).
	if _, err := os.Stat(worktree.AttachStatePath(st.WorktreePath)); !os.IsNotExist(err) {
		t.Errorf("sentinel not cleaned up: %v", err)
	}
}

func TestAttachRunSucceedsOverStaleSentinel(t *testing.T) {
	root, sid, home := setupAttachableSession(t, worktree.SessionStatusActive)
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	st, _ := worktree.ReadSessionLifecycleState(sessionsDir, sid)
	// Seed a stale sentinel: bogus start_time forces IsPIDAlive(false).
	if err := worktree.WriteAttachState(st.WorktreePath, worktree.AttachState{
		V:              1,
		OwnerPID:       os.Getpid(),
		OwnerStartTime: 1,
		StartedAt:      "2026-05-09T00:00:00Z",
		LockPath:       ".niwa/attach.lock",
	}); err != nil {
		t.Fatalf("seed stale sentinel: %v", err)
	}

	err := AttachRun(context.Background(), Options{
		InstanceRoot: root, SessionID: sid, HomeDir: home,
		Stderr:      &bytes.Buffer{},
		SuperviseFn: fakeSupervise(0),
	})
	if err != nil {
		t.Fatalf("AttachRun over stale sentinel failed: %v", err)
	}

	// The sentinel that was written by AttachRun was removed by the
	// cleanup defer; the worktree is now back to available.
	st2, avail, _ := worktree.ReadAttachState(st.WorktreePath, false)
	if avail != worktree.AttachAvailable || st2 != nil {
		t.Errorf("sentinel not cleaned up after successful attach: state=%+v avail=%v", st2, avail)
	}
}
