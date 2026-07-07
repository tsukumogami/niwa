package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// jobState is the subset of ~/.claude/jobs/<id>/state.json niwa reads. The dir
// name is the session-id prefix; the full SessionID inside confirms the match.
//
// Two distinct consumers read this file:
//   - the SessionStart guard (instance_from_hook.go) keys on Template == "bg"
//     to confirm a dispatched background worker.
//   - the dispatch capture path (dispatch_capture.go) keys on Cwd to correlate a
//     launched worker to its instance directory and recover its session id.
//
// The reaper's liveness rule (sessionLive) keys on the job ENTRY existing, not
// on any field inside it, so no field here feeds liveness (DESIGN Decision 6).
//
// state.json is an undocumented internal Claude Code file, so absent fields
// decode to their zero value and every reader fails safe on a miss.
type jobState struct {
	SessionID string `json:"sessionId"`
	Template  string `json:"template"`
	// Cwd is the working directory the background worker launched in. The
	// dispatch identity-capture path (dispatch_capture.go) correlates a
	// launched worker to its instance by matching this against the unique
	// instance directory. Absent decodes to "".
	Cwd string `json:"cwd"`
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

// sessionLive reports whether the session identified by sessionID still exists
// as a Claude Code background session, evaluated as of now. This is the reaper's
// liveness rule (DESIGN Decision 6, R11): a session is LIVE exactly while its
// job ENTRY at <jobsDir>/<session-id>/state.json is present, and DEAD only once
// that entry is gone.
//
// Entry-present is a faithful proxy for "the session still exists in the Agent
// View" -- it covers both a running session and an idle-but-resumable one (it
// finished a task or was suspended but is still listed and re-openable). Entry-
// gone is a faithful proxy for "the developer deleted the session." Liveness
// deliberately does NOT look at the job `state`, at `firstTerminalAt`, or at an
// idle TTL: each of those is true of a live idle-but-resumable session, so
// keying on any of them would reap an instance whose session is still resumable
// (the reaped-on-completion / reaped-on-idle bug this rule fixes).
//
// A session is LIVE (returns true) when:
//   - a job-state file for the session exists and decodes, and
//   - its sessionId (when recorded) matches sessionID.
//
// It is DEAD (returns false) when the job entry is gone or the recorded
// sessionId is for a different session. An empty jobsDir (HOME unresolved)
// yields false: without a jobs dir there is no liveness evidence, so the caller
// falls back to the ephemeral-marker-only safety (it still never reaps a
// non-ephemeral instance). The `now` parameter is retained for signature
// stability with the reaper's injected clock; the entry-present rule does not
// consult it.
func sessionLive(jobsDir, sessionID string, now time.Time) bool {
	if jobsDir == "" {
		return false
	}
	js, ok := readJobState(jobsDir, sessionID)
	if !ok {
		// Job entry gone: the session was deleted, so it is dead.
		return false
	}
	// The dir is keyed by the session-id prefix, so confirm the full id inside
	// matches before trusting it -- a colliding prefix must not be mistaken for
	// this session. A recorded mismatch means this is not our job (treat as dead
	// for our session).
	if js.SessionID != "" && js.SessionID != sessionID {
		return false
	}
	// The entry exists and is ours: the session is live (running or
	// idle-but-resumable). It is reclaimed only once this entry disappears.
	return true
}

// instanceHasLiveJob reports whether any present Claude Code job is rooted
// inside instancePath -- i.e. a live session is currently working there. It is
// the reaper's mapping-INDEPENDENT liveness guard, distinct from sessionLive
// (which keys on a mapping's session_id): the backstop acts on UNMAPPED
// instances, so it has no session id to feed sessionLive, yet an unmapped
// instance can still be alive (a long-lived worker, or one whose mapping is
// absent). A dispatched worker launches with cmd.Dir == its instance
// directory, so its job-state cwd records exactly that path; this scans every
// job entry and returns true when any job's cwd equals instancePath or is
// nested under it.
//
// It exists to stop the reaper -- especially the name+TTL backstop -- from
// destroying an instance a running session lives in, which is what deleted the
// caller's own instance mid-dispatch. An empty jobsDir (HOME unresolved) or an
// unreadable jobs tree yields false: with no evidence of a live job the guard
// does not spare, so the caller falls back to its other eligibility gates
// (name, TTL, mapping) -- it never widens what is reaped, only narrows it.
func instanceHasLiveJob(jobsDir, instancePath string) bool {
	if jobsDir == "" || instancePath == "" {
		return false
	}
	instance := filepath.Clean(instancePath)

	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		js, ok := decodeJobState(filepath.Join(jobsDir, e.Name(), "state.json"))
		if !ok || js.Cwd == "" {
			continue
		}
		if pathWithin(filepath.Clean(js.Cwd), instance) {
			return true
		}
	}
	return false
}

// pathWithin reports whether path is at or below base. Both are expected to be
// cleaned absolute paths. It matches an exact equality and a true descendant
// (base + separator prefix), so "/a/binstance" is NOT treated as within "/a/b".
func pathWithin(path, base string) bool {
	if path == base {
		return true
	}
	return strings.HasPrefix(path, base+string(os.PathSeparator))
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
