package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// jobLivenessTTL is how long a job-state file's updatedAt may lag before the
// session is treated as dead even if its state is not (yet) terminal. A live
// background worker rewrites its job state continuously, so a stale updatedAt
// past this window is a strong signal the session ended without recording a
// terminal state (a crash, a kill, or an orphaned SessionEnd). The reaper still
// only reclaims instances carrying the ephemeral marker, so a conservative TTL
// only delays reclamation; it never risks a developer instance.
const jobLivenessTTL = 30 * time.Minute

// jobState is the subset of ~/.claude/jobs/<id>/state.json niwa reads. The dir
// name is the session-id prefix; the full SessionID inside confirms the match.
//
// Two distinct consumers read this file:
//   - the SessionStart guard (instance_from_hook.go) keys on Template == "bg"
//     to confirm a dispatched background worker.
//   - the reaper (reap.go) keys on State / UpdatedAt to decide liveness.
//
// state.json is an undocumented internal Claude Code file, so absent fields
// decode to their zero value and every reader fails safe on a miss.
type jobState struct {
	SessionID string `json:"sessionId"`
	Template  string `json:"template"`
	State     string `json:"state"`
	// Cwd is the working directory the background worker launched in. The
	// dispatch identity-capture path (dispatch_capture.go) correlates a
	// launched worker to its instance by matching this against the unique
	// instance directory. Absent decodes to "".
	Cwd       string    `json:"cwd"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// terminalJobStates is the set of job `state` values that mean the session has
// ended. The exact vocabulary of state.json is undocumented, so this set is
// matched case-insensitively and kept deliberately broad. A state value not in
// this set is treated as non-terminal (still running), so the TTL is the
// backstop for any unrecognized terminal label.
var terminalJobStates = map[string]bool{
	"completed": true,
	"complete":  true,
	"done":      true,
	"finished":  true,
	"failed":    true,
	"error":     true,
	"errored":   true,
	"canceled":  true,
	"cancelled": true,
	"timeout":   true,
	"timedout":  true,
	"killed":    true,
}

// defaultJobsDir returns the Claude Code jobs directory (~/.claude/jobs). A
// failure to resolve the home directory yields an empty string, which callers
// treat as "no job state" (fail safe), so a missing HOME never aborts.
func defaultJobsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "jobs")
}

// sessionLive reports whether the session identified by sessionID has a live
// Claude Code job under jobsDir, evaluated as of now. This is the reaper's
// liveness rule (DESIGN Decision 6, R11), keyed on the SAME job-state source as
// the SessionStart guard.
//
// A session is LIVE (returns true) only when all hold:
//   - a job-state file for the session exists and decodes, and
//   - its sessionId (when recorded) matches sessionID, and
//   - its State is not terminal, and
//   - its UpdatedAt is within jobLivenessTTL of now (or unset).
//
// It is DEAD (returns false) when the job entry is gone, the state is terminal,
// or updatedAt is older than the TTL. Job-state -- not transcript mtime -- is
// the primary signal; this never reaps a live-but-idle worker that is still
// rewriting its job state. An empty jobsDir (HOME unresolved) yields false:
// without a jobs dir there is no liveness evidence, so the caller falls back to
// the ephemeral-marker-only safety (it still never reaps a non-ephemeral
// instance).
func sessionLive(jobsDir, sessionID string, now time.Time) bool {
	if jobsDir == "" {
		return false
	}
	js, ok := readJobState(jobsDir, sessionID)
	if !ok {
		// Job entry gone: the session is dead.
		return false
	}
	// The dir is keyed by the session-id prefix, so confirm the full id inside
	// matches before trusting the rest -- a colliding prefix must not be
	// mistaken for this session. A recorded mismatch means this is not our job
	// (treat as dead for our session).
	if js.SessionID != "" && js.SessionID != sessionID {
		return false
	}
	if terminalJobStates[strings.ToLower(strings.TrimSpace(js.State))] {
		return false
	}
	if !js.UpdatedAt.IsZero() && now.Sub(js.UpdatedAt) > jobLivenessTTL {
		return false
	}
	return true
}

// readJobState locates the job-state file for sessionID under jobsDir and
// decodes it. The job dir name is the session-id prefix, so it first tries an
// exact match on the full id, then falls back to scanning for a directory whose
// name is a prefix of sessionID (the empirically observed layout). It returns
// ok=false on any miss or decode failure.
func readJobState(jobsDir, sessionID string) (jobState, bool) {
	// Fast path: a directory named by the full session id.
	if js, ok := decodeJobState(filepath.Join(jobsDir, sessionID, "state.json")); ok {
		return js, true
	}

	// Fall back to scanning for a job dir whose name is a prefix of the
	// session id (the observed layout uses a leading slice of the UUID).
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return jobState{}, false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "" || len(name) > len(sessionID) {
			continue
		}
		if sessionID[:len(name)] != name {
			continue
		}
		if js, ok := decodeJobState(filepath.Join(jobsDir, name, "state.json")); ok {
			return js, true
		}
	}
	return jobState{}, false
}

// decodeJobState reads and decodes a single job-state file. ok=false on any
// read or parse failure.
func decodeJobState(path string) (jobState, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return jobState{}, false
	}
	var js jobState
	if err := json.Unmarshal(data, &js); err != nil {
		return jobState{}, false
	}
	return js, true
}
