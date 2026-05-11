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
func noopTerminate(worktreePath string) error                      { return nil }
func noopEnsureDaemon(worktreePath string, extraEnv []string) error { return nil }

// daemonCallTracker records the order of TerminateDaemon and EnsureDaemonRunning
// calls so AC22c tests can assert the production sequencing (terminate before
// supervise, ensure-running on every exit path). Each method records the
// caller's worktree path so tests can also verify both calls target the
// same worktree.
type daemonCallTracker struct {
	terminateCalls    []string
	ensureRunningCalls []string
}

func (d *daemonCallTracker) Terminate(worktree string) error {
	d.terminateCalls = append(d.terminateCalls, worktree)
	return nil
}
func (d *daemonCallTracker) EnsureRunning(worktree string, _ []string) error {
	d.ensureRunningCalls = append(d.ensureRunningCalls, worktree)
	return nil
}

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

// TestAttachRunSucceedsOverStaleSentinel covers PRD AC11/AC12 at the
// AttachRun integration level (the previous review noted MCP-layer
// coverage existed but the AttachRun integration path was untested).
//
// Seeds a stale sentinel (sentinel file exists; OwnerPID points at the
// test process but OwnerStartTime is bogus, so IsPIDAlive returns
// false) WITHOUT holding the flock. This models the operationally-
// common stale-lock scenario: the previous niwa-attach process was
// killed in a way that released the kernel-level flock (clean exit,
// SIGKILL by OOM killer, host reboot) but the sentinel file survived
// on disk. AttachRun's acquire path should:
//  1. Take the flock successfully (no live holder).
//  2. Continue through preflight + daemon teardown.
//  3. Overwrite the stale sentinel with the new owner's metadata
//     (atomic tmp+rename), then run claude (stubbed), detach, cleanup.
//
// The exact retry-once branch at attach.go:104-108 handles a narrower
// race where the flock is briefly held by a process dying mid-call;
// that branch is defensive against a kernel-level timing window and
// is not deterministically reproducible from a unit test. The
// operationally-common path covered here is what operators rely on
// after SSH disconnects and similar.
// TestAttachRunEnvelopeQueuedDuringAttachSurvivesDetach covers PRD AC4a/AC4b
// at the AttachRun integration level: an envelope dropped into the
// worktree's role inbox WHILE the attach lock is held must remain in
// place across detach so the daemon's catch-up replay (run by
// EnsureDaemonRunning's scanExistingInboxes path on respawn) can pick
// it up.
//
// The test uses the SuperviseFn injection point to drop an envelope
// into the inbox at the moment the attach lock is held -- this models
// a coordinator calling niwa_delegate during attach. After supervise
// returns (simulated claude exit), the cleanup defer respawns the
// daemon (stubbed) and the test verifies the envelope file is still
// in the inbox where the daemon's catch-up scan would find it.
//
// The daemon's actual scanExistingInboxes behaviour is exercised by
// mesh_watch tests; this test guards the attach-side contract that
// envelopes queued during attach are preserved, not deleted, not
// moved, and not corrupted.
func TestAttachRunEnvelopeQueuedDuringAttachSurvivesDetach(t *testing.T) {
	root, sid, home := setupAttachableSession(t, mcp.SessionStatusActive)
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	st, _ := mcp.ReadSessionLifecycleState(sessionsDir, sid)
	inboxDir := filepath.Join(st.WorktreePath, ".niwa", "roles", st.Repo, "inbox")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	queuedEnvelopePath := filepath.Join(inboxDir, "test-queued-envelope.json")
	queuedEnvelopeBody := []byte(`{"v":1,"id":"test-queued-envelope","body":{"action":"test"}}`)

	// During the supervise call (i.e. while the attach lock is held and
	// the daemon is torn down), drop an envelope as if a coordinator
	// had called niwa_delegate.
	dropEnvelopeDuringAttach := func(_ context.Context, _ SuperviseOptions) (int, error) {
		if err := os.WriteFile(queuedEnvelopePath, queuedEnvelopeBody, 0o600); err != nil {
			return 1, err
		}
		return 0, nil
	}

	err := AttachRun(context.Background(), withDaemonStubs(Options{
		InstanceRoot: root, SessionID: sid, HomeDir: home,
		Stderr:      &bytes.Buffer{},
		SuperviseFn: dropEnvelopeDuringAttach,
	}))
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
	if _, err := os.Stat(mcp.AttachStatePath(st.WorktreePath)); !os.IsNotExist(err) {
		t.Errorf("sentinel not cleaned up: %v", err)
	}
}

