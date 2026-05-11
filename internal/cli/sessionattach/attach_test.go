package sessionattach

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// fakeSupervise returns a SuperviseFn that returns the given exit code
// without actually spawning anything.
func fakeSupervise(exitCode int) func(context.Context, SuperviseOptions) (int, error) {
	return func(context.Context, SuperviseOptions) (int, error) {
		return exitCode, nil
	}
}

// noopTerminate / noopEnsureDaemon stub the workspace daemon helpers so tests
// don't spawn real daemon processes. Without these stubs the test binary
// gets spawned as os.Executable() with `mesh watch` flags, leaking process
// trees and fsnotify watchers across runs.
func noopTerminate(worktreePath string) error                    { return nil }
func noopEnsureDaemon(worktreePath string, extraEnv []string) error { return nil }

// withDaemonStubs returns Options with the daemon helpers stubbed out so
// tests are hermetic.
func withDaemonStubs(opts Options) Options {
	opts.TerminateDaemonFn = noopTerminate
	opts.EnsureDaemonRunningFn = noopEnsureDaemon
	return opts
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

	state := mcp.SessionLifecycleState{
		V:                    1,
		SessionID:            sessionID,
		Repo:                 "niwa",
		Status:               status,
		WorktreePath:         worktreePath,
		ClaudeConversationID: convID,
	}
	if err := mcp.WriteSessionLifecycleState(sessionsDir, state); err != nil {
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
	root, sid, home := setupAttachableSession(t, mcp.SessionStatusEnded)
	err := AttachRun(context.Background(), withDaemonStubs(Options{
		InstanceRoot: root,
		SessionID:    sid,
		HomeDir:      home,
		Stderr:       &bytes.Buffer{},
		SuperviseFn:  fakeSupervise(0),
	}))
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
	err := AttachRun(context.Background(), withDaemonStubs(Options{
		InstanceRoot: root, SessionID: "deadbeef",
		HomeDir:     t.TempDir(),
		Stderr:      &bytes.Buffer{},
		SuperviseFn: fakeSupervise(0),
	}))
	var ece *ExitCodeError
	if !errors.As(err, &ece) || ece.Code != 1 {
		t.Fatalf("want ExitCodeError code 1, got %v", err)
	}
	if !strings.Contains(ece.Msg, "session deadbeef not found") {
		t.Errorf("missing not-found: %q", ece.Msg)
	}
}

func TestAttachRunPreflightCaseAEmptyConvID(t *testing.T) {
	root, sid, home := setupAttachableSession(t, mcp.SessionStatusActive)
	// Overwrite the lifecycle state to clear the conv id.
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	st, _ := mcp.ReadSessionLifecycleState(sessionsDir, sid)
	st.ClaudeConversationID = ""
	_ = mcp.WriteSessionLifecycleState(sessionsDir, st)
	err := AttachRun(context.Background(), withDaemonStubs(Options{
		InstanceRoot: root, SessionID: sid, HomeDir: home,
		Stderr:      &bytes.Buffer{},
		SuperviseFn: fakeSupervise(0),
	}))
	var ece *ExitCodeError
	if !errors.As(err, &ece) || ece.Code != 1 {
		t.Fatalf("want ExitCodeError code 1, got %v", err)
	}
	if !strings.Contains(ece.Msg, "no captured claude conversation id") {
		t.Errorf("missing case-A message: %q", ece.Msg)
	}
}

func TestAttachRunLockHeldByLiveProcess(t *testing.T) {
	root, sid, home := setupAttachableSession(t, mcp.SessionStatusActive)
	// Seed the sentinel pointing at our own PID (alive) and acquire the
	// flock from this test process so AttachRun encounters EWOULDBLOCK.
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	st, _ := mcp.ReadSessionLifecycleState(sessionsDir, sid)
	myPID := os.Getpid()
	myStart, _ := mcp.PIDStartTime(myPID)
	rfc3339 := "2026-05-10T14:32:11Z" // pinned so the substring assertion is exact
	if err := mcp.WriteAttachState(st.WorktreePath, mcp.AttachState{
		V: 1, OwnerPID: myPID, OwnerStartTime: myStart,
		StartedAt: rfc3339,
		LockPath:  ".niwa/attach.lock",
	}); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	lockPath := mcp.AttachLockPath(st.WorktreePath)
	heldFile, lockErr := acquireAttachLock(lockPath)
	if lockErr != nil {
		t.Fatalf("acquireAttachLock for setup: %v", lockErr)
	}
	t.Cleanup(func() { _ = heldFile.Close() })

	err := AttachRun(context.Background(), withDaemonStubs(Options{
		InstanceRoot: root, SessionID: sid, HomeDir: home,
		Stderr:      &bytes.Buffer{},
		SuperviseFn: fakeSupervise(0),
	}))
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
	root, sid, home := setupAttachableSession(t, mcp.SessionStatusActive)
	var stderr bytes.Buffer
	err := AttachRun(context.Background(), withDaemonStubs(Options{
		InstanceRoot: root, SessionID: sid, HomeDir: home,
		Stderr:      &stderr,
		SuperviseFn: fakeSupervise(0),
	}))
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
	st, _, _ := mcp.ReadAttachState(filepath.Join(root, ".niwa", "worktrees", "niwa-"+sid), false)
	if st != nil {
		t.Errorf("sentinel still present: %+v", st)
	}
}

func TestAttachRunNonZeroClaudeExitPropagated(t *testing.T) {
	root, sid, home := setupAttachableSession(t, mcp.SessionStatusActive)
	err := AttachRun(context.Background(), withDaemonStubs(Options{
		InstanceRoot: root, SessionID: sid, HomeDir: home,
		Stderr:      &bytes.Buffer{},
		SuperviseFn: fakeSupervise(42),
	}))
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("want ExitCodeError, got %T", err)
	}
	if ece.Code != 42 {
		t.Errorf("Code = %d, want 42", ece.Code)
	}
}
