package claudetrust

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Claude Code serializes writes to its config with a lock directory beside the
// file, <config>.lock, taken with an atomic mkdir and treated as stale once its
// modification time is more than 10 seconds old. niwa takes the same lock, by
// the same unresolved name, so a Claude Code write can't land between niwa's
// read and rename, nor niwa's between Claude Code's.
//
// The staleness takeover has the same narrow window Claude Code's own protocol
// has, in which two takers can both proceed. niwa's wait is well below the
// staleness threshold, so a live holder that keeps its lock fresh is never
// taken over.

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

// lockTimedOut records that one wait already timed out in this process. Later
// attempts skip without waiting, so a sweep over many instances against a busy
// config doesn't wait the full timeout once per instance.
var lockTimedOut atomic.Bool

// processLocks holds one mutex per lock path, so goroutines in this process
// queue on the mutex instead of spinning on mkdir against each other.
var processLocks sync.Map // lock path -> *sync.Mutex

// acquireLock takes the lock directory at lockPath and returns the function
// that releases it. The caller defers the release on every path.
//
// When the directory exists, it is inspected with Lstat and never followed: a
// symlink or file at the lock path is never removed, only waited on. A
// directory older than the staleness threshold is stat'ed a second time to
// confirm it is the same directory and still stale, then removed with a
// non-recursive remove (which fails, harmlessly, if anything was placed inside
// it) and the mkdir retried.
func acquireLock(lockPath string) (func(), error) {
	m, _ := processLocks.LoadOrStore(lockPath, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()

	// Checked after the mutex, so goroutines queued behind a wait that timed
	// out skip too.
	if lockTimedOut.Load() {
		mu.Unlock()
		return nil, fmt.Errorf("the lock %s was not taken: an earlier wait for a Claude config lock timed out in this run", lockPath)
	}

	deadline := time.Now().Add(lockWait)
	for {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			return func() {
				_ = os.Remove(lockPath)
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
			lockTimedOut.Store(true)
			mu.Unlock()
			return nil, fmt.Errorf("the lock %s is held by another process (waited %s)", lockPath, lockWait)
		}
		time.Sleep(lockPoll)
	}
}
