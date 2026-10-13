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
	if _, err := os.Stat(f.file); !os.IsNotExist(err) {
		t.Error("config written without the lock")
	}
	if info, err := os.Lstat(lock); err != nil || !info.IsDir() {
		t.Error("another holder's fresh lock was removed")
	}

	// Once a wait timed out, later attempts in the process don't wait again.
	lockWait = 3 * time.Second
	start = time.Now()
	again := Ensure(f.env, f.root, nil, SourceWorkspace)
	if d := time.Since(start); d > time.Second {
		t.Errorf("second attempt waited %s, want an immediate skip", d)
	}
	if again.Warning == "" || again.Grant != nil {
		t.Errorf("second outcome = %+v, want an immediate skip", again)
	}
	g := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}
	writeFile(t, f.file, `{"projects": {`+jsonKey(t, f.key)+`: {"hasTrustDialogAccepted": true}}}`, 0o600)
	before := snap(t, f.file)
	if w := Withdraw(g, f.key, Destroy); w == "" {
		t.Error("Withdraw after a timeout did not skip")
	}
	assertUntouched(t, f.file, before)
}

func TestLockDefaultWaitSkipsWithinSixSeconds(t *testing.T) {
	if testing.Short() {
		t.Skip("waits the real 5-second default")
	}
	f := newFixture(t)
	if lockWait != 5*time.Second {
		t.Fatalf("default lock wait = %s, want 5s", lockWait)
	}
	if err := os.Mkdir(f.file+lockSuffix, 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	out := Ensure(f.env, f.root, nil, SourceWorkspace)
	elapsed := time.Since(start)
	if out.Warning == "" || out.Grant != nil {
		t.Errorf("outcome = %+v, want a skip", out)
	}
	if elapsed < 5*time.Second || elapsed > 6*time.Second {
		t.Errorf("skipped after %s, want between 5s and 6s", elapsed)
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
