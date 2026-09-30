//go:build unix

package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Helper processes: the test binary re-executes itself with helperEnv set
// and runs one Update, so two writers really are separate processes.
const (
	helperEnv       = "NIWA_STORE_TEST_HELPER" // the key this helper puts
	helperDirEnv    = "NIWA_STORE_TEST_HELPER_DIR"
	helperSyncEnv   = "NIWA_STORE_TEST_HELPER_SYNC"
	helperNoLockEnv = "NIWA_STORE_TEST_HELPER_NOLOCK"
	helperPauseEnv  = "NIWA_STORE_TEST_HELPER_PAUSE"
)

// runHelper waits for the start signal, then puts its key. After reading
// the data file it marks itself as having read and waits for every other
// helper to do the same, up to the pause limit.
func runHelper() int {
	key := os.Getenv(helperEnv)
	dir := os.Getenv(helperDirEnv)
	syncDir := os.Getenv(helperSyncEnv)
	pause, err := time.ParseDuration(os.Getenv(helperPauseEnv))
	if err != nil {
		fmt.Fprintln(os.Stderr, "bad pause:", err)
		return 2
	}
	testHookSkipLock = os.Getenv(helperNoLockEnv) != ""
	testHookAfterRead = func() {
		_ = os.WriteFile(filepath.Join(syncDir, key+".read"), nil, 0o600)
		deadline := time.Now().Add(pause)
		for time.Now().Before(deadline) {
			marks, _ := filepath.Glob(filepath.Join(syncDir, "*.read"))
			if len(marks) >= 2 {
				// Without the lock, stagger the writes once both have
				// read: overlapping writers can also remove each other's
				// temp files, which would muddy the lost update the test
				// is after.
				if testHookSkipLock && key == "Y" {
					time.Sleep(300 * time.Millisecond)
				}
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	for start := time.Now(); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(syncDir, "go")); err == nil {
			break
		}
		if time.Since(start) > 30*time.Second {
			fmt.Fprintln(os.Stderr, "no start signal")
			return 2
		}
	}
	if err := Update(dir, testIdentity(), map[string]Entry{key: entry(key+"-value", time.Now(), key)}, nil, false); err != nil {
		fmt.Fprintln(os.Stderr, "update:", err)
		return 1
	}
	return 0
}

// runWriters runs two helper processes, one putting X and one putting Y
// for the same identity, and returns what's stored afterwards.
func runWriters(t *testing.T, dir string, noLock bool, pause time.Duration) map[string]Entry {
	t.Helper()
	syncDir := t.TempDir()
	var cmds []*exec.Cmd
	var outs []*bytes.Buffer
	for _, key := range []string{"X", "Y"} {
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(),
			helperEnv+"="+key,
			helperDirEnv+"="+dir,
			helperSyncEnv+"="+syncDir,
			helperPauseEnv+"="+pause.String(),
		)
		if noLock {
			cmd.Env = append(cmd.Env, helperNoLockEnv+"=1")
		}
		out := &bytes.Buffer{}
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
		outs = append(outs, out)
	}
	if err := os.WriteFile(filepath.Join(syncDir, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper %d: %v\n%s", i, err, outs[i])
		}
	}
	return mustLoad(t, dir, testIdentity())
}

func TestConcurrentWritersKeepBothKeys(t *testing.T) {
	dir := isolate(t)
	// Each writer pauses after reading until both have read. Under the
	// lock the second can't read until the first has written, so the
	// first waits out its pause (well inside the second's lock wait).
	got := runWriters(t, dir, false, 500*time.Millisecond)
	assertKeys(t, got, "X", "Y")
}

func TestConcurrentWritersWithoutTheLockLoseAKey(t *testing.T) {
	dir := isolate(t)
	// The same pause with the lock bypassed: both read an empty store,
	// both write, and the second rename drops the first writer's key.
	got := runWriters(t, dir, true, 20*time.Second)
	if len(got) != 1 {
		t.Fatalf("stored keys = %v, want exactly one (the race must lose a key)", keysOf(got))
	}
}

// holdLock takes the identity's lock the way another process would.
func holdLock(t *testing.T, dir string) (release func()) {
	t.Helper()
	f, err := os.OpenFile(lockPath(dir, testIdentity()), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		})
	}
	t.Cleanup(release)
	return release
}

