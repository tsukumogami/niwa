//go:build unix

package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/source"
)

// These tests observe a waiter only through the P.held file the
// snapshot-lock-wait mark writes. With P not created yet, the waiter parks in
// the hold and can't return until the test creates P, so "hasn't returned yet"
// is deterministic. Deadlines are hang guards only.

// holdWorkspaceSnapshotLock takes the snapshot lock of root's .niwa until the
// returned release is called or the test ends.
func holdWorkspaceSnapshotLock(t *testing.T, root string) func() {
	t.Helper()
	release, err := config.AcquireSnapshotLock(filepath.Join(root, StateDir))
	if err != nil {
		t.Fatalf("holding the snapshot lock: %v", err)
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

// parkAtLockWait sets the hold fault at snapshot-lock-wait on a fresh P and
// makes sure P exists when the test ends.
func parkAtLockWait(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "lock-wait")
	t.Cleanup(func() { _ = os.WriteFile(p, nil, 0o600) })
	t.Setenv("NIWA_TEST_FAULT", "hold:"+p+"@snapshot-lock-wait")
	return p
}

// awaitParked waits for p.held, failing if done delivers first.
func awaitParked(t *testing.T, p string, done <-chan error) {
	t.Helper()
	guard := time.After(60 * time.Second)
	for !faultFileExists(p + ".held") {
		select {
		case err := <-done:
			t.Fatalf("returned (%v) without waiting for the snapshot lock", err)
		case <-guard:
			t.Fatal("never reached the snapshot-lock-wait mark")
		case <-time.After(20 * time.Millisecond):
		}
	}
	select {
	case err := <-done:
		t.Fatalf("returned (%v) while the snapshot lock was held", err)
	default:
	}
}

// letGo releases the holder, unparks the waiter and returns its result.
func letGo(t *testing.T, releaseHolder func(), p string, done <-chan error) error {
	t.Helper()
	releaseHolder()
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		return err
	case <-time.After(60 * time.Second):
		t.Fatal("never returned after the snapshot lock was released")
		return nil
	}
}

func TestWriteSessionMapping_BlocksWhileSnapshotLockHeld(t *testing.T) {
	root := t.TempDir()
	releaseHolder := holdWorkspaceSnapshotLock(t, root)
	p := parkAtLockWait(t)

	mapping := SessionMapping{SessionID: preservedSessionID, InstancePath: filepath.Join(root, "ws+a"), Agent: "claude"}
	done := make(chan error, 1)
	go func() { done <- WriteSessionMapping(root, mapping) }()

	awaitParked(t, p, done)
	if _, err := ReadSessionMapping(root, preservedSessionID); err == nil {
		t.Fatal("the mapping was written while the snapshot lock was held")
	}

	if err := letGo(t, releaseHolder, p, done); err != nil {
		t.Fatalf("WriteSessionMapping: %v", err)
	}
	if _, err := ReadSessionMapping(root, preservedSessionID); err != nil {
		t.Fatalf("the mapping isn't there after the write: %v", err)
	}
}

func TestDeleteSessionMapping_BlocksWhileSnapshotLockHeld(t *testing.T) {
	t.Setenv("NIWA_TEST_FAULT", "")
	root := t.TempDir()
	if err := WriteSessionMapping(root, SessionMapping{SessionID: preservedSessionID, Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	releaseHolder := holdWorkspaceSnapshotLock(t, root)
	p := parkAtLockWait(t)

	done := make(chan error, 1)
	go func() { done <- DeleteSessionMapping(root, preservedSessionID) }()

	awaitParked(t, p, done)
	if _, err := ReadSessionMapping(root, preservedSessionID); err != nil {
		t.Fatalf("the mapping was deleted while the snapshot lock was held: %v", err)
	}

	if err := letGo(t, releaseHolder, p, done); err != nil {
		t.Fatalf("DeleteSessionMapping: %v", err)
	}
	if _, err := ReadSessionMapping(root, preservedSessionID); err == nil {
		t.Fatal("the mapping is still there after the delete")
	}
}

// TestSnapshotEntryPoints_NeverWaitOnThemselves turns a double acquisition
// into an immediate failure. flock contends per open file description, so an
// entry point that took its own lock twice would wait on itself for the whole
// bound. With nothing else holding the lock, any wait at all is that bug, and
// the error spec fails it at once.
func TestSnapshotEntryPoints_NeverWaitOnThemselves(t *testing.T) {
	t.Setenv("NIWA_TEST_FAULT", "error:self-wait@snapshot-lock-wait")
	ctx := context.Background()

	t.Run("EnsureConfigSnapshot refresh", func(t *testing.T) {
		_, configDir := planSnapshotWorkspace(t)
		if err := EnsureConfigSnapshot(ctx, configDir, driftedFetcher(t), nil); err != nil {
			t.Fatalf("refresh: %v", err)
		}
	})

	t.Run("MaterializeFromSource", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "parent", ".niwa")
		fetcher := &fakeFetcher{tarball: makeFakeTarball(t, map[string]string{
			"wrap/":                     "",
			"wrap/.niwa/":               "",
			"wrap/.niwa/workspace.toml": "name = team",
		}), commitOID: "abc"}
		src := source.Source{Owner: "org", Repo: "repo"}
		if _, err := MaterializeFromSource(ctx, src, "org/repo", dir, config.TeamConfigMarkerSet(), fetcher, nil); err != nil {
			t.Fatalf("MaterializeFromSource: %v", err)
		}
		if _, err := os.Stat(config.SnapshotLockPath(dir)); err != nil {
			t.Errorf("no lock file beside the materialized dir: %v", err)
		}
	})

	t.Run("EnsureOverlaySnapshot fresh then refresh", func(t *testing.T) {
		tmp := t.TempDir()
		remote := overlaysyncMakeBareRepo(t, filepath.Join(tmp, "remote"), "workspace-overlay.toml", `name = "demo"`)
		dir := filepath.Join(tmp, "overlays", "snapshot")
		fresh, _, err := EnsureOverlaySnapshot(ctx, remote, dir, nil, nil)
		if err != nil || !fresh {
			t.Fatalf("fresh overlay: fresh=%v err=%v", fresh, err)
		}
		fresh, _, err = EnsureOverlaySnapshot(ctx, remote, dir, nil, nil)
		if err != nil || fresh {
			t.Fatalf("overlay refresh: fresh=%v err=%v", fresh, err)
		}
	})
}

func TestEnsureConfigSnapshotWithStatus_MissingParentTakesNoLock(t *testing.T) {
	t.Setenv("NIWA_TEST_FAULT", "")
	parent := filepath.Join(t.TempDir(), "absent")
	configDir := filepath.Join(parent, "global")

	converted, rank, err := EnsureConfigSnapshotWithStatus(context.Background(), configDir, config.MarkerSet{}, nil, nil)
	if err != nil || converted || rank != 0 {
		t.Fatalf("got (%v, %d, %v), want (false, 0, nil)", converted, rank, err)
	}
	if _, err := os.Lstat(parent); !os.IsNotExist(err) {
		t.Errorf("the missing parent was created: %v", err)
	}
}
