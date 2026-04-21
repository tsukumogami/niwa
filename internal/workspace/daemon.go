package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// EnsureDaemonRunning spawns the mesh watch daemon for instanceRoot if one is
// not already alive. It locates the niwa binary via exec.LookPath, so the
// daemon inherits the same binary as the running process.
//
// The daemon is spawned with Setsid=true so it is fully detached from the
// current terminal session. stdout/stderr are appended to daemon.log.
// cmd.Start() is used (not Run()) so the function returns immediately.
func EnsureDaemonRunning(instanceRoot string) error {
	niwaDir := filepath.Join(instanceRoot, ".niwa")
	pid, startTime, err := readPIDFile(niwaDir)
	if err != nil {
		// Non-fatal: treat as no daemon.
		pid = 0
		startTime = 0
	}

	if pid != 0 && mcp.IsPIDAlive(pid, startTime) {
		return nil // daemon already running
	}

	// Find the niwa binary (same executable as ourselves).
	niwaBin, err := exec.LookPath("niwa")
	if err != nil {
		// Fallback: use os.Executable.
		niwaBin, err = os.Executable()
		if err != nil {
			return fmt.Errorf("cannot find niwa binary: %w", err)
		}
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

	return nil
}

// readPIDFile reads <niwaDir>/daemon.pid and returns (pid, startTime, err).
// Returns (0, 0, nil) if the file does not exist.
func readPIDFile(niwaDir string) (pid int, startTime int64, err error) {
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
