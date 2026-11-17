//go:build unix

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// None of these tests depends on how long anything takes. A waiter is
// observed through the P.held file the snapshot-lock-wait mark writes, and a
// waiter parked in a hold (P not created yet) can't return until the test
// creates P. Deadlines below are hang guards only.

// lockTestHangGuard bounds every wait in these tests.
const lockTestHangGuard = 60 * time.Second

// releaseHoldOnCleanup creates p when the test ends, so a goroutine parked in
// a hold on p never outlives the test by the hold's own limit.
func releaseHoldOnCleanup(t *testing.T, p string) {
	t.Helper()
	t.Cleanup(func() { _ = os.WriteFile(p, nil, 0o600) })
}

// waitForHeld waits until p.held exists. done, when it delivers, means the
// waiter returned before parking, which fails the test.
func waitForHeld(t *testing.T, p string, done <-chan error) {
	t.Helper()
	guard := time.After(lockTestHangGuard)
	for {
		if _, err := os.Lstat(p + ".held"); err == nil {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("the waiter returned (%v) before it waited for the lock", err)
		case <-guard:
			t.Fatalf("the waiter never reached the snapshot-lock-wait mark (%s.held)", p)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// joinWaiter returns what the waiter sent on done.
func joinWaiter(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(lockTestHangGuard):
		t.Fatal("the waiter never returned")
		return nil
	}
}

// holdSnapshotLock takes configDir's lock for the rest of the test or until
// the returned release is called.
func holdSnapshotLock(t *testing.T, configDir string) func() {
	t.Helper()
	release, err := AcquireSnapshotLock(configDir)
	if err != nil {
		t.Fatalf("holding the lock: %v", err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			release()
		}
	})
	return func() {
		if !released {
			released = true
			release()
		}
	}
}

func TestAcquireSnapshotLock_WaitsForHolder(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), ".niwa")
	releaseHolder := holdSnapshotLock(t, configDir)

	p := filepath.Join(t.TempDir(), "lock-wait")
	releaseHoldOnCleanup(t, p)
	t.Setenv("NIWA_TEST_FAULT", "hold:"+p+"@snapshot-lock-wait")

	done := make(chan error, 1)
	go func() {
		release, err := AcquireSnapshotLock(configDir)
		if err == nil {
			release()
		}
		done <- err
	}()

	waitForHeld(t, p, done)
	select {
	case err := <-done:
		t.Fatalf("the waiter returned (%v) while the holder still held the lock", err)
	default:
	}

	releaseHolder()
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := joinWaiter(t, done); err != nil {
		t.Fatalf("acquiring after the holder let go: %v", err)
	}
}

func TestAcquireSnapshotLock_TimeoutNamesLock(t *testing.T) {
	t.Setenv("NIWA_TEST_FAULT", "")
	configDir := filepath.Join(t.TempDir(), ".niwa")
	holdSnapshotLock(t, configDir)

	// The first reading sets the deadline; every later one is past it.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	calls := 0
	snapshotLockNow = func() time.Time {
		calls++
		if calls == 1 {
			return t0
		}
		return t0.Add(snapshotLockTimeout + time.Second)
	}
	t.Cleanup(func() { snapshotLockNow = time.Now })

	release, err := AcquireSnapshotLock(configDir)
	if err == nil {
		release()
		t.Fatal("acquired a lock someone else holds")
	}
	want := fmt.Sprintf("timed out after 30s waiting for the config lock %s.lock: another niwa command is refreshing %s", configDir, configDir)
	if err.Error() != want {
		t.Errorf("timeout error\n  got:  %s\n  want: %s", err, want)
	}

	// The holder still holds: a fresh attempt waits, and the wait fails at
	// once under the error spec.
	snapshotLockNow = time.Now
	t.Setenv("NIWA_TEST_FAULT", "error:still-held@snapshot-lock-wait")
	if release, err := AcquireSnapshotLock(configDir); err == nil {
		release()
		t.Fatal("the timed-out waiter left the lock free")
	} else if !strings.Contains(err.Error(), "still-held") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAcquireSnapshotLock_ErrorSpecAtWait(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), ".niwa")
	holdSnapshotLock(t, configDir)
	t.Setenv("NIWA_TEST_FAULT", "error:boom@snapshot-lock-wait")

	release, err := AcquireSnapshotLock(configDir)
	if err == nil {
		release()
		t.Fatal("acquired a lock someone else holds")
	}
	if !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), SnapshotLockPath(configDir)) {
		t.Fatalf("error should carry the fault and name the lock: %v", err)
	}
}

