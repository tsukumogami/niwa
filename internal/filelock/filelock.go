// Package filelock holds niwa's advisory file locks and the path
// canonicalization their names are derived from.
//
// It is a leaf: it imports no niwa package, so both internal/workspace and
// internal/mirror can use it without one importing the other. Workspace keeps
// thin wrappers so its callers did not change when the code moved here.
package filelock

import (
	"errors"
	"path/filepath"
)

// ErrLockHeld is returned by TryFileLock when another holder has the lock, and
// by WaitFileLock when the wait runs out. The error that wraps it names the
// holder, as read from the lock file with every control character removed, or
// "another niwa command" when the holder has not written its line yet.
var ErrLockHeld = errors.New("lock is held")

// ErrLockUnsupported is returned by TryFileLock and WaitFileLock on a platform
// without flock. Callers decide what an unlockable platform means for them;
// nothing is substituted for the lock.
var ErrLockUnsupported = errors.New("file locking is not supported on this platform")

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

// resolveExistingPrefix resolves the symlinks of the longest existing prefix
// of p and rejoins the rest. internal/workspace has its own copy for its
// containment checks; this one is CanonicalPath's, so the leaf imports nothing.
func resolveExistingPrefix(p string) string {
	resolved, err := filepath.EvalSymlinks(p)
	if err == nil {
		return resolved
	}
	dir := filepath.Dir(p)
	if dir == p {
		return p
	}
	return filepath.Join(resolveExistingPrefix(dir), filepath.Base(p))
}
