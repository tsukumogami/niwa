package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// Concurrency (Issue 7 / AC-C3): the PID read happens under a shared
// flock on `.niwa/daemon.pid.lock`. The daemon itself holds the
// exclusive flock for its lifetime, so a live daemon is observable as
// "shared lock takes instantly, PID file present, PID alive". Two
// concurrent `niwa apply` invocations against an unchanneled workspace
// will both attempt to spawn; the spawned daemons race for the
// exclusive flock and the loser exits cleanly with code 0.
func EnsureDaemonRunning(instanceRoot string) error {
	niwaDir := filepath.Join(instanceRoot, ".niwa")
	pid, startTime, err := readPIDUnderSharedFlock(niwaDir)
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

// TerminateDaemon sends SIGTERM to the mesh watch daemon for instanceRoot,
// polls IsPIDAlive for up to 5 seconds, then sends SIGKILL if still alive.
// It removes daemon.pid once the daemon is confirmed dead or was never running.
func TerminateDaemon(instanceRoot string) error {
	niwaDir := filepath.Join(instanceRoot, ".niwa")
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

	// Send SIGTERM and poll for up to 5 seconds.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		_ = os.Remove(filepath.Join(niwaDir, "daemon.pid"))
		return nil
	}

	deadline := time.Now().Add(5 * time.Second)
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

// readPIDUnderSharedFlock acquires a shared flock on
// `<niwaDir>/daemon.pid.lock`, reads daemon.pid, and releases the flock.
//
// The shared flock pairs with the exclusive flock the daemon holds for
// its lifetime (see cli.acquireDaemonPIDLock). A live daemon is always
// observable as "shared lock succeeds" (multiple readers coexist) +
// "PID alive"; a crashed daemon releases the exclusive lock when the
// kernel closes its fd, letting a subsequent EnsureDaemonRunning reclaim.
//
// The shared flock is non-blocking (LOCK_NB): if the lock file does not
// exist yet (brand-new instance, first `niwa apply`) we fall straight
// through to ReadPIDFile, which returns (0, 0, nil) for a missing file.
// Lock-acquire errors are non-fatal — a missing or unreadable lock file
// should not break `niwa apply`; we just skip the cross-check and fall
// back to the PID file on its own.
func readPIDUnderSharedFlock(niwaDir string) (int, int64, error) {
	lockPath := filepath.Join(niwaDir, "daemon.pid.lock")
	lf, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if err != nil {
		if os.IsNotExist(err) {
			// No lock file ⇒ no daemon has ever run here. Fall back
			// to ReadPIDFile which will also find nothing.
			return ReadPIDFile(niwaDir)
		}
		// Any other error: fall back to the raw PID read. A missing
		// lock file is not a failure mode we want to surface to users.
		return ReadPIDFile(niwaDir)
	}
	defer lf.Close()

	// Shared flock. LOCK_NB so we do not block the apply path on a
	// stuck holder. If a legitimate daemon is alive the shared lock
	// succeeds immediately (shared + exclusive coexist only if both are
	// shared, so a live daemon's LOCK_EX here would block us — but
	// LOCK_NB turns that into EWOULDBLOCK, at which point the PID file
	// read below is authoritative enough for the "is it alive?" check).
	_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	defer func() { _ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) }()

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
