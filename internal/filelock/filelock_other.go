//go:build !unix

package filelock

import (
	"context"
	"time"
)

// TryFileLock has no lock to take here. It reports ErrLockUnsupported so the
// caller decides, rather than proceeding as if it held the lock.
func TryFileLock(string, string) (func(), error) {
	return nil, ErrLockUnsupported
}

// WaitFileLock has no lock to wait for here either; see TryFileLock.
func WaitFileLock(context.Context, string, string, time.Duration) (func(), error) {
	return nil, ErrLockUnsupported
}