func TestUpdateWaitsForTheLock(t *testing.T) {
	dir := isolate(t)
	id := testIdentity()
	mustUpdate(t, dir, id, map[string]Entry{"A": entry("a", time.Now(), "1")}, nil, false)
	release := holdLock(t, dir)
	go func() {
		time.Sleep(300 * time.Millisecond)
		release()
	}()

	start := time.Now()
	mustUpdate(t, dir, id, map[string]Entry{"B": entry("b", time.Now(), "2")}, nil, false)
	if waited := time.Since(start); waited < 250*time.Millisecond {
		t.Errorf("Update returned after %s, before the lock was released", waited)
	}
	assertKeys(t, mustLoad(t, dir, id), "A", "B")
}

func TestUpdateGivesUpOnALockHeldTooLong(t *testing.T) {
	dir := isolate(t)
	id := testIdentity()
	mustUpdate(t, dir, id, map[string]Entry{"A": entry("a", time.Now(), "1")}, nil, false)
	before, _ := os.ReadFile(dataPath(dir, id))
	holdLock(t, dir)

	start := time.Now()
	err := Update(dir, id, map[string]Entry{"B": entry("b", time.Now(), "2")}, nil, false)
	waited := time.Since(start)
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("err = %v, want one wrapping ErrLockTimeout", err)
	}
	// Scheduler slack on top of the bound plus one poll interval.
	if limit := lockTimeout + lockPoll + 150*time.Millisecond; waited < lockTimeout || waited > limit {
		t.Errorf("Update waited %s, want between %s and %s", waited, lockTimeout, limit)
	}
	if after, _ := os.ReadFile(dataPath(dir, id)); !bytes.Equal(before, after) {
		t.Fatal("data file changed after a lock timeout")
	}
}

func TestLoadTakesNoLock(t *testing.T) {
	dir := isolate(t)
	id := testIdentity()
	mustUpdate(t, dir, id, map[string]Entry{"A": entry("a", time.Now(), "1")}, nil, false)
	holdLock(t, dir)

	start := time.Now()
	got := mustLoad(t, dir, id)
	if waited := time.Since(start); waited > 200*time.Millisecond {
		t.Errorf("Load took %s with the lock held", waited)
	}
	assertKeys(t, got, "A")
}

func TestLoadNeverCreatesTheLockFile(t *testing.T) {
	dir := isolate(t)
	id := testIdentity()
	mustUpdate(t, dir, id, map[string]Entry{"A": entry("a", time.Now(), "1")}, nil, false)
	if err := os.Remove(lockPath(dir, id)); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, mustLoad(t, dir, id), "A")
	if _, err := os.Lstat(lockPath(dir, id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load touched the lock file: %v", err)
	}
}

func TestAbortBeforeRenameKeepsThePreviousFile(t *testing.T) {
	dir := isolate(t)
	id := testIdentity()
	mustUpdate(t, dir, id, map[string]Entry{"A": entry("a", time.Now(), "1")}, nil, false)
	before, _ := os.ReadFile(dataPath(dir, id))

	testHookBeforeRename = func() error { return errors.New("simulated crash") }
	err := Update(dir, id, map[string]Entry{"B": entry("b", time.Now(), "2")}, nil, false)
	testHookBeforeRename = nil
	if err == nil {
		t.Fatal("aborted Update returned nil")
	}
	if after, _ := os.ReadFile(dataPath(dir, id)); !bytes.Equal(before, after) {
		t.Fatal("data file changed by an aborted update")
	}
	assertKeys(t, mustLoad(t, dir, id), "A")
	temps, _ := filepath.Glob(filepath.Join(dir, ".*.json.tmp-*"))
	if len(temps) != 1 {
		t.Fatalf("leftover temp files = %v, want 1", temps)
	}

	mustUpdate(t, dir, id, map[string]Entry{"C": entry("c", time.Now(), "3")}, nil, false)
	assertKeys(t, mustLoad(t, dir, id), "A", "C")
	if temps, _ := filepath.Glob(filepath.Join(dir, ".*.json.tmp-*")); len(temps) != 0 {
		t.Fatalf("leftover temp files after the next update: %v", temps)
	}
}
