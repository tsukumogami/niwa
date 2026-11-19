//go:build unix

package filelock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// lockFixture returns a lock path inside a fresh 0700 lock directory.
func lockFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "locks")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "instance.lock")
}

// rawFlock holds path with a plain flock, the way a holder that has not yet
// written its line (or a foreign writer) would.
func rawFlock(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	})
}

func mustTryLock(t *testing.T, path, holder string) func() {
	t.Helper()
	release, err := TryFileLock(path, holder)
	if err != nil {
		t.Fatalf("TryFileLock(%q): %v", holder, err)
	}
	if release == nil {
		t.Fatal("TryFileLock returned a nil release with a nil error")
	}
	return release
}

func TestTryFileLock_ContentionFailsFast(t *testing.T) {
	path := lockFixture(t)
	release := mustTryLock(t, path, "first")

	start := time.Now()
	second, err := TryFileLock(path, "second")
	elapsed := time.Since(start)
	if elapsed >= time.Second {
		t.Fatalf("contended TryFileLock took %s, want under 1s", elapsed)
	}
	if second != nil {
		t.Fatal("contended TryFileLock returned a non-nil release")
	}
	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("contended TryFileLock error = %v, want ErrLockHeld", err)
	}

	release()
	third := mustTryLock(t, path, "third")
	third()
}

func TestTryFileLock_ReleaseKeepsInode(t *testing.T) {
	path := lockFixture(t)
	release := mustTryLock(t, path, "a")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release() // a second call is a no-op

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("lock file gone after release: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("release replaced the lock file")
	}

	release = mustTryLock(t, path, "b")
	defer release()
	again, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, again) {
		t.Fatal("the next TryFileLock locked a different inode")
	}
}

func TestTryFileLock_HolderLine(t *testing.T) {
	path := lockFixture(t)
	releaseA := mustTryLock(t, path, "aaaaaaaaaaaaaaaaaaaa")
	releaseA()

	releaseB := mustTryLock(t, path, "b")
	defer releaseB()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "b\n" {
		t.Fatalf("lock file while B holds it = %q, want %q", data, "b\n")
	}

	_, err = TryFileLock(path, "loser")
	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("loser error = %v, want ErrLockHeld", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "b") || strings.Contains(msg, "aaaa") {
		t.Fatalf("loser error %q: want B's holder line and none of A's", msg)
	}
}

func TestTryFileLock_EmptyHolderLine(t *testing.T) {
	path := lockFixture(t)
	rawFlock(t, path)

	_, err := TryFileLock(path, "loser")
	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("error = %v, want ErrLockHeld", err)
	}
	if !strings.Contains(err.Error(), "another niwa command") {
		t.Fatalf("error %q does not say another niwa command", err)
	}
}

func TestTryFileLock_StripsControlCharacters(t *testing.T) {
	path := lockFixture(t)
	if err := os.WriteFile(path, []byte("evil\x1b[2J\x1b]0;title\x07x\r\nsecond\x00\x7f"), 0o600); err != nil {
		t.Fatal(err)
	}
	rawFlock(t, path)

	_, err := TryFileLock(path, "loser")
	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("error = %v, want ErrLockHeld", err)
	}
	msg := err.Error()
	for i := 0; i < len(msg); i++ {
		if c := msg[i]; c < 0x20 || c == 0x7f {
			t.Fatalf("error %q carries control byte 0x%02x", msg, c)
		}
	}
	if !strings.Contains(msg, "evil") {
		t.Fatalf("error %q lost the printable holder text", msg)
	}
}

func TestTryFileLock_RefusesUnsafeLockDir(t *testing.T) {
	refused := func(t *testing.T, dir string) {
		t.Helper()
		path := filepath.Join(dir, "instance.lock")
		release, err := TryFileLock(path, "h")
		if err == nil {
			release()
			t.Fatal("TryFileLock accepted an unsafe lock directory")
		}
		if errors.Is(err, ErrLockHeld) {
			t.Fatalf("unsafe directory reported as contention: %v", err)
		}
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Fatalf("a lock file was created in the refused directory (%v)", statErr)
		}
	}

	for _, mode := range []os.FileMode{0o755, 0o750, 0o711} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "locks")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, mode); err != nil {
				t.Fatal(err)
			}
			refused(t, dir)
		})
	}

	t.Run("symlink", func(t *testing.T) {
		base := t.TempDir()
		real := filepath.Join(base, "real")
		if err := os.Mkdir(real, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(real, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(base, "locks")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		refused(t, link)
		if _, err := os.Lstat(filepath.Join(real, "instance.lock")); !os.IsNotExist(err) {
			t.Fatal("a lock file was created through the symlinked directory")
		}
	})

	t.Run("regular file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "locks")
		if err := os.WriteFile(dir, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "instance.lock")
		release, err := TryFileLock(path, "h")
		if err == nil {
			release()
			t.Fatal("TryFileLock accepted a regular file as its directory")
		}
		if errors.Is(err, ErrLockHeld) {
			t.Fatalf("a regular-file directory reported as contention: %v", err)
		}
	})

	t.Run("other owner", func(t *testing.T) {
		orig := lockOwnerUID
		lockOwnerUID = func() int { return os.Getuid() + 1 }
		t.Cleanup(func() { lockOwnerUID = orig })

		dir := filepath.Join(t.TempDir(), "locks")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		refused(t, dir)
	})

	t.Run("missing is created 0700", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".niwa", "locks")
		release := mustTryLock(t, filepath.Join(dir, "instance.lock"), "h")
		defer release()
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("created lock directory mode = %s, want a 0700 directory", info.Mode())
		}
	})
}

