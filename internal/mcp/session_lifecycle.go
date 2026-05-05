package mcp

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// sessionIDRe matches the 8-character lowercase hex session IDs used by the
// session lifecycle registry. Validated on every read to guard against path
// traversal in caller-supplied session IDs.
var sessionIDRe = regexp.MustCompile(`^[0-9a-f]{8}$`)

// sessionFileRe matches the per-session lifecycle state filenames produced by
// newSessionLifecycleID. Used by ListSessionLifecycleStates to skip
// sessions.json and any .tmp files.
var sessionFileRe = regexp.MustCompile(`^[0-9a-f]{8}\.json$`)

// SessionLifecycleState is the on-disk schema for a per-session lifecycle
// state file at <instance>/.niwa/sessions/<session-id>.json.
//
// This type is distinct from SessionEntry (the coordinator process registry).
// The two types share no fields and are written by separate code paths.
// Schema version v=1.
type SessionLifecycleState struct {
	V                    int    `json:"v"`
	SessionID            string `json:"session_id"`
	ParentSessionID      string `json:"parent_session_id,omitempty"`
	Repo                 string `json:"repo"`
	Purpose              string `json:"purpose"`
	Status               string `json:"status"`
	CreationTime         string `json:"creation_time"`
	WorktreePath         string `json:"worktree_path"`
	ClaudeConversationID string `json:"claude_conversation_id,omitempty"`
	CreatorPID           int    `json:"creator_pid"`
	CreatorStartTime     int64  `json:"creator_start_time"`
}

// WriteSessionLifecycleState atomically persists state to
// <sessionsDir>/<state.SessionID>.json. Safe to call concurrently provided no
// two callers write the same session ID simultaneously.
func WriteSessionLifecycleState(sessionsDir string, state SessionLifecycleState) error {
	if !sessionIDRe.MatchString(state.SessionID) {
		return fmt.Errorf("invalid session ID %q: must be 8 lowercase hex characters", state.SessionID)
	}
	target := filepath.Join(sessionsDir, state.SessionID+".json")
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// ReadSessionLifecycleState reads the session lifecycle state for sessionID
// from <mainInstanceRoot>/.niwa/sessions/<sessionID>.json.
// The sessionID is validated against ^[0-9a-f]{8}$ before any path is
// constructed, preventing path traversal from caller-supplied values.
func ReadSessionLifecycleState(mainInstanceRoot, sessionID string) (SessionLifecycleState, error) {
	if !sessionIDRe.MatchString(sessionID) {
		return SessionLifecycleState{}, fmt.Errorf("invalid session ID %q: must be 8 lowercase hex characters", sessionID)
	}
	sessionsDir := filepath.Join(mainInstanceRoot, ".niwa", "sessions")
	path := filepath.Join(sessionsDir, sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return SessionLifecycleState{}, fmt.Errorf("reading session state %s: %w", sessionID, err)
	}
	var state SessionLifecycleState
	if err := json.Unmarshal(data, &state); err != nil {
		return SessionLifecycleState{}, fmt.Errorf("parsing session state %s: %w", sessionID, err)
	}
	return state, nil
}

// ListSessionLifecycleStates reads all per-session lifecycle state files from
// sessionsDir, skipping sessions.json and any non-matching files. Corrupt
// individual files are logged and skipped without aborting the scan.
// Callers are responsible for computing liveness via IsPIDAlive.
func ListSessionLifecycleStates(sessionsDir string) ([]SessionLifecycleState, error) {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading sessions directory: %w", err)
	}
	var states []SessionLifecycleState
	for _, entry := range entries {
		if entry.IsDir() || !sessionFileRe.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(sessionsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("session_lifecycle: skipping %s: read error: %v", entry.Name(), err)
			continue
		}
		var state SessionLifecycleState
		if err := json.Unmarshal(data, &state); err != nil {
			log.Printf("session_lifecycle: skipping %s: parse error: %v", entry.Name(), err)
			continue
		}
		states = append(states, state)
	}
	return states, nil
}

// newSessionLifecycleID generates a random 8-character lowercase hex session
// ID and verifies no existing state file has the same ID. Retries up to 5
// times on collision. Returns an error if all attempts fail.
func newSessionLifecycleID(sessionsDir string) (string, error) {
	for range 5 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", fmt.Errorf("generating session ID: %w", err)
		}
		id := fmt.Sprintf("%08x", b)
		path := filepath.Join(sessionsDir, id+".json")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return id, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique session ID after 5 attempts")
}

// SessionsDir returns the sessions directory path for a given instance root.
func SessionsDir(instanceRoot string) string {
	return filepath.Join(instanceRoot, ".niwa", "sessions")
}

// SessionLifecycleStatus constants for SessionLifecycleState.Status.
const (
	SessionStatusActive    = "active"
	SessionStatusEnded     = "ended"
	SessionStatusAbandoned = "abandoned"
)

// NewSessionLifecycleState creates a SessionLifecycleState with V=1 and
// CreationTime set to now (UTC, RFC3339).
func NewSessionLifecycleState(sessionID, repo, purpose, parentSessionID, worktreePath string) SessionLifecycleState {
	pid := os.Getpid()
	startTime, _ := PIDStartTime(pid)
	return SessionLifecycleState{
		V:               1,
		SessionID:       sessionID,
		ParentSessionID: parentSessionID,
		Repo:            repo,
		Purpose:         purpose,
		Status:          SessionStatusActive,
		CreationTime:    time.Now().UTC().Format(time.RFC3339),
		WorktreePath:    worktreePath,
		CreatorPID:      pid,
		CreatorStartTime: startTime,
	}
}
