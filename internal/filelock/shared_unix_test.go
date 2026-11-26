//go:build unix

package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func mustTryShared(t *testing.T, path string) func() {
	t.Helper()
	release, err := TryFileLockShared(path)
	if err != nil {
		t.Fatalf("TryFileLockShared: %v", err)
	}
	if release == nil {
		t.Fatal("TryFileLockShared returned a nil release with a nil error")
	}
	return release
}

// Two shared holders hold the lock at once; an exclusive taker is refused
// at once while either holds it, and takes it once both release.
func TestTryFileLockShared_SharedHoldersExcludeAnExclusiveTaker(t *testing.T) {
	path := lockFixture(t)
	first := mustTryShared(t, path)
	second := mustTryShared(t, path)

	start := time.Now()
	release, err := TryFileLock(path, "recovery")
	if time.Since(start) >= time.Second {
		t.Fatal("an exclusive try against shared holders waited")
	}
	if release != nil || !errors.Is(err, ErrLockHeld) {
		t.Fatalf("exclusive try under shared holders = %v, want ErrLockHeld", err)
	}

	first()
	if release, err := TryFileLock(path, "recovery"); err == nil {
		release()
		t.Fatal("an exclusive try succeeded while one shared holder remained")
	}
	second()
	second() // a second call is a no-op
	mustTryLock(t, path, "recovery")()
}

// A shared try against an exclusive holder is refused at once and names the
// holder from its line.
func TestTryFileLockShared_RefusedByAnExclusiveHolder(t *testing.T) {
	path := lockFixture(t)
	release := mustTryLock(t, path, "niwa reap (pid 7)")
	defer release()

	start := time.Now()
	shared, err := TryFileLockShared(path)
	if time.Since(start) >= time.Second {
		t.Fatal("a shared try against an exclusive holder waited")
	}
	if shared != nil || !errors.Is(err, ErrLockHeld) {
		t.Fatalf("shared try = %v, want ErrLockHeld", err)
	}
	if !strings.Contains(err.Error(), "niwa reap (pid 7)") {
		t.Fatalf("shared try error %q doesn't name the holder", err)
	}
}

// A shared holder writes no holder line and its release doesn't empty the
// file, so one shared holder never rewrites what was there.
func TestTryFileLockShared_WritesNoHolderLine(t *testing.T) {
	path := lockFixture(t)
	if err := os.WriteFile(path, []byte("left by an earlier holder\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := mustTryShared(t, path)
	second := mustTryShared(t, path)
	second()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "left by an earlier holder\n" {
		t.Fatalf("lock file under shared holders = %q, want it untouched", data)
	}
	first()
	if data, _ := os.ReadFile(path); string(data) != "left by an earlier holder\n" {
		t.Fatalf("lock file after the shared releases = %q, want it untouched", data)
	}
}

// The shared try opens the file through the same checks as the exclusive
// one: a symlink at the lock path fails the take and the link's target is
// unchanged, a FIFO doesn't hang it, and a file it creates is 0600.
func TestTryFileLockShared_RefusesUnsafeLockFile(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		path := lockFixture(t)
		target := filepath.Join(t.TempDir(), "target")
		if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if release, err := TryFileLockShared(path); err == nil {
			release()
			t.Fatal("TryFileLockShared followed a symlink at the lock path")
		}
		data, err := os.ReadFile(target)
		if err != nil || string(data) != "keep" {
			t.Fatalf("symlink target = %q (%v), want %q", data, err, "keep")
		}
	})

	t.Run("fifo", func(t *testing.T) {
		path := lockFixture(t)
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			release, err := TryFileLockShared(path)
			if err == nil {
				release()
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("TryFileLockShared accepted a FIFO")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("TryFileLockShared hung on a FIFO")
		}
	})

	t.Run("created 0600", func(t *testing.T) {
		path := lockFixture(t)
		release := mustTryShared(t, path)
		defer release()
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("lock file mode = %s, want a 0600 regular file", info.Mode())
		}
	})

	t.Run("another user's directory", func(t *testing.T) {
		path := lockFixture(t)
		orig := lockOwnerUID
		lockOwnerUID = func() int { return os.Getuid() + 1 }
		t.Cleanup(func() { lockOwnerUID = orig })
		if release, err := TryFileLockShared(path); err == nil {
			release()
			t.Fatal("TryFileLockShared accepted a lock directory another user owns")
		}
	})
}