func TestTryFileLock_RefusesUnsafeLockFile(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		path := lockFixture(t)
		target := filepath.Join(t.TempDir(), "target")
		if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if release, err := TryFileLock(path, "h"); err == nil {
			release()
			t.Fatal("TryFileLock followed a symlink at the lock path")
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
			release, err := TryFileLock(path, "h")
			if err == nil {
				release()
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("TryFileLock accepted a FIFO")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("TryFileLock hung on a FIFO")
		}
	})

	t.Run("directory", func(t *testing.T) {
		path := lockFixture(t)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if release, err := TryFileLock(path, "h"); err == nil {
			release()
			t.Fatal("TryFileLock accepted a directory")
		}
	})

	t.Run("created 0600", func(t *testing.T) {
		path := lockFixture(t)
		release := mustTryLock(t, path, "h")
		defer release()
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("lock file mode = %s, want a 0600 regular file", info.Mode())
		}
	})
}

func TestTryFileLock_CloseOnExec(t *testing.T) {
	path := lockFixture(t)
	release := mustTryLock(t, path, "h")
	defer release()

	var want unix.Stat_t
	if err := unix.Stat(path, &want); err != nil {
		t.Fatal(err)
	}
	found := false
	for fd := 0; fd < 4096; fd++ {
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Dev != want.Dev || st.Ino != want.Ino {
			continue
		}
		found = true
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil {
			t.Fatalf("F_GETFD on fd %d: %v", fd, err)
		}
		if flags&unix.FD_CLOEXEC == 0 {
			t.Fatalf("lock descriptor %d is not close-on-exec (flags %#x)", fd, flags)
		}
	}
	if !found {
		t.Fatal("no open descriptor for the held lock file")
	}
}

func TestWaitFileLock_TakesAFreeLock(t *testing.T) {
	path := lockFixture(t)
	release, err := WaitFileLock(context.Background(), path, "waiter", time.Second)
	if err != nil {
		t.Fatalf("WaitFileLock on a free lock: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "waiter\n" {
		t.Fatalf("lock file while held = %q, want %q", data, "waiter\n")
	}
	release()
	release() // a second call is a no-op
	mustTryLock(t, path, "next")()
}

func TestWaitFileLock_WaitsForTheHolder(t *testing.T) {
	path := lockFixture(t)
	release := mustTryLock(t, path, "first")
	time.AfterFunc(200*time.Millisecond, release)

	start := time.Now()
	second, err := WaitFileLock(context.Background(), path, "second", 10*time.Second)
	if err != nil {
		t.Fatalf("WaitFileLock while the holder releases: %v", err)
	}
	defer second()
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("WaitFileLock returned after %s, before the holder released", elapsed)
	}
}

func TestWaitFileLock_TimeoutNamesTheHolder(t *testing.T) {
	path := lockFixture(t)
	release := mustTryLock(t, path, "restorer")
	defer release()

	start := time.Now()
	got, err := WaitFileLock(context.Background(), path, "loser", 150*time.Millisecond)
	if got != nil {
		t.Fatal("a timed-out WaitFileLock returned a release")
	}
	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("timeout error = %v, want ErrLockHeld", err)
	}
	if !strings.Contains(err.Error(), "restorer") {
		t.Fatalf("timeout error %q does not name the holder", err)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("WaitFileLock gave up after %s, want about 150ms", elapsed)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "restorer\n" {
		t.Fatalf("the loser changed the holder line: %q (%v)", data, err)
	}
}

func TestWaitFileLock_ContextEndsTheWait(t *testing.T) {
	path := lockFixture(t)
	release := mustTryLock(t, path, "holder")
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	_, err := WaitFileLock(ctx, path, "loser", time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error after cancel = %v, want context.Canceled", err)
	}
}

func TestWaitFileLock_RefusesUnsafeLockFile(t *testing.T) {
	path := lockFixture(t)
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if release, err := WaitFileLock(context.Background(), path, "h", time.Second); err == nil {
		release()
		t.Fatal("WaitFileLock followed a symlink at the lock path")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep" {
		t.Fatalf("symlink target = %q (%v), want %q", data, err, "keep")
	}
}

func TestCanonicalPath(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{real, real + "/", link, link + "/./"} {
		if got := CanonicalPath(p); got != want {
			t.Errorf("CanonicalPath(%q) = %q, want %q", p, got, want)
		}
	}
	if got := CanonicalPath(filepath.Join(link, "gone", "deeper")); got != filepath.Join(want, "gone", "deeper") {
		t.Errorf("CanonicalPath of a missing path = %q, want it under %q", got, want)
	}
}
