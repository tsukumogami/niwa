//go:build unix

package config

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/tsukumogami/niwa/internal/testfault"
)

// snapshotLockNow is the clock the acquisition deadline reads. A package
// variable so a test can run the deadline out without waiting for it.
var snapshotLockNow = time.Now

// AcquireSnapshotLock takes the exclusive snapshot lock for configDir, waiting
// up to snapshotLockTimeout for another holder, and returns the release. The
// parent of configDir must exist. The release unlocks and closes the file and
// never removes it. See the package notes in snapshotlock.go for who takes the
// lock and the leaf rule every holder follows.
func AcquireSnapshotLock(configDir string) (func(), error) {
	path := SnapshotLockPath(configDir)
	deadline := snapshotLockNow().Add(snapshotLockTimeout)
	for {
		f, err := openSnapshotLock(path)
		if err != nil {
			return nil, err
		}
		locked, err := waitForSnapshotLock(f, path, configDir, deadline)
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		if !locked {
			// The file was replaced while we waited, so the lock we hold is on
			// an inode nobody else will ever open. Start over on the new one.
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
			continue
		}
		return func() {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		}, nil
	}
}

// waitForSnapshotLock polls a non-blocking exclusive flock on f until it
// succeeds or the deadline passes. On success it reports whether f is still
// the file at path; false means the caller locked a replaced inode.
func waitForSnapshotLock(f *os.File, path, configDir string, deadline time.Time) (bool, error) {
	for {
		lockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if lockErr == nil {
			return sameSnapshotLockFile(f, path)
		}
		if !errors.Is(lockErr, syscall.EWOULDBLOCK) {
			return false, fmt.Errorf("locking %s: %w", path, lockErr)
		}
		if snapshotLockNow().After(deadline) {
			return false, snapshotLockTimeoutError(configDir)
		}
		if err := testfault.Maybe(snapshotLockWaitMark); err != nil {
			return false, fmt.Errorf("waiting for the config lock %s: %w", path, err)
		}
		time.Sleep(snapshotLockPoll)
	}
}

// sameSnapshotLockFile reports whether the open lock file f is still the file
// at path. A missing path counts as replaced.
func sameSnapshotLockFile(f *os.File, path string) (bool, error) {
	held, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("inspecting the lock %s: %w", path, err)
	}
	current, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspecting the lock %s: %w", path, err)
	}
	return os.SameFile(held, current), nil
}

// openSnapshotLock opens or creates the lock file without following a
// symlink and refuses anything that isn't a regular file owned by the current
// user. O_NONBLOCK keeps a FIFO planted at the path from blocking the open.
func openSnapshotLock(path string) (*os.File, error) {
	flags := os.O_RDWR | os.O_CREATE | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the config lock %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("inspecting the config lock %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("the config lock %s is not a regular file (mode %s)", path, info.Mode().Type())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		_ = f.Close()
		return nil, fmt.Errorf("the config lock %s is not owned by the current user", path)
	}
	return f, nil
}
