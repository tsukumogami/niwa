package workspace

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// ---------------------------------------------------------------------
// destroyGraceFromEnv
// ---------------------------------------------------------------------

func TestDestroyGraceFromEnv_Default(t *testing.T) {
	t.Setenv("NIWA_DESTROY_GRACE_SECONDS", "")
	if got := destroyGraceFromEnv(); got != 5*time.Second {
		t.Errorf("default = %v, want 5s", got)
	}
}

func TestDestroyGraceFromEnv_Override(t *testing.T) {
	t.Setenv("NIWA_DESTROY_GRACE_SECONDS", "2")
	if got := destroyGraceFromEnv(); got != 2*time.Second {
		t.Errorf("override = %v, want 2s", got)
	}
}

func TestDestroyGraceFromEnv_InvalidFallsBackToDefault(t *testing.T) {
	t.Setenv("NIWA_DESTROY_GRACE_SECONDS", "abc")
	if got := destroyGraceFromEnv(); got != 5*time.Second {
		t.Errorf("invalid = %v, want 5s default", got)
	}
}

// ---------------------------------------------------------------------
// TerminateDaemon destroy-sequence hardening (Issue 8)
// ---------------------------------------------------------------------

// fakeWorker launches /bin/sleep as an own-session process group so
// negative-PID signals target exactly that one process. Returns the
// *exec.Cmd; cleanup via t.Cleanup.
func fakeWorker(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("/bin/sleep", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting fake worker: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
		}
	})
	return cmd
}

// seedRunningTask writes a minimal .niwa/tasks/<id>/state.json with
// state="running" and worker.pid set. envelope.json is also written
// because ReadState validates it on every read.
func seedRunningTask(t *testing.T, niwaDir, role string, workerPID int) string {
	t.Helper()
	taskID := mcp.NewTaskID()
	tasksDir := filepath.Join(niwaDir, "tasks")
	taskDir := filepath.Join(tasksDir, taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatalf("mkdir task dir: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	env := mcp.TaskEnvelope{
		V:      1,
		ID:     taskID,
		From:   mcp.TaskParty{Role: "coordinator", PID: os.Getpid()},
		To:     mcp.TaskParty{Role: role},
		Body:   json.RawMessage(`{"kind":"test"}`),
		SentAt: now,
	}
	envBytes, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "envelope.json"), envBytes, 0o600); err != nil {
		t.Fatalf("write envelope: %v", err)
	}

	startTime, _ := mcp.PIDStartTime(workerPID)
	st := &mcp.TaskState{
		V:      1,
		TaskID: taskID,
		State:  mcp.TaskStateRunning,
		StateTransitions: []mcp.StateTransition{
			{From: "", To: mcp.TaskStateQueued, At: now},
			{From: mcp.TaskStateQueued, To: mcp.TaskStateRunning, At: now},
		},
		MaxRestarts:   3,
		DelegatorRole: "coordinator",
		TargetRole:    role,
		Worker: mcp.TaskWorker{
			PID:            workerPID,
			StartTime:      startTime,
			Role:           role,
			SpawnStartedAt: now,
		},
		UpdatedAt: now,
	}
	stBytes, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "state.json"), stBytes, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if fh, err := os.Create(filepath.Join(taskDir, ".lock")); err == nil {
		_ = fh.Close()
	}
	return taskID
}

// waitForCmdExit polls cmd.Process.Wait (in a goroutine) until either
// the process exits or the timeout elapses. Returns true when the
// process has terminated. Using Wait (instead of syscall.Kill(pid, 0))
// avoids zombie-false-positives: a dead child shows as "signal 0"
// succeeds until reaped.
func waitForCmdExit(cmd *exec.Cmd, timeout time.Duration) bool {
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// TestTerminateDaemon_SigKillsWorkersFirst: a "running" task with a
// live worker ignoring SIGTERM must be SIGKILLed via its process group
// before the daemon grace period elapses. No daemon.pid is present, so
// the daemon branch is a no-op — this test is the isolated worker-kill
// assertion.
func TestTerminateDaemon_SigKillsWorkersFirst(t *testing.T) {
	instanceRoot := t.TempDir()
	niwaDir := filepath.Join(instanceRoot, ".niwa")
	if err := os.MkdirAll(filepath.Join(niwaDir, "tasks"), 0o700); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}

	// Worker that ignores SIGTERM so only SIGKILL will take it down.
	cmd := exec.Command("/bin/sh", "-c", "trap '' TERM; sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting worker: %v", err)
	}
	pid := cmd.Process.Pid
	// NOTE: no defer-kill cleanup here — waitForCmdExit below will
	// reap the process as part of the assertion. A double Wait panics
	// on some platforms.

	_ = seedRunningTask(t, niwaDir, "web", pid)

	// With no daemon.pid present, TerminateDaemon's daemon branch is a
	// no-op; measure the time to worker death.
	start := time.Now()
	if err := TerminateDaemon(instanceRoot); err != nil {
		t.Fatalf("TerminateDaemon: %v", err)
	}
	elapsed := time.Since(start)

	if !waitForCmdExit(cmd, 2*time.Second) {
		// Best-effort cleanup so the subprocess doesn't leak.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
		t.Fatal("worker did not exit after TerminateDaemon")
	}
	// Even with the default 5s grace, the worker must be dead before
	// the grace window could have elapsed. The signal is synchronous
	// (syscall.Kill), so in practice this should complete in
	// milliseconds — well under 1 second on any reasonable host.
	if elapsed > 2*time.Second {
		t.Errorf("TerminateDaemon took %v; worker should have been SIGKILLed immediately", elapsed)
	}
}

