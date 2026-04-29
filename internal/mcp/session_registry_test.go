package mcp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// deadPID returns a PID that is virtually guaranteed not to exist.
const deadPID = 999999999

// liveEntry returns a SessionEntry whose PID is the current process (always alive).
func liveEntry(role string) SessionEntry {
	pid := os.Getpid()
	start, _ := PIDStartTime(pid)
	return SessionEntry{
		ID:           "test-live",
		Role:         role,
		PID:          pid,
		StartTime:    start,
		InboxDir:     "/tmp/inbox",
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// staleEntry returns a SessionEntry whose PID does not exist (dead).
func staleEntry(role string) SessionEntry {
	return SessionEntry{
		ID:           "test-stale",
		Role:         role,
		PID:          deadPID,
		StartTime:    0,
		InboxDir:     "/tmp/inbox",
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}
}

func writeRegistry(t *testing.T, dir string, reg SessionRegistry) {
	t.Helper()
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), data, 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}
}

func readRegistry(t *testing.T, dir string) SessionRegistry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var reg SessionRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("unmarshal registry: %v", err)
	}
	return reg
}

// TestWriteSessionEntry_NewEntry verifies that a new entry is written when the
// registry is empty (no existing sessions.json).
func TestWriteSessionEntry_NewEntry(t *testing.T) {
	dir := t.TempDir()
	entry := liveEntry("worker")

	if err := WriteSessionEntry(dir, entry); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reg := readRegistry(t, dir)
	if len(reg.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(reg.Sessions))
	}
	if reg.Sessions[0].ID != entry.ID {
		t.Errorf("expected ID %q, got %q", entry.ID, reg.Sessions[0].ID)
	}
}

// TestWriteSessionEntry_LiveEntryBlocked verifies that WriteSessionEntry returns
// ErrAlreadyRegistered (wrapped) when a live session for the same role exists.
func TestWriteSessionEntry_LiveEntryBlocked(t *testing.T) {
	dir := t.TempDir()
	existing := liveEntry("coordinator")
	writeRegistry(t, dir, SessionRegistry{Sessions: []SessionEntry{existing}})

	newcomer := SessionEntry{
		ID:           "test-newcomer",
		Role:         "coordinator",
		PID:          os.Getpid(),
		StartTime:    existing.StartTime,
		InboxDir:     "/tmp/inbox",
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}
	err := WriteSessionEntry(dir, newcomer)
	if err == nil {
		t.Fatal("expected ErrAlreadyRegistered, got nil")
	}
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("expected errors.Is(err, ErrAlreadyRegistered), got: %v", err)
	}

	// Registry must still contain only the original entry.
	reg := readRegistry(t, dir)
	if len(reg.Sessions) != 1 {
		t.Fatalf("expected 1 session after rejected write, got %d", len(reg.Sessions))
	}
	if reg.Sessions[0].ID != existing.ID {
		t.Errorf("expected original entry to remain, got ID %q", reg.Sessions[0].ID)
	}
}

// TestWriteSessionEntry_StaleEntryReplaced verifies that a stale entry (dead PID)
// for the same role is pruned and replaced by the new entry.
func TestWriteSessionEntry_StaleEntryReplaced(t *testing.T) {
	dir := t.TempDir()
	stale := staleEntry("coordinator")
	writeRegistry(t, dir, SessionRegistry{Sessions: []SessionEntry{stale}})

	fresh := SessionEntry{
		ID:           "test-fresh",
		Role:         "coordinator",
		PID:          os.Getpid(),
		StartTime:    0,
		InboxDir:     "/tmp/inbox",
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := WriteSessionEntry(dir, fresh); err != nil {
		t.Fatalf("unexpected error replacing stale entry: %v", err)
	}

	reg := readRegistry(t, dir)
	if len(reg.Sessions) != 1 {
		t.Fatalf("expected 1 session after replacement, got %d", len(reg.Sessions))
	}
	if reg.Sessions[0].ID != fresh.ID {
		t.Errorf("expected fresh entry ID %q, got %q", fresh.ID, reg.Sessions[0].ID)
	}
}
