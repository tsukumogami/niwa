package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// jobState is the subset of ~/.claude/jobs/<id>/state.json niwa reads. The dir
// name is the session-id prefix; the full SessionID inside confirms the match.
//
// Everything here is Claude Code's own harness surface rather than a
// general-purpose reader, and its consumers are the paths that are Claude
// Code's by declaration:
//   - the SessionStart guard (instance_from_hook.go) keys on Template == "bg"
//     to confirm a dispatched background worker. Ephemeral-session provisioning
//     rides that hook, and the capability is declared unavailable for every
//     agent that has no such hook.
//   - the reaper's entry-present liveness rule and `niwa watch`'s review
//     continuation, both of which read a Claude Code job entry.
//   - the worktree hooks and sweep: the WorktreeCreate hook records whether
//     the session that asked for a worktree had a job entry, and the reap's
//     worktree sweep keeps that worktree while the entry is listed.
//
// The dispatch capture path no longer reads this file. Correlating a launched
// worker to its instance is one behavior for whichever agent was launched, so
// it moved to session_records.go, which is driven by the agent's own
// declaration of where its records sit and what shape they are.
//
// The reaper's liveness rule (sessionLive) keys on the job ENTRY existing, not
// on any field inside it, so no field here feeds liveness (DESIGN Decision 6).
// Which agents that rule applies to is decided before it is called, from the
// agent recorded on the session mapping.
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
	// The remaining fields feed ONLY the watch continuation classifier
	// (recordJobActivity -> watch.ClassifySessionActivity). They are the job
	// lifecycle/tempo fields the reaper and capture paths deliberately ignore.
	// Absent decodes to the zero value, so a state.json without them classifies
	// dead/unknown (fail-closed), never a wrong Continue.
	//
	// State is the job lifecycle ("done", "working", "blocked"); Tempo is the
	// session tempo ("idle", "active", "blocked").
	State string `json:"state"`
	Tempo string `json:"tempo"`
	// InFlight.Tasks counts in-flight sub-tasks. Advisory only: a nonzero value
	// forces busy unless every task is an armed session_cron self-wake (see
	// jobActivityFromState), but a zero value is not sufficient for idle on its
	// own (stale teammate counts persist after a turn ends).
	//
	// InFlight.Kinds lists the kinds of the in-flight tasks ("session_cron",
	// "local_bash", ...). It is lenient: any shape other than an array of
	// strings decodes as empty instead of failing the whole decode, so a change
	// in this field can never break the reaper's liveness reads.
	InFlight struct {
		Tasks int          `json:"tasks"`
		Kinds jobTaskKinds `json:"kinds"`
	} `json:"inFlight"`
	// Block is present (non-nil) when the session is blocked on a pending
	// question; Needs is non-empty when it needs a human answer. Either marks
	// the session as awaiting a human (the "attached" proxy).
	Block json.RawMessage `json:"block"`
	Needs string          `json:"needs"`
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

// jobEntryGone reports whether jobsDir positively lists no job entry for
// sessionID, for the worktree sweep's job-keyed rule (reap_worktrees.go). It
// looks where readJobState looks, at each directory whose name is a prefix of
// the id, and an entry recorded for another session is not this one's. Unlike
// sessionLive it tells "gone" from "can't tell": a jobs directory that is
// unset, missing or can't be read, and an entry whose state can't be read,
// are never gone, since the worktree would be removed on exactly the evidence
// that couldn't be checked.
func jobEntryGone(jobsDir, sessionID string) bool {
	if jobsDir == "" {
		return false
	}
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || name == "" || len(name) > len(sessionID) || sessionID[:len(name)] != name {
			continue
		}
		js, ok := decodeJobState(filepath.Join(jobsDir, name, "state.json"))
		if !ok || js.SessionID == "" || js.SessionID == sessionID {
			return false
		}
	}
	return true
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

// jobTaskKinds is inFlight.kinds. Its decoder never fails: a value that is not
// an array of strings leaves it empty, and the classifier then counts every
// in-flight task as busy.
type jobTaskKinds []string

func (k *jobTaskKinds) UnmarshalJSON(data []byte) error {
	var kinds []string
	if err := json.Unmarshal(data, &kinds); err != nil {
		*k = nil
		return nil
	}
	*k = kinds
	return nil
}

// jobStateOwnerUID is the uid a job-state file must belong to for the checked
// read. A package variable so tests can check the ownership refusal.
var jobStateOwnerUID = os.Getuid

// maxJobStateBytes bounds the checked read. Real state files are a few
// kilobytes.
const maxJobStateBytes = 1 << 20

// readJobStateChecked is the job-state read for commands about to act on a
// session (retask, and watch's continuation). The lenient readers above stay
// as they are, because the reaper's liveness rule depends on them; this one
// treats state.json as data a compromised worker could have shaped.
//
// It locates the entry the way readJobState does (the directory named by the
// full id, then a directory whose name is a prefix of the id). The job
// directory must be a real directory, and state.json is opened without
// following a symlink and without blocking on a FIFO, then must be a regular
// file owned by the current user. The decoded sessionId must be non-empty and
// equal sessionID.
//
// A missing entry returns an error for which errors.Is(err, fs.ErrNotExist) is
// true; every other failure does not, so a caller can tell "the job entry is
// gone" from "the job state can't be trusted".
func readJobStateChecked(jobsDir, sessionID string) (jobState, error) {
	js, _, err := readJobStateFileChecked(jobsDir, sessionID)
	return js, err
}

