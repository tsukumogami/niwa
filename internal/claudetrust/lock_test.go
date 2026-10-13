package claudetrust

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLockHeldFreshSkips(t *testing.T) {
	f := newFixture(t)
	fastLock(t, 200*time.Millisecond)
	lock := f.file + lockSuffix
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	out := Ensure(f.env, f.root, nil, SourceWorkspace)
	elapsed := time.Since(start)
	if !strings.Contains(out.Warning, lock) || !strings.Contains(out.Warning, f.file) || out.Grant != nil {
		t.Errorf("outcome = %+v, want a skip naming %s", out, lock)
	}
	if elapsed < 200*time.Millisecond || elapsed > 2*time.Second {
		t.Errorf("waited %s, want about the 200ms override", elapsed)
	}
	if out.Trusted {
		t.Error("Trusted = true on a lock timeout")
	}
	if _, err := os.Stat(f.file); !os.IsNotExist(err) {
		t.Error("config written without the lock")
	}
	if info, err := os.Lstat(lock); err != nil || !info.IsDir() {
		t.Error("another holder's fresh lock was removed")
	}

	// Once a wait on this path timed out, a later Ensure against it doesn't
	// wait again.
	lockWait = 3 * time.Second
	start = time.Now()
	again := Ensure(f.env, f.root, nil, SourceWorkspace)
	if d := time.Since(start); d > time.Second {
		t.Errorf("second attempt waited %s, want an immediate skip", d)
	}
	if again.Warning == "" || again.Grant != nil || again.Trusted {
		t.Errorf("second outcome = %+v, want an immediate skip", again)
	}
}

// TestLockWithdrawWaitsAfterTimeout checks a timeout doesn't stop a later
// withdrawal from trying: it waits for the lock again, skips while the lock
// is still held, and goes through once it is free, so one busy moment in a
// teardown sweep doesn't leave every later instance's entry behind.
func TestLockWithdrawWaitsAfterTimeout(t *testing.T) {
	f := newFixture(t)
	fastLock(t, 150*time.Millisecond)
	lock := f.file + lockSuffix
	writeFile(t, f.file, `{"projects": {`+jsonKey(t, f.key)+`: {"hasTrustDialogAccepted": true}}}`, 0o600)
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(f.root), "inst-2")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if out := Ensure(f.env, other, nil, SourceWorkspace); out.Warning == "" || out.Trusted {
		t.Fatalf("outcome = %+v, want a lock timeout", out)
	}

	g := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}
	before := snap(t, f.file)
	start := time.Now()
	if w := Withdraw(g, f.key, Destroy); !strings.Contains(w, lock) {
		t.Errorf("withdraw warning = %q, want a skip naming %s", w, lock)
	}
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Errorf("withdraw gave up after %s, want it to wait for the lock again", d)
	}
	assertUntouched(t, f.file, before)

	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if w := Withdraw(g, f.key, Destroy); w != "" {
		t.Errorf("withdraw warning = %q after the lock was freed, want none", w)
	}
	if e, ok := entryOf(t, f.file, f.key); ok {
		t.Errorf("entry = %v, want it removed", e)
	}
	assertClean(t, f.file)
}

// TestLockTimeoutIsPerPath checks a timeout on one config file's lock doesn't
// make Ensure skip another file.
func TestLockTimeoutIsPerPath(t *testing.T) {
	f := newFixture(t)
	fastLock(t, 100*time.Millisecond)
	if err := os.Mkdir(f.file+lockSuffix, 0o700); err != nil {
		t.Fatal(err)
	}
	if out := Ensure(f.env, f.root, nil, SourceWorkspace); out.Warning == "" || out.Trusted {
		t.Fatalf("outcome = %+v, want a lock timeout", out)
	}

	dir := filepath.Join(filepath.Dir(f.env.Home), "claude-b")
	env := Env{Home: f.env.Home, ClaudeConfigDir: dir}
	out := Ensure(env, f.root, nil, SourceWorkspace)
	if out.Warning != "" || !out.Trusted || out.Grant == nil || out.Grant.ConfigFile != filepath.Join(dir, ".claude.json") {
		t.Errorf("outcome = %+v (grant %+v), want a write to the other file", out, out.Grant)
	}
}

