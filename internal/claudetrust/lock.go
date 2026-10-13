package claudetrust

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"
)

// Claude Code serializes writes to its config with a lock directory beside the
// file, <config>.lock, taken with an atomic mkdir and treated as stale once its
// modification time is more than 10 seconds old. niwa takes the same lock, by
// the same unresolved name, so a Claude Code write can't land between niwa's
// read and rename, nor niwa's between Claude Code's.
//
// The staleness takeover has the same narrow window Claude Code's own protocol
// has, in which two takers can both proceed. Takeover depends only on the
// lock directory's modification time, not on how long anyone has waited.
// niwa holds the lock for milliseconds, one read and one rename, far under
// Claude Code's 10-second threshold, so in normal operation niwa's lock is
// never taken over as stale.

const (
	// lockSuffix names the lock directory after the config path.
	lockSuffix = ".lock"
	// defaultLockWait is how long niwa waits for the lock before skipping.
	defaultLockWait = 5 * time.Second
	// defaultLockStale is Claude Code's own staleness threshold.
	defaultLockStale = 10 * time.Second
	// defaultLockPoll is the retry interval while the lock is held.
	defaultLockPoll = 50 * time.Millisecond
)

// The lock timings are variables so tests can shorten them.
var (
	lockWait  = defaultLockWait
	lockStale = defaultLockStale
	lockPoll  = defaultLockPoll
)

// lockTimeouts records the lock paths whose wait already timed out in this
// process. An Ensure against one of them skips without waiting, so a sweep
// over many instances against one busy config doesn't wait the full timeout
// once per instance. A withdrawal ignores it and always waits, so a teardown
// keeps trying to remove each instance's entry.
//
// Entries are never cleared, which assumes the process is a short-lived niwa
// command: one run gives up on a busy config and the next run tries again. A
// long-lived process that called into this package would stop writing trust
// to that file for good after its first timeout.
var lockTimeouts = struct {
	sync.Mutex
	paths map[string]bool
}{paths: map[string]bool{}}

// lockTimedOutAt reports whether a wait for lockPath already timed out.
func lockTimedOutAt(lockPath string) bool {
	lockTimeouts.Lock()
	defer lockTimeouts.Unlock()
	return lockTimeouts.paths[lockPath]
}

// markLockTimedOut records that a wait for lockPath timed out.
func markLockTimedOut(lockPath string) {
	lockTimeouts.Lock()
	defer lockTimeouts.Unlock()
	lockTimeouts.paths[lockPath] = true
}

// clearLockTimeouts forgets every recorded timeout. Only tests call it.
func clearLockTimeouts() {
	lockTimeouts.Lock()
	defer lockTimeouts.Unlock()
	lockTimeouts.paths = map[string]bool{}
}

// processLocks holds one mutex per lock path, so goroutines in this process
// queue on the mutex instead of spinning on mkdir against each other.
var processLocks sync.Map // lock path -> *sync.Mutex

// acquireLock takes the lock directory at lockPath and returns the function
// that releases it. The caller defers the release on every path. With
// skipIfTimedOut, a path whose wait already timed out in this process is
// skipped at once instead of waited on again.
//
// When the directory exists, it is inspected with Lstat and never followed: a
// symlink or file at the lock path is never removed, only waited on. A
// directory older than the staleness threshold is stat'ed a second time to
// confirm it is the same directory and still stale, then removed with a
// non-recursive remove (which fails, harmlessly, if anything was placed inside
// it) and the mkdir retried.
//
// The release removes the lock directory only if it is still the one this
// call created. Another process that judged the lock stale may have removed
// it and taken its own; that lock is left for its holder.
func acquireLock(lockPath string, skipIfTimedOut bool) (func(), error) {
	m, _ := processLocks.LoadOrStore(lockPath, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()

	// Checked after the mutex, so goroutines queued behind a wait that timed
	// out skip too.
	if skipIfTimedOut && lockTimedOutAt(lockPath) {
		mu.Unlock()
		return nil, fmt.Errorf("the lock %s was not taken: an earlier wait for it timed out in this run", lockPath)
	}

	deadline := time.Now().Add(lockWait)
	for {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			owned, err := os.Lstat(lockPath)
			if err != nil {
				// Without its identity the release couldn't tell this lock
				// from a successor's, so it is given up at once.
				_ = os.Remove(lockPath)
				mu.Unlock()
				return nil, fmt.Errorf("inspecting the lock %s just taken: %w", lockPath, err)
			}
			return func() {
				if cur, err := os.Lstat(lockPath); err == nil && os.SameFile(owned, cur) {
					_ = os.Remove(lockPath)
				}
				mu.Unlock()
			}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			mu.Unlock()
			return nil, fmt.Errorf("taking the lock %s: %w", lockPath, err)
		}

		if first, lerr := os.Lstat(lockPath); lerr == nil && first.IsDir() && time.Since(first.ModTime()) > lockStale {
			second, lerr := os.Lstat(lockPath)
			if lerr == nil && second.IsDir() && os.SameFile(first, second) &&
				time.Since(second.ModTime()) > lockStale && os.Remove(lockPath) == nil {
				continue
			}
		}

		if !time.Now().Before(deadline) {
			markLockTimedOut(lockPath)
			mu.Unlock()
			return nil, fmt.Errorf("the lock %s is held by another process (waited %s)", lockPath, lockWait)
		}
		time.Sleep(lockPoll)
	}
}
