//go:build unix

package workspace

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

const (
	// codexTrustLockTimeout bounds how long one apply waits for another to
	// finish its read-modify-write. Generous against a slow disk, short
	// enough that a stuck holder surfaces as an error rather than an apply
	// that never returns.
	codexTrustLockTimeout = 30 * time.Second

	// codexTrustLockPoll is the interval between acquisition attempts. The
	// lock is taken non-blocking and retried so the deadline above is real:
	// a blocking flock has no timeout to give.
	codexTrustLockPoll = 20 * time.Millisecond
)

// acquireCodexTrustLock takes an exclusive advisory lock on lockPath and
// returns the release. flock associates the lock with the open file
// description, so two acquisitions in one process contend exactly as two
// processes do.
func acquireCodexTrustLock(lockPath string) (func(), error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the Codex trust lock %s: %w", lockPath, err)
	}

	deadline := time.Now().Add(codexTrustLockTimeout)
	for {
		lockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if lockErr == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(lockErr, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("locking the Codex trust lock %s: %w", lockPath, lockErr)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf(
				"timed out after %s waiting for another niwa apply to finish writing Codex trust entries (lock %s)",
				codexTrustLockTimeout, lockPath)
		}
		time.Sleep(codexTrustLockPoll)
	}
}