// TestLockDefaultWait pins the default wait. The shortened-wait tests above
// cover what the wait does; this keeps the default from drifting.
func TestLockDefaultWait(t *testing.T) {
	resetLockState(t)
	if defaultLockWait != 5*time.Second || lockWait != defaultLockWait {
		t.Errorf("default lock wait = %s (current %s), want 5s", defaultLockWait, lockWait)
	}
	if defaultLockWait >= defaultLockStale {
		t.Errorf("default lock wait %s is not below the staleness threshold %s", defaultLockWait, defaultLockStale)
	}
}

// TestLockReleaseLeavesASuccessorsLock checks the release removes only the
// lock directory it created: one that replaced it (another process took the
// lock over as stale) is left for its holder.
func TestLockReleaseLeavesASuccessorsLock(t *testing.T) {
	f := newFixture(t)
	lock := f.file + lockSuffix
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		t.Fatal(err)
	}

	release, err := acquireLock(lock, true)
	if err != nil {
		t.Fatal(err)
	}
	// The successor's directory exists before ours is removed, so it can't
	// reuse our directory's inode.
	successor := filepath.Join(filepath.Dir(lock), "successor")
	if err := os.Mkdir(successor, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(successor, lock); err != nil {
		t.Fatal(err)
	}
	theirs, err := os.Lstat(lock)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if cur, err := os.Lstat(lock); err != nil || !os.SameFile(theirs, cur) {
		t.Errorf("the successor's lock was removed or replaced (err %v)", err)
	}

	// With the lock still ours, the release removes it.
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	release, err = acquireLock(lock, true)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Errorf("our own lock was left behind (err %v)", err)
	}
}

func TestLockWaitsForRelease(t *testing.T) {
	f := newFixture(t)
	lock := f.file + lockSuffix
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = os.Remove(lock)
	}()
	out := Ensure(f.env, f.root, nil, SourceWorkspace)
	if out.Warning != "" || out.Grant == nil {
		t.Errorf("outcome = %+v, want the write after the holder released", out)
	}
	assertClean(t, f.file)
}

func TestLockStaleIsTakenOver(t *testing.T) {
	f := newFixture(t)
	fastLock(t, 200*time.Millisecond)
	lock := f.file + lockSuffix
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-11 * time.Second)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	out := Ensure(f.env, f.root, nil, SourceWorkspace)
	if out.Warning != "" || out.Grant == nil || !out.Changed {
		t.Errorf("outcome = %+v, want the write to proceed", out)
	}
	assertClean(t, f.file)
}

func TestLockStaleWithContentsIsNotRemoved(t *testing.T) {
	f := newFixture(t)
	fastLock(t, 200*time.Millisecond)
	lock := f.file + lockSuffix
	inside := filepath.Join(lock, "keep")
	writeFile(t, inside, "x", 0o600)
	old := time.Now().Add(-11 * time.Second)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	out := Ensure(f.env, f.root, nil, SourceWorkspace)
	if out.Warning == "" || out.Grant != nil {
		t.Errorf("outcome = %+v, want a skip", out)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Errorf("content inside a stale lock was removed: %v", err)
	}
}

