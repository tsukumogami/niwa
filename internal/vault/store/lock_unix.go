//go:build unix

package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"time"
)

const (
	oNoFollow  = syscall.O_NOFOLLOW
	oDirectory = syscall.O_DIRECTORY
)

// acquireLock takes an exclusive flock on f, trying without blocking
// every poll until timeout has passed, since a blocking flock has no
// deadline to give. flock belongs to the open file description, so two
// acquisitions in one process contend exactly as two processes do, and
// the kernel releases the lock when its holder dies.
func acquireLock(f *os.File, timeout, poll time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			// Some network filesystems don't support flock at all.
			return fmt.Errorf("%w: locking: %w", ErrUnwritable, err)
		}
		left := time.Until(deadline)
		if left <= 0 {
			return fmt.Errorf("%w after %s", ErrLockTimeout, timeout)
		}
		time.Sleep(min(poll, left))
	}
}

func releaseLock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// fileOwner returns the uid that owns fi.
func fileOwner(fi fs.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
