package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrAlreadyRegistered is returned by WriteSessionEntry when a live session
// for the same role already exists in the registry.
var ErrAlreadyRegistered = errors.New("already registered")

// WriteSessionEntry atomically updates sessions.json with the given entry.
// Stale entries (dead PIDs) for the same role are pruned automatically.
// Returns ErrAlreadyRegistered (wrapped) when a live session for entry.Role
// already exists.
func WriteSessionEntry(sessionsDir string, entry SessionEntry) error {
	registryPath := filepath.Join(sessionsDir, "sessions.json")

	var registry SessionRegistry
	if data, err := os.ReadFile(registryPath); err == nil {
		_ = json.Unmarshal(data, &registry)
	}

	var kept []SessionEntry
	for _, s := range registry.Sessions {
		if s.Role == entry.Role {
			if IsPIDAlive(s.PID, s.StartTime) {
				return fmt.Errorf("%w: role %q already registered by live session PID %d (registered %s)",
					ErrAlreadyRegistered, entry.Role, s.PID, s.RegisteredAt)
			}
			continue // prune stale entry
		}
		kept = append(kept, s)
	}
	registry.Sessions = append(kept, entry)

	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	tmp := registryPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, registryPath)
}

// maybeRegisterCoordinator writes a SessionEntry to sessions.json as a
// transparent side effect of the first niwa_await_task or niwa_check_messages
// call when s.role == "coordinator". It is a no-op for non-coordinator roles
// and a no-op when a live coordinator entry already exists.
//
// This makes coordinator visibility automatic: a coordinator that has never
// called either tool has no mechanism to receive worker questions anyway, so
// registering on first use is equivalent to explicit registration for the
// purposes of live-session routing.
func (s *Server) maybeRegisterCoordinator() {
	if s.role != "coordinator" || s.instanceRoot == "" {
		return
	}

	sessionsDir := filepath.Join(s.instanceRoot, ".niwa", "sessions")
	registryPath := filepath.Join(sessionsDir, "sessions.json")

	pid := os.Getpid()

	// Check for an existing live coordinator entry.
	var registry SessionRegistry
	if data, err := os.ReadFile(registryPath); err == nil {
		_ = json.Unmarshal(data, &registry)
		for _, entry := range registry.Sessions {
			if entry.Role == "coordinator" && IsPIDAlive(entry.PID, entry.StartTime) {
				return
			}
		}
	}

	startTime, _ := PIDStartTime(pid)
	sessionID := NewSessionID()
	inboxDir := filepath.Join(s.instanceRoot, ".niwa", "roles", s.role, "inbox")

	entry := SessionEntry{
		ID:           sessionID,
		Role:         s.role,
		PID:          pid,
		StartTime:    startTime,
		InboxDir:     inboxDir,
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}

	_ = os.MkdirAll(sessionsDir, 0o700)
	// Ignore ErrAlreadyRegistered: a concurrent registration between our check
	// and our write means the goal is achieved.
	_ = WriteSessionEntry(sessionsDir, entry)
}
