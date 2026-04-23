package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// EnsureDaemonRunning spawns the mesh watch daemon for instanceRoot if one is
// not already alive. It uses os.Executable() to locate the daemon binary so
// the daemon always runs the same binary version as the caller — exec.LookPath
// would find an installed system binary that may differ (e.g. in tests, the
// running binary is niwa-test, not the installed niwa).
//
// The daemon is spawned with Setsid=true so it is fully detached from the
// current terminal session. stdout/stderr are appended to daemon.log.
// cmd.Start() is used (not Run()) so the function returns immediately.
// After spawning, the function polls for daemon.pid for up to 500ms so
// callers can assert daemon liveness right after Create/Apply returns.
//
// Concurrency (Issue 7 / AC-C3): the PID read is best-effort — the
// daemon writes daemon.pid atomically (tmp + rename) and IsPIDAlive
// cross-checks against /proc. Two concurrent `niwa apply` invocations
// against an unchanneled workspace may both attempt to spawn; the
// spawned daemons race for the exclusive flock on daemon.pid.lock
// (acquired by the daemon itself at startup — see cli.acquireDaemonPIDLock)
// and the loser exits cleanly with code 0.
func EnsureDaemonRunning(instanceRoot string) error {
	niwaDir := filepath.Join(instanceRoot, ".niwa")
	pid, startTime, err := readPIDBestEffort(niwaDir)
	if err != nil {
		// Non-fatal: treat as no daemon.
		pid = 0
		startTime = 0
	}

	if pid != 0 && mcp.IsPIDAlive(pid, startTime) {
		return nil // daemon already running
	}

	// Use the current executable as the daemon binary so the daemon is always
	// the same version as the process that spawned it. exec.LookPath("niwa")
	// would find a system-installed binary that may be a different version.
	niwaBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine own binary path: %w", err)
	}

	// Ensure .niwa dir and log file are accessible.
	if err := os.MkdirAll(niwaDir, 0o700); err != nil {
		return fmt.Errorf("creating .niwa dir: %w", err)
	}

	logPath := filepath.Join(niwaDir, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening daemon log: %w", err)
	}
	// logFile is intentionally not closed here: the child process inherits the fd,
	// and os.Process tracks it. We close our end via defer.
	defer logFile.Close()

	cmd := exec.Command(niwaBin, "mesh", "watch", "--instance-root="+instanceRoot)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting mesh daemon: %w", err)
	}

	// Release our reference; the child runs independently.
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}

	// Wait for the daemon to write its PID file before returning. The daemon
	// writes daemon.pid atomically after establishing the fsnotify watcher, so
	// its presence confirms the watch loop is running. Poll up to 500ms — the
	// daemon writes the PID file in well under 100ms in normal conditions.
	pidPath := filepath.Join(niwaDir, "daemon.pid")
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pidPath); err == nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Timed out — daemon may have failed to start (e.g. missing fsnotify
	// support). Return nil so Create/Apply still succeed; the missing PID
	// file is the observable failure signal.
	return nil
}

// TerminateDaemon drives the `niwa destroy` teardown sequence:
//
//  1. SIGKILL every running worker's process group (negative PID signal)
//     FIRST, without any grace window. This bounds the attack surface of
//     a compromised worker running with `--permission-mode=acceptEdits`
//     — the daemon's grace period would otherwise give such a worker a
//     5-second exfiltration window during teardown (Issue 8 / DESIGN
//     Known Limitation: acceptEdits blast radius).
//  2. SIGTERM the daemon process.
//  3. Wait up to NIWA_DESTROY_GRACE_SECONDS (default 5 s) for a clean
//     exit. The daemon keeps its grace period so in-flight state.json
//     writes and transitions.log fsyncs can complete cleanly.
//  4. SIGKILL the daemon if still alive.
//  5. Remove daemon.pid.
func TerminateDaemon(instanceRoot string) error {
	niwaDir := filepath.Join(instanceRoot, ".niwa")

	// Step 1: SIGKILL all running worker PGIDs BEFORE touching the
	// daemon. This must run even when the daemon is already gone — a
	// crashed daemon may leave orphan workers whose process groups are
	// still live and whose acceptEdits permission is still in force.
	killRunningWorkerPGIDs(niwaDir)

	// Step 2+: shut down the daemon itself.
	pid, startTime, err := ReadPIDFile(niwaDir)
	if err != nil {
		return fmt.Errorf("reading daemon pid: %w", err)
	}
	if pid == 0 {
		return nil // no daemon running
	}

	if !mcp.IsPIDAlive(pid, startTime) {
		_ = os.Remove(filepath.Join(niwaDir, "daemon.pid"))
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(filepath.Join(niwaDir, "daemon.pid"))
		return nil
	}

	// Send SIGTERM and poll for up to NIWA_DESTROY_GRACE_SECONDS.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		_ = os.Remove(filepath.Join(niwaDir, "daemon.pid"))
		return nil
	}

	grace := destroyGraceFromEnv()
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if !mcp.IsPIDAlive(pid, startTime) {
			_ = os.Remove(filepath.Join(niwaDir, "daemon.pid"))
			return nil
		}
	}

	// Still alive: send SIGKILL.
	_ = proc.Signal(syscall.SIGKILL)
	_ = os.Remove(filepath.Join(niwaDir, "daemon.pid"))
	return nil
}