// TestTerminateDaemon_SkipsNonRunningTasks: tasks whose state is NOT
// "running" (queued, completed, abandoned) must not be killed. A worker
// PID belonging to another (live) process MUST NOT receive SIGKILL
// because of a stale state.json.
func TestTerminateDaemon_SkipsNonRunningTasks(t *testing.T) {
	instanceRoot := t.TempDir()
	niwaDir := filepath.Join(instanceRoot, ".niwa")
	if err := os.MkdirAll(filepath.Join(niwaDir, "tasks"), 0o700); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}

	// Live process that must survive — we'll write state.json saying
	// task is "completed" with this PID. TerminateDaemon must not kill
	// a completed task's worker.
	cmd := fakeWorker(t, "30")

	taskID := mcp.NewTaskID()
	taskDir := filepath.Join(niwaDir, "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatalf("mkdir task dir: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	env := mcp.TaskEnvelope{
		V:      1,
		ID:     taskID,
		From:   mcp.TaskParty{Role: "coordinator", PID: os.Getpid()},
		To:     mcp.TaskParty{Role: "web"},
		Body:   json.RawMessage(`{"kind":"test"}`),
		SentAt: now,
	}
	envBytes, _ := json.MarshalIndent(env, "", "  ")
	_ = os.WriteFile(filepath.Join(taskDir, "envelope.json"), envBytes, 0o600)
	st := &mcp.TaskState{
		V:             1,
		TaskID:        taskID,
		State:         mcp.TaskStateCompleted,
		DelegatorRole: "coordinator",
		TargetRole:    "web",
		MaxRestarts:   3,
		Worker: mcp.TaskWorker{
			PID:  cmd.Process.Pid,
			Role: "web",
		},
		UpdatedAt: now,
	}
	stBytes, _ := json.MarshalIndent(st, "", "  ")
	_ = os.WriteFile(filepath.Join(taskDir, "state.json"), stBytes, 0o600)

	if err := TerminateDaemon(instanceRoot); err != nil {
		t.Fatalf("TerminateDaemon: %v", err)
	}

	// Give the kernel a beat to deliver any signal.
	time.Sleep(200 * time.Millisecond)
	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Errorf("completed-task worker was killed by TerminateDaemon (err=%v)", err)
	}
}

// TestTerminateDaemon_DaemonGraceStillHonored: with a live daemon
// (simulated via a sleep subprocess) whose PID is written to daemon.pid
// and whose SIGTERM is ignored, TerminateDaemon should take at least
// ~NIWA_DESTROY_GRACE_SECONDS before SIGKILLing. Using grace=1s for
// fast CI.
func TestTerminateDaemon_DaemonGraceStillHonored(t *testing.T) {
	t.Setenv("NIWA_DESTROY_GRACE_SECONDS", "1")

	instanceRoot := t.TempDir()
	niwaDir := filepath.Join(instanceRoot, ".niwa")
	if err := os.MkdirAll(filepath.Join(niwaDir, "tasks"), 0o700); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}

	// Simulate a daemon that ignores SIGTERM.
	daemon := exec.Command("/bin/sh", "-c", "trap '' TERM; sleep 30")
	daemon.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := daemon.Start(); err != nil {
		t.Fatalf("starting fake daemon: %v", err)
	}
	daemonPID := daemon.Process.Pid

	startTime, _ := mcp.PIDStartTime(daemonPID)
	pidContent := strconv.Itoa(daemonPID) + "\n" + strconv.FormatInt(startTime, 10) + "\n"
	pidPath := filepath.Join(niwaDir, "daemon.pid")
	if err := os.WriteFile(pidPath, []byte(pidContent), 0o600); err != nil {
		t.Fatalf("writing daemon.pid: %v", err)
	}

	start := time.Now()
	if err := TerminateDaemon(instanceRoot); err != nil {
		t.Fatalf("TerminateDaemon: %v", err)
	}
	elapsed := time.Since(start)

	// Must have taken at least ~grace window (1 s) before escalating
	// to SIGKILL. Allow a small lower-bound slack for poll granularity.
	if elapsed < 800*time.Millisecond {
		t.Errorf("elapsed=%v, daemon grace window was NOT honored (want ~1s)", elapsed)
	}

	// Daemon must now exit. Use Wait to reap; a lingering zombie would
	// otherwise make syscall.Kill(pid, 0) return nil.
	if !waitForCmdExit(daemon, 2*time.Second) {
		_ = syscall.Kill(-daemonPID, syscall.SIGKILL)
		_, _ = daemon.Process.Wait()
		t.Error("daemon did not exit after TerminateDaemon + grace")
	}

	// daemon.pid must be removed.
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Errorf("daemon.pid still present after destroy: %v", err)
	}
}

