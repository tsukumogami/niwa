package worktree

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"syscall"
)

// IsPIDAlive returns true if the given PID exists and its recorded start time
// matches, preventing false positives from PID recycling.
func IsPIDAlive(pid int, startTime int64) bool {
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// os.FindProcess on Unix always succeeds; use kill(0) via /proc.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	if startTime == 0 {
		return true
	}
	recorded, err := pidStartTime(pid)
	if err != nil {
		// Can't verify start time — be conservative and say alive.
		return true
	}
	return recorded == startTime
}

// processSignal is the test seam for IsProcessAlive. Production callers
// route through os.FindProcess + proc.Signal(0); tests override this
// variable to inject EPERM or other errors without needing a root-owned
// process. The seam is package-private so external callers cannot
// reach it; tests share the package and patch the variable directly.
var processSignal = func(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.Signal(0))
}

// IsProcessAlive reports whether a process with the given PID is alive,
// using only a PID (no start-time gate). Implements a fail-closed Signal(0)
// liveness probe:
//
//   - nil error: process exists and we have permission to signal it → alive.
//   - os.ErrProcessDone or syscall.ESRCH: process is gone → dead.
//   - syscall.EPERM (different UID) or any unknown error → fail-closed,
//     report alive so a UID-mismatch race never reaps a live holder.
//
// Unlike IsPIDAlive, which combines PID with a start-time check for
// recycle-safe PID files, this helper omits the start-time gate and assumes
// the caller holds a short-lived lock where PID recycling is statistically
// improbable.
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := processSignal(pid)
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return false
	}
	// syscall.EPERM (different UID, can't signal) and any unknown error
	// path fail closed: assume alive so the reaper never races a live
	// holder out of its lock.
	return true
}

// PPIDChain returns up to n PPIDs walking upward from the current process.
// The first element is os.Getppid(); the second (if n >= 2) is that PID's
// parent per readPPID, and so on.
//
// Walking stops as soon as a PID ≤ 1 is reached (init/kthreadd). A PID in
// the chain that does not exist surfaces as a structured error so callers
// can distinguish "no parent present" from "read failed".
//
// Levels beyond the first need platform process introspection (procfs on
// Linux, the kern.proc.pid sysctl on Darwin). On a platform with neither,
// only level 1 resolves and deeper walks return an error.
func PPIDChain(n int) ([]int, error) {
	if n <= 0 {
		return nil, fmt.Errorf("PPIDChain: n must be >= 1, got %d", n)
	}
	chain := make([]int, 0, n)

	// Level 1: cross-platform via os.Getppid.
	ppid := os.Getppid()
	if ppid <= 1 {
		return nil, fmt.Errorf("PPIDChain: no parent PID (got %d)", ppid)
	}
	chain = append(chain, ppid)

	// Deeper levels: platform process introspection.
	for i := 1; i < n; i++ {
		parent := readPPID(chain[i-1])
		if parent == 0 {
			return chain, fmt.Errorf(
				"PPIDChain: cannot read parent of PID %d (level %d)", chain[i-1], i+1)
		}
		if parent <= 1 {
			return chain, fmt.Errorf(
				"PPIDChain: reached init at level %d", i+1)
		}
		chain = append(chain, parent)
	}
	return chain, nil
}

// NewSessionID generates a random session UUID for use during registration.
func NewSessionID() string { return newUUID() }

// NewUUID generates a random UUIDv4 (no external dependencies). Exported so
// other packages can share one UUIDv4 generator for entity IDs without
// re-implementing the crypto/rand formatting.
func NewUUID() string { return newUUID() }

// PIDStartTime returns an opaque, platform-specific process start-time
// fingerprint for a PID (jiffies since boot on Linux, microseconds since the
// epoch on Darwin). Exported so callers outside the package can record a
// recycle-safe PID fingerprint without doing the platform lookup themselves.
// Compare values only against other values from the same machine and OS.
func PIDStartTime(pid int) (int64, error) { return pidStartTime(pid) }

// newUUID generates a random UUID v4 without external dependencies.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
