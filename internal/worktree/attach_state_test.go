package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	niwaDir := filepath.Join(dir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o700); err != nil {
		t.Fatalf("mkdir .niwa: %v", err)
	}
	return dir
}

func TestAttachStatePathHelpers(t *testing.T) {
	wt := "/some/worktree"
	if got, want := AttachLockPath(wt), "/some/worktree/.niwa/attach.lock"; got != want {
		t.Errorf("AttachLockPath = %q, want %q", got, want)
	}
	if got, want := AttachStatePath(wt), "/some/worktree/.niwa/attach.state"; got != want {
		t.Errorf("AttachStatePath = %q, want %q", got, want)
	}
}

func TestWriteReadAttachStateRoundTrip(t *testing.T) {
	wt := setupWorktree(t)
	myPID := os.Getpid()
	myStart, _ := PIDStartTime(myPID)
	state := AttachState{
		V:              1,
		OwnerPID:       myPID,
		OwnerStartTime: myStart,
		StartedAt:      time.Now().UTC().Format(time.RFC3339),
		LockPath:       ".niwa/attach.lock",
	}
	if err := WriteAttachState(wt, state); err != nil {
		t.Fatalf("WriteAttachState: %v", err)
	}
	got, avail, err := ReadAttachState(wt, false)
	if err != nil {
		t.Fatalf("ReadAttachState: %v", err)
	}
	if avail != AttachAttached {
		t.Errorf("avail = %v, want %v (own PID is alive)", avail, AttachAttached)
	}
	if got.OwnerPID != state.OwnerPID || got.StartedAt != state.StartedAt {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, state)
	}
}

func TestWriteAttachStateMode0600(t *testing.T) {
	wt := setupWorktree(t)
	if err := WriteAttachState(wt, AttachState{V: 1, OwnerPID: 1, StartedAt: "now", LockPath: ".niwa/attach.lock"}); err != nil {
		t.Fatalf("WriteAttachState: %v", err)
	}
	info, err := os.Stat(AttachStatePath(wt))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}
}

func TestReadAttachStateMissingReturnsAvailable(t *testing.T) {
	wt := setupWorktree(t)
	got, avail, err := ReadAttachState(wt, false)
	if err != nil {
		t.Fatalf("ReadAttachState (missing): %v", err)
	}
	if got != nil {
		t.Errorf("got = %+v, want nil", got)
	}
	if avail != AttachAvailable {
		t.Errorf("avail = %v, want %v", avail, AttachAvailable)
	}
}

func TestReadAttachStateStaleDetection(t *testing.T) {
	wt := setupWorktree(t)
	// PID 1 (init) is alive on every Linux system, so we use a deliberately
	//-implausible PID with a wrong start time to force IsPIDAlive==false.
	// Use os.Getpid() with a different start time so the (pid, start) tuple
	// won't match.
	state := AttachState{
		V:              1,
		OwnerPID:       os.Getpid(),
		OwnerStartTime: 1, // bogus start time -- different from real
		StartedAt:      "2026-05-10T14:32:11Z",
		LockPath:       ".niwa/attach.lock",
	}
	if err := WriteAttachState(wt, state); err != nil {
		t.Fatalf("WriteAttachState: %v", err)
	}
	got, avail, err := ReadAttachState(wt, false)
	if err != nil {
		t.Fatalf("ReadAttachState: %v", err)
	}
	if avail != AttachStale {
		t.Errorf("avail = %v, want %v (start time mismatch should look dead)", avail, AttachStale)
	}
	if got == nil || got.OwnerPID != state.OwnerPID {
		t.Errorf("got = %+v, want sentinel echoed back", got)
	}
	// Sentinel still present (we passed reapStale=false).
	if _, err := os.Stat(AttachStatePath(wt)); err != nil {
		t.Errorf("sentinel removed unexpectedly: %v", err)
	}
}

func TestReadAttachStateReapStaleDeletes(t *testing.T) {
	wt := setupWorktree(t)
	state := AttachState{V: 1, OwnerPID: os.Getpid(), OwnerStartTime: 1, StartedAt: "x", LockPath: ".niwa/attach.lock"}
	if err := WriteAttachState(wt, state); err != nil {
		t.Fatalf("WriteAttachState: %v", err)
	}
	_, avail, err := ReadAttachState(wt, true)
	if err != nil {
		t.Fatalf("ReadAttachState reap: %v", err)
	}
	if avail != AttachStale {
		t.Errorf("avail = %v, want %v", avail, AttachStale)
	}
	if _, err := os.Stat(AttachStatePath(wt)); !os.IsNotExist(err) {
		t.Errorf("sentinel still present after reap: %v", err)
	}
}

func TestRemoveAttachStateIdempotent(t *testing.T) {
	wt := setupWorktree(t)
	// Removing a missing file is a no-op.
	if err := RemoveAttachState(wt); err != nil {
		t.Fatalf("RemoveAttachState (missing): %v", err)
	}
	state := AttachState{V: 1, OwnerPID: 1, StartedAt: "x", LockPath: ".niwa/attach.lock"}
	if err := WriteAttachState(wt, state); err != nil {
		t.Fatalf("WriteAttachState: %v", err)
	}
	if err := RemoveAttachState(wt); err != nil {
		t.Fatalf("RemoveAttachState: %v", err)
	}
	if _, err := os.Stat(AttachStatePath(wt)); !os.IsNotExist(err) {
		t.Errorf("file still present after remove: %v", err)
	}
}

func TestReadAttachStateParseError(t *testing.T) {
	wt := setupWorktree(t)
	if err := os.WriteFile(AttachStatePath(wt), []byte("not json"), 0o600); err != nil {
		t.Fatalf("seed bad json: %v", err)
	}
	_, avail, err := ReadAttachState(wt, false)
	if err == nil {
		t.Fatalf("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parsing attach state") {
		t.Errorf("error = %q, want substring 'parsing attach state'", err.Error())
	}
	if avail != AttachAvailable {
		t.Errorf("avail = %v, want default %v on parse failure", avail, AttachAvailable)
	}
}
