package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/filelock"
)

// ErrLockHeld is returned by TryFileLock when another holder has the lock. It
// is filelock.ErrLockHeld, so errors.Is matches either name.
var ErrLockHeld = filelock.ErrLockHeld

// ErrLockUnsupported is returned by TryFileLock on a platform without flock.
// It is filelock.ErrLockUnsupported.
var ErrLockUnsupported = filelock.ErrLockUnsupported

// TryFileLock takes an exclusive advisory lock on path without waiting. The
// lock itself lives in internal/filelock, where internal/mirror can reach it
// without importing this package; see filelock.TryFileLock for the contract.
func TryFileLock(path, holder string) (func(), error) {
	return filelock.TryFileLock(path, holder)
}

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
// it resolved; see filelock.CanonicalPath.
func CanonicalPath(p string) string {
	return filelock.CanonicalPath(p)
}