// destroyGraceFromEnv returns the grace window between SIGTERM and
// SIGKILL for the daemon. Controlled by NIWA_DESTROY_GRACE_SECONDS;
// default 5 s. Invalid values fall back to the default silently — the
// destroy path never refuses to proceed over a bad override.
func destroyGraceFromEnv() time.Duration {
	const def = 5 * time.Second
	raw := os.Getenv("NIWA_DESTROY_GRACE_SECONDS")
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return def
	}
	return time.Duration(v) * time.Second
}

// killRunningWorkerPGIDs enumerates .niwa/tasks/*/state.json and sends
// SIGKILL to the process group (negative PID) of every task whose state
// is "running" and whose worker.pid > 0. This is the "worker SIGKILL
// first" half of the destroy-phase hardening (Issue 8): workers get no
// grace period so an `acceptEdits`-enabled worker cannot exfiltrate
// during the daemon's SIGTERM → SIGKILL window.
//
// Errors are intentionally silent: this path runs during teardown and
// must proceed opportunistically. A missing state.json, a worker that
// has already exited, a PID that no longer belongs to that worker — all
// of these are acceptable outcomes (the goal is "this PID is not
// writing anymore", not "we observed a clean kill").
func killRunningWorkerPGIDs(niwaDir string) {
	tasksDir := filepath.Join(niwaDir, "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return // no tasks dir = nothing to kill
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		taskDir := filepath.Join(tasksDir, ent.Name())
		_, st, err := mcp.ReadState(taskDir)
		if err != nil {
			continue
		}
		if st.State != mcp.TaskStateRunning {
			continue
		}
		if st.Worker.PID <= 0 {
			continue
		}
		// Negative PID => signal the entire process group. Workers are
		// spawned with Setsid=true so worker.pid is also the PGID.
		_ = syscall.Kill(-st.Worker.PID, syscall.SIGKILL)
	}
}

// readPIDBestEffort reads `<niwaDir>/daemon.pid` without holding any
// flock. The daemon writes daemon.pid atomically (tmp + rename) and
// IsPIDAlive cross-checks the recorded PID against /proc, so a lock-
// less read is sufficient: stale contents simply look like "no daemon
// running" to the caller, which re-spawns and lets the exclusive
// flock (cli.acquireDaemonPIDLock) resolve the race.
//
// A missing file maps to (0, 0, nil) via ReadPIDFile; read errors are
// propagated so callers can log them but, in practice, EnsureDaemonRunning
// treats any read failure as "no daemon" and falls through to spawn.
func readPIDBestEffort(niwaDir string) (int, int64, error) {
	return ReadPIDFile(niwaDir)
}

// ReadPIDFile reads <niwaDir>/daemon.pid and returns (pid, startTime, err).
// Returns (0, 0, nil) if the file does not exist.
func ReadPIDFile(niwaDir string) (pid int, startTime int64, err error) {
	pidPath := filepath.Join(niwaDir, "daemon.pid")
	data, err := os.ReadFile(pidPath)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 1 {
		return 0, 0, fmt.Errorf("daemon.pid: empty file")
	}

	var p int
	if _, err := fmt.Sscanf(lines[0], "%d", &p); err != nil {
		return 0, 0, fmt.Errorf("daemon.pid: invalid pid: %w", err)
	}

	var st int64
	if len(lines) >= 2 {
		if _, err := fmt.Sscanf(lines[1], "%d", &st); err != nil {
			st = 0
		}
	}

	return p, st, nil
}