// TestTerminateDaemon_WorkerKilledBeforeDaemonGrace is the end-to-end
// "security hardening" assertion: with BOTH a live worker ignoring
// SIGTERM AND a live daemon ignoring SIGTERM, the worker must be dead
// long before the daemon grace window elapses. This is the contract
// Issue 8 adds — the worker's acceptEdits-enabled exfiltration window
// is bounded by the speed of syscall.Kill, not the daemon grace.
func TestTerminateDaemon_WorkerKilledBeforeDaemonGrace(t *testing.T) {
	// Use a generous grace so timing assertions have room on slow CI.
	t.Setenv("NIWA_DESTROY_GRACE_SECONDS", "2")

	instanceRoot := t.TempDir()
	niwaDir := filepath.Join(instanceRoot, ".niwa")
	if err := os.MkdirAll(filepath.Join(niwaDir, "tasks"), 0o700); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}

	// Worker (SIGTERM-ignoring) and daemon (SIGTERM-ignoring).
	worker := exec.Command("/bin/sh", "-c", "trap '' TERM; sleep 30")
	worker.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := worker.Start(); err != nil {
		t.Fatalf("worker start: %v", err)
	}
	workerPID := worker.Process.Pid

	daemon := exec.Command("/bin/sh", "-c", "trap '' TERM; sleep 30")
	daemon.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := daemon.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	daemonPID := daemon.Process.Pid

	// Seed the "running" task referencing the worker.
	_ = seedRunningTask(t, niwaDir, "web", workerPID)

	// Write daemon.pid.
	startTime, _ := mcp.PIDStartTime(daemonPID)
	pidContent := strconv.Itoa(daemonPID) + "\n" + strconv.FormatInt(startTime, 10) + "\n"
	_ = os.WriteFile(filepath.Join(niwaDir, "daemon.pid"), []byte(pidContent), 0o600)

	// Start TerminateDaemon asynchronously so we can observe the
	// worker's death BEFORE TerminateDaemon returns.
	terminateDone := make(chan error, 1)
	go func() {
		terminateDone <- TerminateDaemon(instanceRoot)
	}()

	// Worker should die immediately (SIGKILL first), well before the
	// 2s daemon grace window elapses.
	workerDead := make(chan bool, 1)
	go func() {
		workerDead <- waitForCmdExit(worker, 1*time.Second)
	}()

	select {
	case ok := <-workerDead:
		if !ok {
			_ = syscall.Kill(-workerPID, syscall.SIGKILL)
			_, _ = worker.Process.Wait()
			_ = syscall.Kill(-daemonPID, syscall.SIGKILL)
			_, _ = daemon.Process.Wait()
			t.Fatal("worker did not exit within 1s (should be SIGKILLed immediately)")
		}
	case <-time.After(1500 * time.Millisecond):
		_ = syscall.Kill(-workerPID, syscall.SIGKILL)
		_, _ = worker.Process.Wait()
		_ = syscall.Kill(-daemonPID, syscall.SIGKILL)
		_, _ = daemon.Process.Wait()
		t.Fatal("worker kill detection timed out")
	}

	// Now wait for TerminateDaemon to finish. It should still be
	// honoring the daemon grace window — confirming the ordering.
	select {
	case err := <-terminateDone:
		if err != nil {
			t.Errorf("TerminateDaemon: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-daemonPID, syscall.SIGKILL)
		_, _ = daemon.Process.Wait()
		t.Fatal("TerminateDaemon did not return within 5s")
	}

	// Reap the daemon.
	if !waitForCmdExit(daemon, 2*time.Second) {
		_ = syscall.Kill(-daemonPID, syscall.SIGKILL)
		_, _ = daemon.Process.Wait()
		t.Error("daemon did not exit after TerminateDaemon")
	}
}

// TestTerminateDaemon_NoDaemonPresent: no daemon.pid, no tasks — the
// call must succeed silently with no errors.
func TestTerminateDaemon_NoDaemonPresent(t *testing.T) {
	instanceRoot := t.TempDir()
	niwaDir := filepath.Join(instanceRoot, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o700); err != nil {
		t.Fatalf("mkdir niwa: %v", err)
	}
	if err := TerminateDaemon(instanceRoot); err != nil {
		t.Errorf("TerminateDaemon with no daemon: %v", err)
	}
}
