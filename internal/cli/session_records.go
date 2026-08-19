package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// This file reads the record an agent writes when a session starts: the one
// that says which session it is and which directory it was started in. Two
// things in niwa need it. The dispatch capture path correlates a just-launched
// worker to the unique instance directory it was given, and the reaper asks
// whether a mapped session still exists before reclaiming the instance it
// lives in.
//
// It is one reader, not one per agent. What differs between agents is where the
// records sit, how deeply they are nested, whether a record is a whole file or
// the first line of one, and which field names carry the two values -- and all
// of that is declared in the agent's agentplan.SessionRecords rather than
// written here. What does not differ is the part that is easy to get subtly
// wrong: normalizing both sides of a path comparison, refusing to guess when
// two records claim the same directory, and treating a record whose id has not
// appeared yet as absent rather than as a match.
//
// Nothing in this file names an agent, and it must stay that way; the scan in
// dispatch_layout_test.go is what enforces it.

// sessionRecord is one decoded record from an agent's store.
type sessionRecord struct {
	// Handle is the user-facing string for this session: what a developer
	// types after the agent's own management verbs, and what niwa's resume
	// passes. It is the session id for some agents and the name of the
	// directory the record sits in for others; which one is the agent's
	// declaration, not this reader's choice.
	Handle string

	// ID is the session id as recorded. It may be empty on a record the agent
	// has created but not yet filled in, which is a state the caller must
	// treat as "not ready" rather than as "no session".
	ID string

	// Cwd is the working directory the session was started in, exactly as
	// recorded. Callers normalize before comparing.
	Cwd string
}

// recordStoreRoot resolves the directory an agent keeps its session records
// under. An agent that lets the developer relocate its own state directory is
// honored first; otherwise the records sit at the declared path under home.
//
// home and lookupEnv are parameters rather than direct reads so the whole path
// is offline-testable, the same reason the capture's clock and poll interval
// are injected. An empty home with no environment override yields an empty
// root, which every caller treats as "no records" rather than as an error --
// without a home directory there is no evidence either way, and a reader with
// no evidence must not answer.
func recordStoreRoot(r agentplan.SessionRecords, home string, lookupEnv func(string) string) string {
	if len(r.HomePath) == 0 {
		return ""
	}
	if r.HomeEnv != "" && lookupEnv != nil {
		// The override replaces the agent's own directory under home -- the
		// first component -- and everything below it is unchanged.
		if override := strings.TrimSpace(lookupEnv(r.HomeEnv)); override != "" {
			return filepath.Join(append([]string{override}, r.HomePath[1:]...)...)
		}
	}
	if home == "" {
		return ""
	}
	return filepath.Join(append([]string{home}, r.HomePath...)...)
}

// scanSessionRecords walks root to the depth the records declare and decodes
// every record it finds there.
//
// A root that does not exist yields no records and no error: the agent may not
// have written anything yet, which is the ordinary state during the poll right
// after a launch. Any other read failure is an error, because a store that
// exists and cannot be read is not the same as a store with nothing in it. An
// individual record that will not open or will not decode is skipped, so one
// half-written file cannot hide every other session on the host.
func scanSessionRecords(root string, r agentplan.SessionRecords) ([]sessionRecord, error) {
	if root == "" {
		return nil, nil
	}
	dirs, err := descend(root, r.Depth)
	if err != nil {
		return nil, err
	}

	var out []sessionRecord
	for _, dir := range dirs {
		if r.FileName != "" {
			rec, ok := decodeSessionRecord(filepath.Join(dir, r.FileName), r)
			if !ok {
				continue
			}
			out = append(out, withHandle(rec, dir, r))
			continue
		}

		matches, err := filepath.Glob(filepath.Join(dir, r.FileGlob))
		if err != nil {
			// A malformed glob is a defect in the declaration, not a
			// condition of the store, so it is worth surfacing rather than
			// skipping.
			return nil, fmt.Errorf("matching session records under %s: %w", dir, err)
		}
		for _, path := range matches {
			rec, ok := decodeSessionRecord(path, r)
			if !ok {
				continue
			}
			out = append(out, withHandle(rec, dir, r))
		}
	}
	return out, nil
}

// withHandle fills in a record's user-facing handle from what the agent
// declares its handle to be. A record-directory handle is the actual name on
// disk and never a slice of the id, which keeps it right if an agent ever stops
// deriving one from the other.
func withHandle(rec sessionRecord, dir string, r agentplan.SessionRecords) sessionRecord {
	switch r.Handle {
	case agentplan.HandleRecordDir:
		rec.Handle = filepath.Base(dir)
	case agentplan.HandleSessionID:
		rec.Handle = rec.ID
	}
	return rec
}

// descend returns every directory exactly depth levels below root, or root
// itself at depth zero. A missing directory anywhere in the walk contributes
// nothing rather than failing: the record stores are grown by the agent as it
// goes, so a level that does not exist yet is an ordinary state.
func descend(root string, depth int) ([]string, error) {
	if depth <= 0 {
		if _, err := os.Stat(root); err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("reading session record store %s: %w", root, err)
		}
		return []string{root}, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading session record store %s: %w", root, err)
	}

	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		below, err := descend(filepath.Join(root, e.Name()), depth-1)
		if err != nil {
			return nil, err
		}
		out = append(out, below...)
	}
	return out, nil
}

// decodeSessionRecord reads one record file and pulls the two declared fields
// out of it. ok is false on any read or parse failure, and on a record that
// carries no working directory at all -- a record that cannot say where its
// session started is one no caller here can use.
func decodeSessionRecord(path string, r agentplan.SessionRecords) (sessionRecord, bool) {
	data, ok := readRecordBytes(path, r.FirstLineOnly)
	if !ok {
		return sessionRecord{}, false
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return sessionRecord{}, false
	}
	cwd := stringAt(doc, r.CwdPath)
	if cwd == "" {
		return sessionRecord{}, false
	}
	return sessionRecord{ID: stringAt(doc, r.IDPath), Cwd: cwd}, true
}

