package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// sessionUUIDRe validates Claude session IDs against the canonical UUID
// format (8-4-4-4-12 lowercase hex, with the version/variant nibbles left
// unconstrained so any UUID variant Claude Code emits is accepted). A
// session_id flows from untrusted hook stdin straight into a path component
// and command arguments, so it MUST be validated before use; anything that
// does not match this pattern is rejected without touching the filesystem.
var sessionUUIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidSessionID reports whether id is a syntactically valid Claude session
// id (a lowercase UUID). Callers validate before using the id as a path
// component or command argument.
func ValidSessionID(id string) bool {
	return sessionUUIDRe.MatchString(id)
}

// EphemeralSessionMode reports whether the workspace at workspaceRoot is
// opted in to per-session ephemeral instance provisioning. It reads the
// additive EphemeralSessionMode flag from the workspace-root state file
// (.niwa/instance.json). It is the master switch of the `niwa instance
// from-hook` SessionStart guard: when the workspace has no root state, or
// the flag is absent/false, this returns false and the hook is inert, so an
// ordinary workspace is never touched. A read or parse failure is treated as
// "not enabled" (false), never as an error: the guard must fail safe.
func EphemeralSessionMode(workspaceRoot string) bool {
	state, err := LoadState(workspaceRoot)
	if err != nil {
		return false
	}
	return state.EphemeralSessionMode
}

// SessionMapping records the binding between a Claude Code session and the
// ephemeral niwa instance provisioned for it. It is the single source of
// truth for session teardown and the orphan reaper. Persisted at the
// workspace root under .niwa/sessions/<session_id>.json.
type SessionMapping struct {
	SessionID      string    `json:"session_id"`
	InstanceName   string    `json:"instance_name"`
	InstancePath   string    `json:"instance_path"`
	TranscriptPath string    `json:"transcript_path"`
	Created        time.Time `json:"created"`
	Ephemeral      bool      `json:"ephemeral"`
	// Label is an optional human-friendly alias derived later from the
	// session topic. It is metadata only and is never used to rename the
	// on-disk instance directory. omitempty keeps it absent when unset.
	Label string `json:"label,omitempty"`
}

// sessionsDir returns the workspace-root session mapping directory,
// .niwa/sessions, under workspaceRoot.
func sessionsDir(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, StateDir, "sessions")
}

// sessionMappingPath returns the on-disk path for a session mapping after
// validating the session id. An invalid id yields an error and no path, so
// no caller can construct a path from an unvalidated id.
func sessionMappingPath(workspaceRoot, sessionID string) (string, error) {
	if !ValidSessionID(sessionID) {
		return "", fmt.Errorf("invalid session id %q: must be a lowercase UUID", sessionID)
	}
	return filepath.Join(sessionsDir(workspaceRoot), sessionID+".json"), nil
}

// WriteSessionMapping persists m under the workspace root at
// .niwa/sessions/<session_id>.json. The session id is validated against the
// UUID format before any path is constructed; an invalid id is rejected
// without writing. The write is atomic (write-temp-then-rename). When
// m.Created is zero it is stamped to now (UTC).
func WriteSessionMapping(workspaceRoot string, m SessionMapping) error {
	target, err := sessionMappingPath(workspaceRoot, m.SessionID)
	if err != nil {
		return err
	}
	if m.Created.IsZero() {
		m.Created = time.Now().UTC()
	}

	if err := os.MkdirAll(sessionsDir(workspaceRoot), 0o700); err != nil {
		return fmt.Errorf("creating sessions directory: %w", err)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling session mapping: %w", err)
	}

	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing session mapping: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("finalizing session mapping: %w", err)
	}
	return nil
}

// ReadSessionMapping reads the mapping for sessionID from the workspace
// root. The session id is validated before any path is constructed. Returns
// an error when the id is invalid or the mapping does not exist.
func ReadSessionMapping(workspaceRoot, sessionID string) (SessionMapping, error) {
	path, err := sessionMappingPath(workspaceRoot, sessionID)
	if err != nil {
		return SessionMapping{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return SessionMapping{}, fmt.Errorf("reading session mapping %s: %w", sessionID, err)
	}
	var m SessionMapping
	if err := json.Unmarshal(data, &m); err != nil {
		return SessionMapping{}, fmt.Errorf("parsing session mapping %s: %w", sessionID, err)
	}
	return m, nil
}

// DeleteSessionMapping removes the mapping for sessionID from the workspace
// root. The session id is validated before any path is constructed.
// Removing a mapping that does not exist is not an error, so teardown and
// the reaper can both call it without racing on who deletes first.
func DeleteSessionMapping(workspaceRoot, sessionID string) error {
	path, err := sessionMappingPath(workspaceRoot, sessionID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting session mapping %s: %w", sessionID, err)
	}
	return nil
}
