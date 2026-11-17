//go:build unix

package workspace

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// faultFileExists reports whether path names an existing file system entry.
func faultFileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// TestEnsureConfigSnapshot_MappingWrittenDuringHeldRefreshSurvives reproduces
// the writer side of #297. A refresh copies .niwa/sessions into its staging
// directory and only then swaps staging over .niwa. A session mapping written
// between the copy and the swap lands in the .niwa that the swap is about to
// throw away, so the dispatch that wrote it loses its resume handle.
//
// The refresh is parked at the snapshot-swap mark: the preserve copies are
// done, the renames are not. The mapping write then starts. Once the write has
// either finished or is observed waiting at snapshot-lock-wait, the refresh is
// let go. The mapping must be readable from the post-swap .niwa/sessions, and
// the write must have waited for the refresh to finish its swap.
//
// No assertion depends on elapsed time: the refresh is parked through its
// hold file, and the writer's wait is observed through P.held.
func TestEnsureConfigSnapshot_MappingWrittenDuringHeldRefreshSurvives(t *testing.T) {
	root, configDir := planSnapshotWorkspace(t)
	faults := t.TempDir()
	releaseRefresh := filepath.Join(faults, "release-refresh")
	lockWait := filepath.Join(faults, "lock-wait")

	// P is pre-created, so a waiter at snapshot-lock-wait records P.held and
	// keeps polling the lock instead of parking in the hold itself.
	if err := os.WriteFile(lockWait, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NIWA_TEST_FAULT",
		"hold:"+releaseRefresh+"@snapshot-swap,hold:"+lockWait+"@snapshot-lock-wait")

	fetcher := driftedFetcher(t)
	refreshDone := make(chan error, 1)
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			_ = os.WriteFile(releaseRefresh, nil, 0o600)
		})
	}
	go func() {
		refreshDone <- EnsureConfigSnapshot(context.Background(), configDir, fetcher, nil)
	}()

	var refreshErr error
	refreshEnded := false
	// Let a parked refresh go and join it before the temp dirs are removed,
	// whatever way the test ends.
	t.Cleanup(func() {
		release()
		if !refreshEnded {
			select {
			case <-refreshDone:
			case <-time.After(3 * time.Minute):
			}
		}
	})

	// Wait for the refresh to park between its preserve copies and its swap.
	hangGuard := time.After(60 * time.Second)
	for !faultFileExists(releaseRefresh + ".held") {
		select {
		case refreshErr = <-refreshDone:
			refreshEnded = true
			t.Fatalf("the refresh ended before reaching the snapshot-swap hold: %v", refreshErr)
		case <-hangGuard:
			t.Fatal("the refresh never reached the snapshot-swap hold")
		case <-time.After(20 * time.Millisecond):
		}
	}

	mapping := SessionMapping{
		SessionID:    preservedSessionID,
		InstanceName: "ws+task-0000beef",
		InstancePath: filepath.Join(root, "ws+task-0000beef"),
		Agent:        "claude",
		Ephemeral:    true,
		Origin:       "dispatch",
	}
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- WriteSessionMapping(root, mapping)
	}()

	// The write either waits for the refresh's lock (P.held appears) or, with
	// nothing to wait on, runs to completion.
	var writeErr error
	writeEnded := false
	hangGuard = time.After(60 * time.Second)
	for !writeEnded && !faultFileExists(lockWait+".held") {
		select {
		case writeErr = <-writeDone:
			writeEnded = true
		case <-hangGuard:
			t.Fatal("the mapping write neither finished nor waited for the config lock")
		case <-time.After(20 * time.Millisecond):
		}
	}

	release()
	select {
	case refreshErr = <-refreshDone:
		refreshEnded = true
	case <-time.After(3 * time.Minute):
		t.Fatal("the released refresh never ended")
	}
	if !writeEnded {
		select {
		case writeErr = <-writeDone:
			writeEnded = true
		case <-time.After(3 * time.Minute):
			t.Fatal("the mapping write never ended after the refresh was released")
		}
	}

	if refreshErr != nil {
		t.Fatalf("refresh: %v", refreshErr)
	}
	if writeErr != nil {
		t.Fatalf("writing the session mapping during the held refresh: %v", writeErr)
	}

	// The refresh really swapped: upstream content is in place.
	if got, err := os.ReadFile(filepath.Join(configDir, "workspace.toml")); err != nil {
		t.Fatalf("workspace.toml missing after refresh: %v", err)
	} else if string(got) != "name = updated" {
		t.Errorf("upstream content not refreshed: got %q", got)
	}

	got, err := ReadSessionMapping(root, preservedSessionID)
	if err != nil {
		t.Fatalf("the mapping written while the refresh was held was lost by its swap: %v", err)
	}
	if got.InstancePath != mapping.InstancePath || got.Agent != mapping.Agent {
		t.Errorf("session mapping changed across the held refresh\n  wrote: %+v\n  read:  %+v", mapping, got)
	}

	if !faultFileExists(lockWait + ".held") {
		t.Errorf("the mapping write never waited for the config lock (no %s.held)", lockWait)
	}
}
