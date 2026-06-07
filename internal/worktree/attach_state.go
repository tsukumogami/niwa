package worktree

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// AttachState is the on-disk shape of <worktree>/.niwa/attach.state. It is
// written by `niwa session attach` on lock acquire and removed by the same
// process on clean exit. Stale sentinels (the recorded OwnerPID is dead per
// IsPIDAlive) are detected by readers and may be opportunistically reaped.
//
// The file lives in <worktree>/.niwa/ alongside the session's other
// per-worktree state.
type AttachState struct {
	V              int    `json:"v"`
	OwnerPID       int    `json:"owner_pid"`
	OwnerStartTime int64  `json:"owner_start_time"`
	StartedAt      string `json:"started_at"` // RFC3339 UTC
	LockPath       string `json:"lock_path"`  // ".niwa/attach.lock" (relative to worktree)
}

// AttachAvailability is the computed projection of an attach.state file plus
// its holder liveness. The `niwa session list` renderer uses this enum to
// render the AVAILABILITY column.
type AttachAvailability string

const (
	// AttachAvailable means no sentinel is present; the session is free to attach.
	AttachAvailable AttachAvailability = "available"
	// AttachAttached means a sentinel is present and its OwnerPID is alive.
	AttachAttached AttachAvailability = "attached"
	// AttachStale means a sentinel is present but its OwnerPID is dead per
	// IsPIDAlive. Readers may opportunistically reap such sentinels.
	AttachStale AttachAvailability = "stale"
)

// AttachLockPath returns <worktreePath>/.niwa/attach.lock.
func AttachLockPath(worktreePath string) string {
	return filepath.Join(worktreePath, ".niwa", "attach.lock")
}

// AttachStatePath returns <worktreePath>/.niwa/attach.state.
func AttachStatePath(worktreePath string) string {
	return filepath.Join(worktreePath, ".niwa", "attach.state")
}

// ReadAttachState returns the parsed sentinel and a derived availability.
//
//   - If no sentinel exists, returns (nil, AttachAvailable, nil).
//   - If the sentinel exists and its OwnerPID is alive per IsPIDAlive, returns
//     (state, AttachAttached, nil).
//   - If the sentinel exists but the OwnerPID is dead, returns
//     (state, AttachStale, nil). When reapStale is true, the sentinel is
//     deleted before returning. The deletion is best-effort: failure is logged
//     but never returned.
//
// Parse errors and read errors (other than ENOENT) are returned to the caller
// so they can decide whether to treat them as "available" or surface them.
func ReadAttachState(worktreePath string, reapStale bool) (*AttachState, AttachAvailability, error) {
	path := AttachStatePath(worktreePath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, AttachAvailable, nil
		}
		return nil, AttachAvailable, fmt.Errorf("reading attach state: %w", err)
	}
	var state AttachState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, AttachAvailable, fmt.Errorf("parsing attach state: %w", err)
	}
	if IsPIDAlive(state.OwnerPID, state.OwnerStartTime) {
		return &state, AttachAttached, nil
	}
	if reapStale {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Printf("attach_state: best-effort reap failed for %s: %v", path, err)
		}
	}
	return &state, AttachStale, nil
}

// WriteAttachState atomically writes the sentinel via tmp+rename with mode
// 0600. The .niwa directory must already exist (created by session_create).
func WriteAttachState(worktreePath string, state AttachState) error {
	target := AttachStatePath(worktreePath)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling attach state: %w", err)
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing attach state tmp: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming attach state: %w", err)
	}
	return nil
}

// RemoveAttachState removes the sentinel file. Idempotent: a missing file
// returns nil rather than an error.
func RemoveAttachState(worktreePath string) error {
	path := AttachStatePath(worktreePath)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing attach state: %w", err)
	}
	return nil
}
