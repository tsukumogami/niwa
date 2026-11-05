//go:build unix

package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
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

func TestInstanceLockPath(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	inst := filepath.Join(ws, "inst")
	for _, d := range []string{inst, filepath.Join(ws, "sub"), filepath.Join(ws, "inst-2")} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "wslink")
	if err := os.Symlink(ws, link); err != nil {
		t.Fatal(err)
	}

	got := InstanceLockPath(home, inst)
	if filepath.Dir(got) != filepath.Join(home, ".niwa", "locks") {
		t.Fatalf("lock %q is not directly in ~/.niwa/locks", got)
	}

	for _, variant := range []string{inst + "/", ws + "/./inst", ws + "/sub/../inst", filepath.Join(link, "inst")} {
		if v := InstanceLockPath(home, variant); v != got {
			t.Errorf("InstanceLockPath(%q) = %q, want %q", variant, v, got)
		}
	}
	if other := InstanceLockPath(home, filepath.Join(ws, "inst-2")); other == got {
		t.Error("inst-2 shares inst's lock")
	}

	viaLink := InstanceLockPath(home, filepath.Join(link, "inst"))
	if err := os.Remove(inst); err != nil {
		t.Fatal(err)
	}
	if after := InstanceLockPath(home, inst); after != got {
		t.Errorf("after removal InstanceLockPath = %q, want %q", after, got)
	}
	if after := InstanceLockPath(home, filepath.Join(link, "inst")); after != viaLink {
		t.Errorf("after removal, through the symlinked root = %q, want %q", after, viaLink)
	}

	name := filepath.Base(got)
	if !regexp.MustCompile(`^i-[0-9a-f]{32}\.lock$`).MatchString(name) {
		t.Errorf("lock name %q is not derived from a hash alone", name)
	}
	// A component made only of hex digits (a temp directory's "001") can turn
	// up inside any hash by chance; every other component must not appear.
	for _, comp := range strings.Split(inst, string(os.PathSeparator)) {
		if comp != "" && strings.Trim(comp, "0123456789abcdef") != "" && strings.Contains(name, comp) {
			t.Errorf("lock name %q carries path component %q", name, comp)
		}
	}
	if strings.HasPrefix(got, inst+string(os.PathSeparator)) || strings.HasPrefix(got, filepath.Join(ws, ".niwa")+string(os.PathSeparator)) {
		t.Errorf("lock %q lives under the instance or the workspace's .niwa", got)
	}
}
