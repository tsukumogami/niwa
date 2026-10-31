//go:build unix

package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

const (
	// fileLockTimeout bounds how long one apply waits for another to finish
	// its read-modify-write. Generous against a slow disk, short enough that a
	// stuck holder surfaces as an error rather than an apply that never
	// returns.
	fileLockTimeout = 30 * time.Second

	// fileLockPoll is the interval between acquisition attempts. The lock is
	// taken non-blocking and retried so the deadline above is real: a blocking
	// flock has no timeout to give.
	fileLockPoll = 20 * time.Millisecond
)

// acquireFileLock takes an exclusive advisory lock on lockPath and returns the
// release. flock associates the lock with the open file description, so two
// acquisitions in one process contend exactly as two processes do. what names
// the guarded work in the timeout error, which reads "waiting for another niwa
// apply to finish <what>".
func acquireFileLock(lockPath, what string) (func(), error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the lock %s: %w", lockPath, err)
	}

	deadline := time.Now().Add(fileLockTimeout)
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
			return nil, fmt.Errorf("locking %s: %w", lockPath, lockErr)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf(
				"timed out after %s waiting for another niwa apply to finish %s (lock %s)",
				fileLockTimeout, what, lockPath)
		}
		time.Sleep(fileLockPoll)
	}
}

// lockOwnerUID is the uid the lock directory and lock file must belong to. A
// package variable so tests can check the ownership refusal without a second
// account.
var lockOwnerUID = os.Getuid

// lockHolderMax bounds how much of a lock file a refused taker reads for the
// holder line.
const lockHolderMax = 256

// TryFileLock takes an exclusive advisory lock on path without waiting. When
// another holder has it, it returns at once with an error wrapping ErrLockHeld
// that names the holder, so the caller decides what contention means (refuse,
// spare, defer) instead of stalling behind a command that can hold the lock
// for minutes. acquireFileLock is the waiting variant for short critical
// sections.
//
// The lock directory must already be, or is created as, a real directory (not
// a symlink) owned by the current user with mode exactly 0700. The lock file is
// opened without following a symlink, without blocking on a FIFO, close-on-exec
// so a daemonized child never inherits the lock, and only a regular file owned
// by the user is accepted. A file it creates has mode 0600.
//
// The winner records holder (who has the lock, such as a session id) through
// the locked descriptor, truncating whatever an earlier holder left. The
// release empties the file, unlocks and closes it, and never unlinks it: an
// unlinked lock file would let the next two takers lock different inodes.
func TryFileLock(path, holder string) (func(), error) {
	if err := ensureLockDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := openLockFile(path)
	if err != nil {
		return nil, err
	}

	if lockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); lockErr != nil {
		defer f.Close()
		if errors.Is(lockErr, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w by %s", ErrLockHeld, readLockHolder(f))
		}
		return nil, fmt.Errorf("locking %s: %w", path, lockErr)
	}

	unlock := func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
	if err := writeLockHolder(f, holder); err != nil {
		unlock()
		return nil, fmt.Errorf("recording the holder of %s: %w", path, err)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			// Empty the holder line while still holding the lock, so a taker
			// that loses to the next winner never reads this holder's stale line.
			_ = f.Truncate(0)
			unlock()
		})
	}, nil
}

// ensureLockDir creates dir with mode 0700 when it is missing and refuses it
// unless it is a real directory owned by lockOwnerUID with mode exactly 0700.
func ensureLockDir(dir string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return fmt.Errorf("creating the parent of the lock directory %s: %w", dir, err)
		}
		switch mkErr := os.Mkdir(dir, 0o700); {
		case mkErr == nil:
			// Mkdir's mode is filtered by the umask; set the exact mode.
			if err := os.Chmod(dir, 0o700); err != nil {
				return fmt.Errorf("setting the mode of the lock directory %s: %w", dir, err)
			}
		case !os.IsExist(mkErr):
			return fmt.Errorf("creating the lock directory %s: %w", dir, mkErr)
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return fmt.Errorf("inspecting the lock directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("the lock directory %s is not a directory (mode %s)", dir, info.Mode().Type())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != lockOwnerUID() {
		return fmt.Errorf("the lock directory %s is not owned by the current user", dir)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		return fmt.Errorf("the lock directory %s has mode %04o, want 0700", dir, perm)
	}
	return nil
}

// openLockFile opens the lock file for reading and writing, creating it 0600,
// and refuses anything but a regular file owned by lockOwnerUID. O_NOFOLLOW
// refuses a symlink at the path in the open itself, so nothing outside the lock
// directory is created or truncated through one, and O_NONBLOCK keeps a FIFO
// from stalling the open.
func openLockFile(path string) (*os.File, error) {
	flags := os.O_RDWR | os.O_CREATE | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the lock %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("inspecting the lock %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("the lock %s is not a regular file (mode %s)", path, info.Mode().Type())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != lockOwnerUID() {
		_ = f.Close()
		return nil, fmt.Errorf("the lock %s is not owned by the current user", path)
	}
	return f, nil
}

// writeLockHolder replaces the lock file's content with holder's line. It runs
// only while the lock is held.
func writeLockHolder(f *os.File, holder string) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	line := sanitizeLockHolder(holder)
	if line == "" {
		return nil
	}
	_, err := f.WriteAt([]byte(line+"\n"), 0)
	return err
}

// readLockHolder returns the holder line a refused taker reports. Anyone who
// can write the lock directory can write that line, so every control character
// is removed before it reaches an error or a terminal. An empty line means the
// winner has not written yet.
func readLockHolder(f *os.File) string {
	buf := make([]byte, lockHolderMax)
	n, _ := f.ReadAt(buf, 0)
	line := string(buf[:n])
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if line = sanitizeLockHolder(line); line == "" {
		return "another niwa command"
	}
	return line
}

// sanitizeLockHolder drops invalid UTF-8 and every control character (C0, DEL
// and C1), and trims surrounding space.
func sanitizeLockHolder(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}