// TestAttachRunDaemonCallOrdering covers PRD AC22c by verifying both
// daemon-helper invocations fire on the happy path, target the same
// worktree, and arrive in the correct order: TerminateDaemon before
// the supervise call (so envelopes don't get claimed during attach),
// EnsureDaemonRunning after the supervise call (so the mesh resumes).
// This guards the defer-ordering fix from this PR: a future refactor
// that reverts the defer-reorder would regress the daemon stays
// dead on a WriteAttachState failure, but it would also be caught
// here by missing the EnsureRunning call.
func TestAttachRunDaemonCallOrdering(t *testing.T) {
	root, sid, home := setupAttachableSession(t, mcp.SessionStatusActive)
	tracker := &daemonCallTracker{}
	err := AttachRun(context.Background(), Options{
		InstanceRoot:          root,
		SessionID:             sid,
		HomeDir:               home,
		Stderr:                &bytes.Buffer{},
		SuperviseFn:           fakeSupervise(0),
		TerminateDaemonFn:     tracker.Terminate,
		EnsureDaemonRunningFn: tracker.EnsureRunning,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	if got := len(tracker.terminateCalls); got != 1 {
		t.Errorf("TerminateDaemon invoked %d times, want exactly 1", got)
	}
	if got := len(tracker.ensureRunningCalls); got != 1 {
		t.Errorf("EnsureDaemonRunning invoked %d times, want exactly 1", got)
	}
	if len(tracker.terminateCalls) > 0 && len(tracker.ensureRunningCalls) > 0 {
		if tracker.terminateCalls[0] != tracker.ensureRunningCalls[0] {
			t.Errorf("daemon helpers targeted different worktrees: terminate=%q ensure=%q",
				tracker.terminateCalls[0], tracker.ensureRunningCalls[0])
		}
	}

	// Worktree the helpers were called on should match the session state.
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	st, _ := mcp.ReadSessionLifecycleState(sessionsDir, sid)
	if len(tracker.terminateCalls) > 0 && tracker.terminateCalls[0] != st.WorktreePath {
		t.Errorf("TerminateDaemon called on %q, want %q", tracker.terminateCalls[0], st.WorktreePath)
	}
}

// TestAttachRunDaemonRespawnsOnSuperviseError covers the defer-ordering
// fix specifically: when the supervise call returns an error (claude
// binary not found, exec failed, etc.), the cleanup defer must still
// fire so the daemon respawns. Without the defer-reorder this PR
// landed, an early supervise failure would leave the daemon dead.
func TestAttachRunDaemonRespawnsOnSuperviseError(t *testing.T) {
	root, sid, home := setupAttachableSession(t, mcp.SessionStatusActive)
	tracker := &daemonCallTracker{}
	errSupervise := func(context.Context, SuperviseOptions) (int, error) {
		return 1, errors.New("simulated claude not found")
	}
	err := AttachRun(context.Background(), Options{
		InstanceRoot:          root,
		SessionID:             sid,
		HomeDir:               home,
		Stderr:                &bytes.Buffer{},
		SuperviseFn:           errSupervise,
		TerminateDaemonFn:     tracker.Terminate,
		EnsureDaemonRunningFn: tracker.EnsureRunning,
	})
	if err == nil {
		t.Fatalf("expected error from supervise failure, got nil")
	}
	if len(tracker.terminateCalls) != 1 {
		t.Errorf("TerminateDaemon invoked %d times, want 1", len(tracker.terminateCalls))
	}
	if len(tracker.ensureRunningCalls) != 1 {
		t.Errorf("EnsureDaemonRunning invoked %d times after supervise error, want 1 "+
			"(if 0, the defer-reorder regressed and the daemon stays dead)",
			len(tracker.ensureRunningCalls))
	}
}

func TestAttachRunSucceedsOverStaleSentinel(t *testing.T) {
	root, sid, home := setupAttachableSession(t, mcp.SessionStatusActive)
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	st, _ := mcp.ReadSessionLifecycleState(sessionsDir, sid)
	// Seed a stale sentinel: bogus start_time forces IsPIDAlive(false).
	if err := mcp.WriteAttachState(st.WorktreePath, mcp.AttachState{
		V:              1,
		OwnerPID:       os.Getpid(),
		OwnerStartTime: 1,
		StartedAt:      "2026-05-09T00:00:00Z",
		LockPath:       ".niwa/attach.lock",
	}); err != nil {
		t.Fatalf("seed stale sentinel: %v", err)
	}

	err := AttachRun(context.Background(), withDaemonStubs(Options{
		InstanceRoot: root, SessionID: sid, HomeDir: home,
		Stderr:      &bytes.Buffer{},
		SuperviseFn: fakeSupervise(0),
	}))
	if err != nil {
		t.Fatalf("AttachRun over stale sentinel failed: %v", err)
	}

	// The sentinel that was written by AttachRun was removed by the
	// cleanup defer; the worktree is now back to available.
	st2, avail, _ := mcp.ReadAttachState(st.WorktreePath, false)
	if avail != mcp.AttachAvailable || st2 != nil {
		t.Errorf("sentinel not cleaned up after successful attach: state=%+v avail=%v", st2, avail)
	}
}

// Note: the retry-once branch in attach.go (read-sentinel-after-
// EWOULDBLOCK, reap-if-stale, retry acquireAttachLock) is defensive
// against a narrow kernel-level race where a process's flock is held
// across its own death. That window is not deterministically
// reproducible from a unit test — once a process dies, the kernel
// releases its flock immediately, so any test that holds the flock
// "stale" against a "dead" PID is structurally impossible. The branch
// is exercised only by the simpler stale-sentinel test above (which
// proves the AttachRun side of the recovery story; the lower-level
// retry mechanics are covered by TestReadAttachStateStaleDetection
// and TestReadAttachStateReapStaleDeletes in internal/mcp/).
