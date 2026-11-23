package mirror

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The workspace key directory ~/.niwa/w-<key>/ is shared: the plugin store
// keeps plugins/ beside mirrors/ there, and reads a narrowed mode on an
// existing key directory as its own unusable condition. The mirror creates
// the key directory when it's missing and otherwise leaves its mode as it is;
// it narrows only the levels it owns alone, mirrors/ and below.

// requireKeyDirLeft fails unless the key directory still has mode want.
func requireKeyDirLeft(t *testing.T, key string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(key)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != want {
		t.Fatalf("the key directory %s reads %04o afterwards, want it left at %04o", key, info.Mode().Perm(), want)
	}
}

// chmodKeyDir sets the key directory's mode and puts it back to 0700 when
// the test ends, so the temporary directory can be removed.
func chmodKeyDir(t *testing.T, key string, mode os.FileMode) {
	t.Helper()
	t.Cleanup(func() { _ = os.Chmod(key, 0o700) })
	if err := os.Chmod(key, mode); err != nil {
		t.Fatal(err)
	}
}

// mirrorLevels are the directories under the key directory the mirror owns
// alone.
func mirrorLevels(f fixture) []string {
	mdir := f.mdir()
	return []string{mdir, metaDir(mdir), filepath.Join(mdir, ".store"), filepath.Join(mdir, ".tmp")}
}

func TestEnsureLayout_LeavesAnExistingKeyDirMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o500, 0o555, 0o750, 0o755} {
		t.Run(fmt.Sprintf("%04o", mode), func(t *testing.T) {
			f := newFixture(t)
			key := keyDir(f.home, f.root)
			// mirrors/ is there already, as it is once a mirror has been
			// built, so a key directory without write permission still lets
			// ensureLayout reach the mirror's own levels.
			if err := os.MkdirAll(f.mdir(), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(key, 0o700); err != nil {
				t.Fatal(err)
			}
			chmodKeyDir(t, key, mode)

			if err := ensureLayout(f.home, f.root); err != nil {
				t.Fatalf("ensureLayout with the key directory at %04o: %v", mode, err)
			}
			requireKeyDirLeft(t, key, mode)
			for _, dir := range mirrorLevels(f) {
				requireMode(t, dir, 0o700, true)
			}
		})
	}
}

func TestEnsureLayout_NarrowedKeyDirWithoutMirrorsFailsAndIsLeft(t *testing.T) {
	f := newFixture(t)
	key := keyDir(f.home, f.root)
	if err := os.MkdirAll(key, 0o700); err != nil {
		t.Fatal(err)
	}
	chmodKeyDir(t, key, 0o500)

	err := ensureLayout(f.home, f.root)
	if err == nil || !strings.Contains(err.Error(), f.mdir()) {
		t.Fatalf("ensureLayout = %v, want an error naming %s", err, f.mdir())
	}
	requireKeyDirLeft(t, key, 0o500)
}

func TestEnsureLayout_CreatesAMissingKeyDir0700(t *testing.T) {
	f := newFixture(t)
	// ~/.niwa is left to the caller's mode, so it's made first.
	if err := os.MkdirAll(filepath.Join(f.home, ".niwa"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A umask that takes the owner's write bit off: the mode is set
	// explicitly after the mkdir, so it comes out 0700 anyway.
	old := syscall.Umask(0o277)
	t.Cleanup(func() { syscall.Umask(old) })

	err := ensureLayout(f.home, f.root)
	syscall.Umask(old)
	if err != nil {
		t.Fatal(err)
	}
	requireMode(t, keyDir(f.home, f.root), 0o700, true)
	for _, dir := range mirrorLevels(f) {
		requireMode(t, dir, 0o700, true)
	}
}

func TestEnsureLayout_KeyDirThatIsAFileIsRefused(t *testing.T) {
	f := newFixture(t)
	key := keyDir(f.home, f.root)
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureLayout(f.home, f.root); err == nil || !strings.Contains(err.Error(), key) {
		t.Fatalf("ensureLayout = %v, want an error naming %s", err, key)
	}
	requireMode(t, key, 0o600, false)
}

func TestHostile_NarrowedKeyDirIsLeftAlone(t *testing.T) {
	for _, r := range routes {
		t.Run("check via "+r.name, func(t *testing.T) {
			f := r.setup(t)
			key := keyDir(f.home, f.root)
			chmodKeyDir(t, key, 0o500)

			requireOutcome(t, r.call(t, f, r.opts(f)), FaultNone, OutcomeHealthy)
			requireKeyDirLeft(t, key, 0o500)
			for _, dir := range mirrorLevels(f) {
				requireMode(t, dir, 0o700, true)
			}
		})
		t.Run("restore via "+r.name, func(t *testing.T) {
			f := r.setup(t)
			if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
				t.Fatal(err)
			}
			key := keyDir(f.home, f.root)
			chmodKeyDir(t, key, 0o500)

			requireOutcome(t, r.call(t, f, r.opts(f)), FaultMissing, OutcomeRestored)
			requireKeyDirLeft(t, key, 0o500)
			for _, dir := range mirrorLevels(f) {
				requireMode(t, dir, 0o700, true)
			}
		})
	}
}