func TestAcquireSnapshotLock_NoWaitWhenFree(t *testing.T) {
	// The error spec fails any wait at all, so a free lock must be taken
	// without reaching the mark.
	t.Setenv("NIWA_TEST_FAULT", "error:waited@snapshot-lock-wait")
	configDir := filepath.Join(t.TempDir(), ".niwa")
	release, err := AcquireSnapshotLock(configDir)
	if err != nil {
		t.Fatalf("taking a free lock: %v", err)
	}
	release()
}

func TestAcquireSnapshotLock_RefusesSymlinkAndNonRegular(t *testing.T) {
	t.Setenv("NIWA_TEST_FAULT", "")
	base := t.TempDir()

	t.Run("symlink", func(t *testing.T) {
		configDir := filepath.Join(base, "linked")
		target := filepath.Join(base, "elsewhere")
		if err := os.Symlink(target, SnapshotLockPath(configDir)); err != nil {
			t.Fatal(err)
		}
		release, err := AcquireSnapshotLock(configDir)
		if err == nil {
			release()
			t.Fatal("took a lock through a symlink")
		}
		if !strings.Contains(err.Error(), SnapshotLockPath(configDir)) {
			t.Errorf("error should name the lock: %v", err)
		}
		if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
			t.Errorf("the symlink's target was created: %v", statErr)
		}
	})

	t.Run("directory", func(t *testing.T) {
		configDir := filepath.Join(base, "dir")
		if err := os.Mkdir(SnapshotLockPath(configDir), 0o700); err != nil {
			t.Fatal(err)
		}
		release, err := AcquireSnapshotLock(configDir)
		if err == nil {
			release()
			t.Fatal("took a lock on a directory")
		}
		if !strings.Contains(err.Error(), SnapshotLockPath(configDir)) {
			t.Errorf("error should name the lock: %v", err)
		}
	})
}

func TestAcquireSnapshotLock_ReleaseLeavesFile(t *testing.T) {
	t.Setenv("NIWA_TEST_FAULT", "")
	configDir := filepath.Join(t.TempDir(), ".niwa")
	release, err := AcquireSnapshotLock(configDir)
	if err != nil {
		t.Fatal(err)
	}
	release()
	info, err := os.Lstat(SnapshotLockPath(configDir))
	if err != nil {
		t.Fatalf("the lock file is gone after release: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("the lock file is not a regular file: %s", info.Mode())
	}
	if _, err := os.Lstat(configDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("taking the lock created the config dir: %v", err)
	}
}

func TestAcquireSnapshotLock_RecheckAfterReplace(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), ".niwa")
	lockPath := SnapshotLockPath(configDir)
	releaseHolder := holdSnapshotLock(t, configDir)

	p := filepath.Join(t.TempDir(), "lock-wait")
	releaseHoldOnCleanup(t, p)
	t.Setenv("NIWA_TEST_FAULT", "hold:"+p+"@snapshot-lock-wait")

	type result struct {
		release func()
		err     error
	}
	got := make(chan result, 1)
	done := make(chan error, 1)
	go func() {
		release, err := AcquireSnapshotLock(configDir)
		got <- result{release, err}
		done <- err
	}()
	waitForHeld(t, p, done)

	// Replace the lock file while the waiter has the old one open.
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	releaseHolder()
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := joinWaiter(t, done); err != nil {
		t.Fatalf("waiter: %v", err)
	}
	waiter := <-got
	defer waiter.release()

	// The waiter must hold the file now at the path, so another attempt
	// has to wait, and the error spec fails that wait at once.
	t.Setenv("NIWA_TEST_FAULT", "error:held@snapshot-lock-wait")
	if release, err := AcquireSnapshotLock(configDir); err == nil {
		release()
		t.Fatal("the waiter locked the replaced inode instead of the file at the path")
	} else if !strings.Contains(err.Error(), "held") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// writeConfigFile writes content to dir/workspace.toml, creating dir.
func writeConfigFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_WaitsForSwapThenReads(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, ConfigDir)
	// Mid-swap: .niwa has been renamed to .niwa.prev and the refresh holds
	// the lock.
	writeConfigFile(t, configDir+PrevSuffix, minimalConfig)
	releaseHolder := holdSnapshotLock(t, configDir)

	p := filepath.Join(t.TempDir(), "lock-wait")
	releaseHoldOnCleanup(t, p)
	t.Setenv("NIWA_TEST_FAULT", "hold:"+p+"@snapshot-lock-wait")

	done := make(chan error, 1)
	var loaded *ParseResult
	go func() {
		r, err := Load(filepath.Join(configDir, ConfigFile))
		loaded = r
		done <- err
	}()
	waitForHeld(t, p, done)

	// The refresh finishes its swap and lets go.
	writeConfigFile(t, configDir, minimalConfig)
	releaseHolder()
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := joinWaiter(t, done); err != nil {
		t.Fatalf("Load across a swap: %v", err)
	}
	if loaded == nil || loaded.Config.Workspace.Name != "test-ws" {
		t.Fatalf("Load returned the wrong config: %+v", loaded)
	}
}

func TestLoad_SecondENOENTIsReal(t *testing.T) {
	t.Setenv("NIWA_TEST_FAULT", "")
	configDir := filepath.Join(t.TempDir(), ConfigDir)
	release, err := AcquireSnapshotLock(configDir)
	if err != nil {
		t.Fatal(err)
	}
	release()

	_, err = Load(filepath.Join(configDir, ConfigFile))
	if err == nil {
		t.Fatal("Load of a missing file succeeded")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error should still be a not-exist error: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "reading config: ") {
		t.Errorf("error lost its prefix: %v", err)
	}
}

func TestLoad_NoLockFileNoWaitNoCreate(t *testing.T) {
	t.Setenv("NIWA_TEST_FAULT", "error:boom@snapshot-lock-wait")
	configDir := filepath.Join(t.TempDir(), ConfigDir)

	_, err := Load(filepath.Join(configDir, ConfigFile))
	if err == nil {
		t.Fatal("Load of a missing file succeeded")
	}
	if !errors.Is(err, fs.ErrNotExist) || strings.Contains(err.Error(), "boom") {
		t.Errorf("want the plain not-exist error, got: %v", err)
	}
	if _, statErr := os.Lstat(SnapshotLockPath(configDir)); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("Load created a lock file: %v", statErr)
	}
}

func TestDiscover_DoesNotRestoreOverLiveSwap(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, ConfigDir)
	prevDir := configDir + PrevSuffix
	writeConfigFile(t, prevDir, "old")
	releaseHolder := holdSnapshotLock(t, configDir)

	p := filepath.Join(t.TempDir(), "lock-wait")
	releaseHoldOnCleanup(t, p)
	t.Setenv("NIWA_TEST_FAULT", "hold:"+p+"@snapshot-lock-wait")

	type found struct {
		path, dir string
	}
	got := make(chan found, 1)
	done := make(chan error, 1)
	go func() {
		path, dir, err := Discover(root)
		got <- found{path, dir}
		done <- err
	}()
	waitForHeld(t, p, done)

	// The refresh finishes its second rename, leaving its .prev for its own
	// cleanup, then lets go.
	writeConfigFile(t, configDir, "new")
	releaseHolder()
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := joinWaiter(t, done); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	f := <-got
	if f.dir != configDir || f.path != filepath.Join(configDir, ConfigFile) {
		t.Errorf("Discover found %q in %q, want %q", f.path, f.dir, configDir)
	}
	if data, err := os.ReadFile(filepath.Join(configDir, ConfigFile)); err != nil || string(data) != "new" {
		t.Errorf("the finished swap's config was replaced: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(prevDir, ConfigFile)); err != nil || string(data) != "old" {
		t.Errorf(".niwa.prev was moved or changed: %q, %v", data, err)
	}
}