func TestLockPathNotADirectoryIsNeverFollowedOrRemoved(t *testing.T) {
	old := time.Now().Add(-11 * time.Second)
	cases := map[string]func(t *testing.T, lock, elsewhere string){
		"symlink to a stale directory": func(t *testing.T, lock, elsewhere string) {
			// The target is a directory old enough to be stale; following
			// the link would make it look like a stale lock to take over.
			if err := os.Symlink(elsewhere, lock); err != nil {
				t.Fatal(err)
			}
		},
		"regular file": func(t *testing.T, lock, _ string) {
			writeFile(t, lock, "not a lock", 0o600)
			if err := os.Chtimes(lock, old, old); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			fastLock(t, 200*time.Millisecond)
			lock := f.file + lockSuffix
			elsewhere := filepath.Join(filepath.Dir(f.env.Home), "elsewhere")
			sentinel := filepath.Join(elsewhere, "sentinel")
			writeFile(t, sentinel, "s", 0o600)
			if err := os.Chtimes(elsewhere, old, old); err != nil {
				t.Fatal(err)
			}
			plant(t, lock, elsewhere)
			before, err := os.Lstat(lock)
			if err != nil {
				t.Fatal(err)
			}

			out := Ensure(f.env, f.root, nil, SourceWorkspace)
			if out.Warning == "" || out.Grant != nil {
				t.Errorf("outcome = %+v, want a skip", out)
			}
			after, err := os.Lstat(lock)
			if err != nil || !os.SameFile(before, after) || after.Mode().Type() != before.Mode().Type() {
				t.Errorf("lock path changed: before %v after %v err %v", before.Mode(), after, err)
			}
			if _, err := os.Stat(sentinel); err != nil {
				t.Errorf("symlink target touched: %v", err)
			}
			if _, err := os.Stat(f.file); !os.IsNotExist(err) {
				t.Error("config written without the lock")
			}
		})
	}
}

func TestInjectedFailureBeforeRenameLeavesOriginal(t *testing.T) {
	f := newFixture(t)
	original := `{"keep": 1, "projects": {` + jsonKey(t, f.key) + `: {"hasTrustDialogAccepted": false}}}`
	writeFile(t, f.file, original, 0o644)
	before := snap(t, f.file)
	var staged string
	beforeRename = func(tmp string) error {
		staged = tmp
		if _, err := os.Stat(tmp); err != nil {
			t.Errorf("temp file missing at the hook: %v", err)
		}
		assertMode(t, tmp, 0o600)
		return errors.New("injected failure")
	}

	out := Ensure(f.env, f.root, nil, SourceWorkspace)
	if !strings.Contains(out.Warning, "injected failure") || !strings.Contains(out.Warning, f.file) || out.Grant != nil || out.Changed {
		t.Errorf("outcome = %+v, want a skip carrying the injected failure", out)
	}
	if staged == "" || filepath.Dir(staged) != filepath.Dir(f.file) {
		t.Errorf("temp file %q not beside the config", staged)
	}
	assertUntouched(t, f.file, before)
	assertClean(t, f.file)

	g := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeFlagOnly, Source: SourceWorkspace}
	writeFile(t, f.file, `{"projects": {`+jsonKey(t, f.key)+`: {"hasTrustDialogAccepted": true}}}`, 0o644)
	before = snap(t, f.file)
	if w := Withdraw(g, f.key, Destroy); !strings.Contains(w, "injected failure") {
		t.Errorf("withdraw warning = %q, want the injected failure", w)
	}
	assertUntouched(t, f.file, before)
	assertClean(t, f.file)
}

func TestConcurrentEnsureAllLand(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.file, `{"keep": true}`, 0o600)
	const n = 10
	roots := make([]string, n)
	for i := range roots {
		roots[i] = filepath.Join(filepath.Dir(f.root), fmt.Sprintf("concurrent-%d", i))
		if err := os.Mkdir(roots[i], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	outs := make([]Outcome, n)
	var wg sync.WaitGroup
	for i := range roots {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outs[i] = Ensure(f.env, roots[i], nil, SourceWorkspace)
		}(i)
	}
	wg.Wait()

	for i, out := range outs {
		if out.Warning != "" || out.Grant == nil || out.Grant.Mode != ModeCreated {
			t.Errorf("root %d outcome = %+v", i, out)
		}
		if e, ok := entryOf(t, f.file, mustKey(t, roots[i])); !ok || e["hasTrustDialogAccepted"] != true {
			t.Errorf("root %d entry missing: %v", i, e)
		}
	}
	if decodeAny(t, readFile(t, f.file))["keep"] != true {
		t.Error("other key lost under concurrency")
	}
	assertClean(t, f.file)
}