// readRecordBytes returns the JSON document at path: the whole file, or its
// first line for a store whose records are one session's transcript with its
// metadata on line one. Reading the whole of such a file would read an entire
// conversation to learn two fields.
func readRecordBytes(path string, firstLineOnly bool) ([]byte, bool) {
	if !firstLineOnly {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, false
		}
		return data, true
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// A metadata line can carry a whole system prompt, which is far past the
	// default 64 KiB scanner limit is comfortable with but nowhere near this
	// bound. A line longer than this is not a record niwa wrote against.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	if !scanner.Scan() {
		return nil, false
	}
	return scanner.Bytes(), true
}

// stringAt walks a decoded document down a field path and returns the string
// at the end of it, or "" for a path that is absent or does not end in a
// string. An absent field is not an error here: records are undocumented files
// written by somebody else's tool, so every reader fails safe on a miss.
func stringAt(doc map[string]any, path []string) string {
	var cur any = doc
	for _, key := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = obj[key]
		if !ok {
			return ""
		}
	}
	s, _ := cur.(string)
	return s
}

// matchRecordByCwd does a single pass over a store and returns the record whose
// recorded working directory is targetDir, already normalized by the caller.
//
// It reports ambiguous when more than one record claims targetDir. That is not
// a case to resolve: the caller hands over a freshly created, unique directory,
// so two sessions claiming it means something happened that niwa's model does
// not cover, and picking one would be a guess presented as a fact.
//
// A record that matches the directory but whose id has not been written yet is
// counted toward ambiguity and not returned as a match, so a caller polling for
// a launch keeps polling rather than concluding the session does not exist.
func matchRecordByCwd(root string, r agentplan.SessionRecords, targetDir string) (rec sessionRecord, ambiguous bool, err error) {
	records, err := scanSessionRecords(root, r)
	if err != nil {
		return sessionRecord{}, false, err
	}

	var found sessionRecord
	matches := 0
	for _, candidate := range records {
		if normalizePath(candidate.Cwd) != targetDir {
			continue
		}
		matches++
		if matches > 1 {
			return sessionRecord{}, true, nil
		}
		if workspace.ValidSessionID(candidate.ID) {
			found = candidate
		}
	}
	return found, false, nil
}

// instanceHasRecordedSession reports whether any agent niwa can launch has a
// session record whose working directory is at or under instancePath.
//
// It is the agent-neutral half of the reaper's mapping-independent guard. The
// other half reads one agent's harness job state directly, which was the whole
// story while niwa launched one agent; it is not any more. A worker started
// detached survives a niwa that dies before writing the mapping, and the
// instance it is working in is then unmapped -- which is precisely the case the
// name-and-age backstop acts on. A guard that reads one agent's store would
// find nothing, and the backstop would destroy the directory a live worker is
// working in.
//
// It answers "is there a session rooted here", not "is that session still
// running", and the difference is the whole cost of this guard. For an agent
// that removes a session's record when the session is deleted, the answer
// tracks the session and the sparing lasts as long as the session does. For an
// agent that never removes one, the answer is yes forever: the instance is
// spared on every sweep from the first worker that ran in it until somebody
// removes it by hand. That is the mapped sweep's declared cost reaching the
// backstop, and it is the reason found comes back with a reason to print rather
// than as a bare bool -- a sweep that silently stops reclaiming a whole class of
// instance is indistinguishable from one that is working.
//
// What closes it properly is not more of this. Record presence cannot be made
// into a liveness signal for an agent that keeps its records; the question the
// backstop is actually asking is not "did this agent's session end" but "is
// anything running in this directory right now", and that has an agent-neutral
// answer one level down: whether any live process has a working directory
// inside the instance. That needs no cooperation from either agent's
// bookkeeping and would also catch a worker whose harness state went missing,
// which nothing here can. It is not built -- internal/worktree's
// procinfo_linux.go has the beginnings of it in pidStartTime and readPPID, and
// it is Linux-only as it stands.
//
// reason is empty when the sparing ends on its own, because there is nothing
// worth telling a developer about an instance that will be reclaimed without
// them; it carries the permanent case's explanation otherwise.
func instanceHasRecordedSession(instancePath string) (reason string, found bool) {
	if instancePath == "" {
		return "", false
	}
	instance := normalizePath(instancePath)
	home := userHomeDir()

	for _, ag := range agentplan.LaunchableAgents() {
		spec, ok := agentplan.For(ag).LaunchSpec()
		if !ok {
			continue
		}
		records, err := scanSessionRecords(recordStoreRoot(spec.Records, home, os.Getenv), spec.Records)
		if err != nil {
			// An unreadable store is no evidence either way, and this guard
			// only ever spares, so no evidence means it does not spare here --
			// the caller's other gates still apply.
			continue
		}
		for _, rec := range records {
			if !pathWithin(normalizePath(rec.Cwd), instance) {
				continue
			}
			if spec.Records.Liveness == agentplan.LivenessRecordPresence {
				return "", true
			}
			return fmt.Sprintf("a %s session was started in it, and %s never removes a session's record, so niwa cannot tell whether that worker is still running", ag, ag), true
		}
	}
	return "", false
}

// normalizePath resolves symlinks then cleans path so two spellings of the same
// directory compare equal. EvalSymlinks fails on a path that does not exist --
// a stale record's working directory, say -- and in that case Clean alone is
// enough for the candidate to simply not match rather than to fail the pass.
func normalizePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}
