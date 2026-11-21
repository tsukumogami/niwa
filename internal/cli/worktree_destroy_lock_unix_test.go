//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestLoadMappingsForDestroy_WaitsForSnapshotLock pins the locked read: while
// the workspace root's config snapshot lock is held (a refresh mid-swap), the
// read waits, and once the holder lets go it sees the store the holder left.
// The wait is observed through the snapshot-lock-wait mark, never by timing.
func TestLoadMappingsForDestroy_WaitsForSnapshotLock(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, workspace.StateDir)
	releaseHolder, err := config.AcquireSnapshotLock(configDir)
	if err != nil {
		t.Fatal(err)
	}
	held := true
	t.Cleanup(func() {
		if held {
			releaseHolder()
		}
	})

	p := filepath.Join(t.TempDir(), "lock-wait")
	t.Cleanup(func() { _ = os.WriteFile(p, nil, 0o600) })
	t.Setenv("NIWA_TEST_FAULT", "hold:"+p+"@snapshot-lock-wait")

	type result struct {
		mappings []workspace.SessionMapping
		err      error
	}
	done := make(chan result, 1)
	go func() {
		m, err := loadMappingsForDestroy(root)
		done <- result{m, err}
	}()

	guard := time.After(60 * time.Second)
	for {
		if _, err := os.Lstat(p + ".held"); err == nil {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("the read returned (%+v) without waiting for the snapshot lock", r)
		case <-guard:
			t.Fatal("the read never reached the snapshot-lock-wait mark")
		case <-time.After(20 * time.Millisecond):
		}
	}

	// The holder finishes its swap with a mapping in the store, then lets go.
	sessionsDir := filepath.Join(configDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, uuidA+".json"),
		[]byte(`{"session_id":"`+uuidA+`","instance_path":"/ws/a"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	held = false
	releaseHolder()
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("loadMappingsForDestroy: %v", r.err)
		}
		if len(r.mappings) != 1 || r.mappings[0].SessionID != uuidA {
			t.Fatalf("want the mapping the holder left, got %+v", r.mappings)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the read never returned after the lock was released")
	}
}