// readJobStateFileChecked is readJobStateChecked returning the raw file as
// well, for readers of fields jobState doesn't carry.
func readJobStateFileChecked(jobsDir, sessionID string) (jobState, []byte, error) {
	if jobsDir == "" {
		return jobState{}, nil, errors.New("no Claude Code jobs directory")
	}
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." {
		return jobState{}, nil, errors.New("not a usable session id")
	}

	candidates := []string{sessionID}
	if entries, err := os.ReadDir(jobsDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			if name == "" || name == sessionID || len(name) > len(sessionID) || sessionID[:len(name)] != name {
				continue
			}
			candidates = append(candidates, name)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return jobState{}, nil, fmt.Errorf("reading the Claude Code jobs directory: %w", err)
	}

	// The first entry whose state names this session wins. An entry that
	// exists but can't be trusted is reported only when no other candidate
	// matches, so a stray prefix directory can't hide the real entry.
	var firstErr error
	for _, name := range candidates {
		js, data, err := readJobStateEntryChecked(filepath.Join(jobsDir, name), sessionID)
		if err == nil {
			return js, data, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return jobState{}, nil, firstErr
	}
	return jobState{}, nil, fmt.Errorf("no Claude Code job entry for session %s: %w", sessionID, fs.ErrNotExist)
}

// readJobStateEntryChecked reads one job directory's state.json under the
// checked rules.
func readJobStateEntryChecked(dir, sessionID string) (jobState, []byte, error) {
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return jobState{}, nil, err
	}
	if !dirInfo.IsDir() {
		if dirInfo.Mode()&fs.ModeSymlink != 0 {
			return jobState{}, nil, fmt.Errorf("job directory %s is a symlink", dir)
		}
		return jobState{}, nil, fmt.Errorf("job directory %s is not a directory", dir)
	}

	path := filepath.Join(dir, "state.json")
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return jobState{}, nil, err
		}
		return jobState{}, nil, fmt.Errorf("opening %s (only a regular file is read, never through a symlink): %w", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return jobState{}, nil, fmt.Errorf("inspecting %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return jobState{}, nil, fmt.Errorf("%s is not a regular file (mode %s)", path, info.Mode().Type())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != jobStateOwnerUID() {
		return jobState{}, nil, fmt.Errorf("%s is not owned by the current user", path)
	}

	data, err := io.ReadAll(io.LimitReader(f, maxJobStateBytes+1))
	if err != nil {
		return jobState{}, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(data) > maxJobStateBytes {
		return jobState{}, nil, fmt.Errorf("%s is larger than %d bytes", path, maxJobStateBytes)
	}
	var js jobState
	if err := json.Unmarshal(data, &js); err != nil {
		// The decoder's message can quote the file's content, so it is not
		// passed on.
		return jobState{}, nil, fmt.Errorf("%s is not a valid job state", path)
	}
	if js.SessionID == "" {
		return jobState{}, nil, fmt.Errorf("%s records no session id", path)
	}
	if js.SessionID != sessionID {
		return jobState{}, nil, fmt.Errorf("%s belongs to another session", path)
	}
	return js, data, nil
}

// readSavedOptions returns the option argv Claude Code saved for the session
// (state.json's respawnFlags), which a flagless `claude --bg --resume` wake
// re-applies. It reads through readJobStateChecked and accepts only a flat
// array of strings; an absent or null field, or any other shape, is an error.
// The key and shape are as Claude Code 2.1.314 writes them for a dispatched
// worker (e.g. --permission-mode, --name, --settings, --model with their
// values); a change in either fails here rather than being guessed at.
//
// The saved options include the --settings document, which carries
// environment values, so no error from here includes an option's value.
func readSavedOptions(jobsDir, sessionID string) ([]string, error) {
	_, data, err := readJobStateFileChecked(jobsDir, sessionID)
	if err != nil {
		return nil, err
	}
	var doc struct {
		RespawnFlags json.RawMessage `json:"respawnFlags"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, errors.New("the job state's saved options could not be decoded")
	}
	raw := strings.TrimSpace(string(doc.RespawnFlags))
	if raw == "" || raw == "null" {
		return nil, errors.New("the job state records no saved options")
	}
	var elems []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &elems); err != nil {
		return nil, errors.New("the job state's saved options are not an array")
	}
	opts := make([]string, 0, len(elems))
	for i, e := range elems {
		trimmed := strings.TrimSpace(string(e))
		if !strings.HasPrefix(trimmed, `"`) {
			return nil, fmt.Errorf("saved option %d is not a string", i)
		}
		var s string
		if err := json.Unmarshal([]byte(trimmed), &s); err != nil {
			return nil, fmt.Errorf("saved option %d is not a string", i)
		}
		opts = append(opts, s)
	}
	return opts, nil
}
