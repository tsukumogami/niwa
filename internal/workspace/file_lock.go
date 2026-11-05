package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
)

// ErrLockHeld is returned by TryFileLock when another holder has the lock. The
// error that wraps it names the holder, as read from the lock file with every
// control character removed, or "another niwa command" when the holder has not
// written its line yet.
var ErrLockHeld = errors.New("lock is held")

// ErrLockUnsupported is returned by TryFileLock on a platform without flock.
// Callers decide what an unlockable platform means for them; nothing is
// substituted for the lock.
var ErrLockUnsupported = errors.New("file locking is not supported on this platform")

// InstanceLockPath returns the per-instance lock file for instancePath, in
// niwa's own lock directory under home (~/.niwa/locks).
//
// The lock never lives inside the instance or the workspace's .niwa/: destroy
// removes the instance directory and a config refresh replaces .niwa/ whole,
// and either would give the lock file a new inode under a holder, so a second
// taker would lock a different file. The file name is a hash of the instance
// path after CanonicalPath, so the same instance named through a symlinked
// root, a trailing slash or a ".." step maps to one lock. The name is "i-"
// and the hash alone, so it carries no component of the path.
func InstanceLockPath(home, instancePath string) string {
	sum := sha256.Sum256([]byte(CanonicalPath(instancePath)))
	return filepath.Join(home, ".niwa", "locks", "i-"+hex.EncodeToString(sum[:])[:32]+".lock")
}

// CanonicalPath returns p made absolute, cleaned and with every symlink along
// it resolved. A path that no longer exists (an instance already removed) is
// resolved as far as its nearest existing ancestor, with the rest rejoined, so
// the answer for a path stays the same after the path is deleted.
func CanonicalPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Clean(resolveExistingPrefix(filepath.Clean(p)))
}
