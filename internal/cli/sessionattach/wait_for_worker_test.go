package sessionattach

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// seedRunningTaskFixture writes the minimum task-store files (state.json,
// envelope.json, in-progress symlink) so findRunningWorker has something to
// see. Returns the task ID. workerPID and workerStartTime point at the
// process the test wants findRunningWorker to consider "alive".
func seedRunningTaskFixture(t *testing.T, mainInstanceRoot, worktreePath, repoName string, workerPID int, workerStartTime int64) string {
	t.Helper()

	taskID := mcp.NewTaskID()
	taskDir := filepath.Join(mainInstanceRoot, ".niwa", "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatalf("mkdir task dir: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	env := mcp.TaskEnvelope{
		V:      1,
		ID:     taskID,
		From:   mcp.TaskParty{Role: "coordinator", PID: 1000},
		To:     mcp.TaskParty{Role: repoName},
		Body:   json.RawMessage(`{}`),
		SentAt: now,
	}
	envBytes, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "envelope.json"), envBytes, 0o600); err != nil {
		t.Fatalf("write envelope: %v", err)
	}

	st := mcp.TaskState{
		V:                1,
		TaskID:           taskID,
		State:            mcp.TaskStateRunning,
		StateTransitions: []mcp.StateTransition{{From: "", To: mcp.TaskStateQueued, At: now}, {From: mcp.TaskStateQueued, To: mcp.TaskStateRunning, At: now}},
		MaxRestarts:      3,
		DelegatorRole:    "coordinator",
		TargetRole:       repoName,
		UpdatedAt:        now,
		Worker: mcp.TaskWorker{
			Role:      repoName,
			PID:       workerPID,
			StartTime: workerStartTime,
		},
	}
	stBytes, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "state.json"), stBytes, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	// Mark the task as in-progress inside the worktree's role inbox. The
	// findRunningWorker scan only counts tasks whose in-progress envelope
	// lives under this worktree (so a task running in another worktree
	// doesn't trip the wait loop).
	inboxInProgress := filepath.Join(worktreePath, ".niwa", "roles", repoName, "inbox", "in-progress")
	if err := os.MkdirAll(inboxInProgress, 0o700); err != nil {
		t.Fatalf("mkdir in-progress: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxInProgress, taskID+".json"), envBytes, 0o600); err != nil {
		t.Fatalf("write in-progress: %v", err)
	}
	return taskID
}

// startSleepChild spawns `sleep <seconds>` and registers a cleanup to kill
// + reap it. Returns (pid, startTime). The child is placed in its own
// process group via Setpgid so the wait-for-worker tests can target it
// with `kill -<pid>` (process-group signal) the same way attach.go does
// in production. Without Setpgid the child inherits the test binary's
// PGID and the production code path would signal the wrong process group.
//
// A reaper goroutine calls cmd.Wait() immediately so a SIGTERM that kills
// the sleep child is reaped within milliseconds; otherwise the dead
// process lingers as a zombie and IsPIDAlive keeps returning true until
// t.Cleanup runs (which is after the test function returns — too late
// for assertions that poll PID liveness inside the test).
func startSleepChild(t *testing.T, seconds int) (int, int64) {
	t.Helper()
	cmd := exec.Command("sleep", fmt.Sprintf("%d", seconds))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Skipf("sleep not available: %v", err)
	}
	pid := cmd.Process.Pid
	startTime, _ := mcp.PIDStartTime(pid)
	reaped := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(reaped)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-reaped
	})
	return pid, startTime
}

