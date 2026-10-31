//go:build !unix

package workspace

// Platforms without flock get no substitute lock. A lock file created and
// removed by hand would serialize nothing under contention -- the window
// between the check and the create is the whole race -- and pretending
// otherwise is worse than the honest gap. Every file these locks guard is
// still replaced atomically, so a concurrent apply can lose the other's write
// but never leaves a truncated or invalid document.
func acquireFileLock(string, string) (func(), error) {
	return func() {}, nil
}

// TryFileLock has no lock to take here. It reports ErrLockUnsupported so the
// caller decides, rather than proceeding as if it held the lock.
func TryFileLock(string, string) (func(), error) {
	return nil, ErrLockUnsupported
}
