package config

import (
	"fmt"
	"path/filepath"
	"time"
)

// The config snapshot lock serializes every refresh of one config directory.
//
// A refresh stages the new snapshot in <dir>.next, copies local state into it,
// renames <dir> to <dir>.prev and renames the staging directory into place.
// Two commands doing that at once share the staging and .prev paths, so one
// wipes the other's staging, and a session mapping written between the copy
// and the swap is thrown away with the old <dir>. A reader that finds <dir>
// missing between the two renames can also mistake the live swap for a crash
// and put <dir>.prev back over it.
//
// AcquireSnapshotLock takes an exclusive flock on <dir>.lock. The file sits
// beside <dir>, <dir>.next and <dir>.prev, so a swap never replaces it, and it
// is never unlinked: deleting a lock file while someone waits on it would let
// two holders lock two different inodes under the same name.
//
// Who takes it: the snapshot writers (refresh, materialize and the overlay
// sync), the session mapping writers for the workspace config dir, the locked
// mapping read in worktree destroy, discovery before it restores <dir>.prev,
// and a config read that finds the file missing beside an existing lock.
//
// Leaf rule: while a process holds a snapshot lock it takes no other lock. Not
// a second snapshot lock, not even the same file through a new open (flock
// contends per open file description, so a process that locks the same file
// twice waits on itself), not an instance lock and not a marketplace lock. The
// snapshot entry points lock once and call internals that never call Discover,
// Load or the session mapping writers. A process may hold an instance lock
// while it takes a snapshot lock; never the reverse.

const (
	// snapshotLockTimeout bounds one acquisition. A waiter that runs out of
	// time fails with an error naming the lock; it never goes on unlocked.
	// There is deliberately no environment override.
	snapshotLockTimeout = 30 * time.Second

	// snapshotLockPoll is the interval between non-blocking attempts. A
	// blocking flock has no timeout to give, so the lock is polled.
	snapshotLockPoll = 20 * time.Millisecond

	// snapshotLockWaitMark is the testfault label checked each time an
	// attempt finds the lock held, so tests can observe a waiter without
	// depending on how long it waits.
	snapshotLockWaitMark = "snapshot-lock-wait"
)

// SnapshotLockPath returns the lock file guarding configDir: configDir with
// ".lock" appended, a sibling of the directory rather than a file inside it.
func SnapshotLockPath(configDir string) string {
	return filepath.Clean(configDir) + ".lock"
}

// snapshotLockTimeoutError is the error a waiter gets when the bound runs out.
func snapshotLockTimeoutError(configDir string) error {
	dir := filepath.Clean(configDir)
	return fmt.Errorf("timed out after %s waiting for the config lock %s: another niwa command is refreshing %s",
		snapshotLockTimeout, SnapshotLockPath(dir), dir)
}