// setupRunningWorkerInstance creates the workspace fixture
// findRunningWorker needs: mainInstanceRoot + a session worktree under it.
func setupRunningWorkerInstance(t *testing.T) (root, worktreePath, repoName string) {
	t.Helper()
	root = t.TempDir()
	repoName = "myrepo"
	worktreePath = filepath.Join(root, ".niwa", "worktrees", "myrepo-abcd1234")
	if err := os.MkdirAll(filepath.Join(worktreePath, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	return root, worktreePath, repoName
}

// TestFindRunningWorker_NoTasks asserts an empty task store reports no
// running worker — the baseline path the happy-attach scenario relies on.
func TestFindRunningWorker_NoTasks(t *testing.T) {
	root, worktree, _ := setupRunningWorkerInstance(t)
	if _, ok := findRunningWorker(root, worktree); ok {
		t.Errorf("expected no running worker in empty task store")
	}
}

// TestFindRunningWorker_LiveTaskInWorktreeInbox seeds one task as
// state=running with a live PID and an in-progress envelope in the
// session's inbox; findRunningWorker must surface it.
func TestFindRunningWorker_LiveTaskInWorktreeInbox(t *testing.T) {
	root, worktree, repo := setupRunningWorkerInstance(t)
	pid, start := startSleepChild(t, 30)
	taskID := seedRunningTaskFixture(t, root, worktree, repo, pid, start)

	got, ok := findRunningWorker(root, worktree)
	if !ok {
		t.Fatalf("expected findRunningWorker to surface a task")
	}
	if got.taskID != taskID {
		t.Errorf("taskID = %q, want %q", got.taskID, taskID)
	}
	if got.pid != pid {
		t.Errorf("pid = %d, want %d", got.pid, pid)
	}
}

// TestFindRunningWorker_DeadHolderIsSkipped asserts the PID-liveness check
// runs at the end: a task in state=running whose worker.pid is dead does
// not trip the wait loop.
func TestFindRunningWorker_DeadHolderIsSkipped(t *testing.T) {
	root, worktree, repo := setupRunningWorkerInstance(t)
	// Use os.Getpid() with a bogus start_time so IsPIDAlive returns false.
	seedRunningTaskFixture(t, root, worktree, repo, os.Getpid(), 1 /* bogus */)
	if _, ok := findRunningWorker(root, worktree); ok {
		t.Errorf("expected dead worker to be skipped")
	}
}

// TestHandleRunningWorker_NoWorkerNoOp confirms the function returns
// immediately (no wait, no kill) when nothing is running. This is the
// common case for the happy-path attach.
func TestHandleRunningWorker_NoWorkerNoOp(t *testing.T) {
	root, worktree, _ := setupRunningWorkerInstance(t)
	var stderr bytes.Buffer
	opts := Options{InstanceRoot: root, Force: false}
	if err := handleRunningWorker(context.Background(), opts, worktree, &stderr); err != nil {
		t.Errorf("unexpected err: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected silent stderr, got: %q", stderr.String())
	}
}

// TestHandleRunningWorker_ForceSIGTERMsWorker asserts the --force path:
// the running worker's process group is signalled and the function
// returns after the worker exits within the grace period. Tests AC13.
func TestHandleRunningWorker_ForceSIGTERMsWorker(t *testing.T) {
	root, worktree, repo := setupRunningWorkerInstance(t)
	pid, start := startSleepChild(t, 60)
	seedRunningTaskFixture(t, root, worktree, repo, pid, start)

	var stderr bytes.Buffer
	opts := Options{InstanceRoot: root, Force: true, GraceSeconds: 1}
	err := handleRunningWorker(context.Background(), opts, worktree, &stderr)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(stderr.String(), "--force: terminating worker on task") {
		t.Errorf("missing --force warning: %q", stderr.String())
	}
	// Confirm holder is dead.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !mcp.IsPIDAlive(pid, start) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("worker PID %d still alive after --force grace period", pid)
}

// TestHandleRunningWorker_WaitsForNaturalExit verifies the non-force path:
// without --force, handleRunningWorker polls until findRunningWorker stops
// returning the worker. Uses state.json mutation instead of PID death so the
// test isn't entangled with zombie-reap timing — the wait loop's contract is
// "poll until the worker is no longer running", which findRunningWorker
// reports via either a state-transition out of TaskStateRunning OR a dead
// PID.
func TestHandleRunningWorker_WaitsForNaturalExit(t *testing.T) {
	root, worktree, repo := setupRunningWorkerInstance(t)
	// Seed a running task pointing at our own PID (alive). The wait loop
	// will not terminate based on PID liveness; we'll mutate state.json to
	// state=completed to simulate a natural exit.
	myPID := os.Getpid()
	myStart, _ := mcp.PIDStartTime(myPID)
	taskID := seedRunningTaskFixture(t, root, worktree, repo, myPID, myStart)

	// After 400ms, transition the task to completed.
	taskDir := filepath.Join(root, ".niwa", "tasks", taskID)
	go func() {
		time.Sleep(400 * time.Millisecond)
		_ = mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
			next := *cur
			next.State = mcp.TaskStateCompleted
			next.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			return &next, nil, nil
		})
	}()

	var stderr bytes.Buffer
	opts := Options{
		InstanceRoot: root,
		Force:        false,
		PollInterval: 50 * time.Millisecond,
	}
	startTime := time.Now()
	if err := handleRunningWorker(context.Background(), opts, worktree, &stderr); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	elapsed := time.Since(startTime)
	if elapsed < 300*time.Millisecond {
		t.Errorf("returned too quickly (%s) — should have waited for the state transition", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Errorf("took too long (%s)", elapsed)
	}
}

// TestHandleRunningWorker_SIGINTAborts asserts AC14b: a SIGINT during the
// wait loop returns a 130 ExitCodeError so the operator can press Ctrl-C
// to abandon the attach attempt without disturbing the worker.
func TestHandleRunningWorker_SIGINTAborts(t *testing.T) {
	root, worktree, repo := setupRunningWorkerInstance(t)
	pid, start := startSleepChild(t, 60) // long-lived; would never naturally exit during test
	seedRunningTaskFixture(t, root, worktree, repo, pid, start)

	// Use ctx cancellation as the abort signal — handleRunningWorker selects
	// on ctx.Done(), which is the same control flow as the SIGINT handler.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	var stderr bytes.Buffer
	opts := Options{
		InstanceRoot: root,
		Force:        false,
		PollInterval: 50 * time.Millisecond,
	}
	err := handleRunningWorker(ctx, opts, worktree, &stderr)
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("want ExitCodeError, got %v", err)
	}
	if ece.Code != 130 {
		t.Errorf("Code = %d, want 130 (SIGINT abort per AC14b)", ece.Code)
	}
	// Worker should still be alive — abort must not signal the worker.
	if !mcp.IsPIDAlive(pid, start) {
		t.Errorf("worker was killed by abort path; abort must leave the worker untouched")
	}
}
