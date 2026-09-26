//go:build !linux && !darwin

package worktree

import (
	"fmt"
	"runtime"
)

// pidStartTime has no portable implementation outside Linux and Darwin.
// Callers treat the error as "cannot verify" and fall back to a PID-only
// liveness check.
func pidStartTime(pid int) (int64, error) {
	return 0, fmt.Errorf("pidStartTime: unsupported on %s", runtime.GOOS)
}

// readPPID has no portable implementation outside Linux and Darwin; 0 means
// "unknown parent" to callers.
func readPPID(pid int) int { return 0 }
