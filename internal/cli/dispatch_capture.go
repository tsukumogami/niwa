package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// captureSessionID recovers a launched background worker's full session UUID
// AND its short id (the jobs-dir basename) by correlating the jobs-dir
// state.json whose cwd equals instanceDir (D3). Because instanceDir is a
// freshly-created unique path, cwd == instanceDir is an exact disambiguation
// key, stronger than the probabilistic short id.
//
// It polls <jobsDir>/*/state.json until a match appears or timeout elapses,
// decoding each candidate and normalizing both its cwd and instanceDir via
// filepath.EvalSymlinks + filepath.Clean before comparing, so a symlinked
// instance path still matches. On exactly one match whose sessionId is a valid
// UUID (workspace.ValidSessionID), it returns that full UUID together with the
// matched directory basename (the short id `claude attach/logs/stop` are keyed
// on). A state.json that matches the cwd but carries an invalid/empty sessionId
// is treated as not-yet-ready and polling continues until timeout.
//
// The full UUID keys the durable mapping (workspace.ValidSessionID requires
// it); the short id is the user-facing handle. The short id is the ACTUAL
// jobs-dir basename capture matched, never a slice of the UUID, so it stays
// correct even if claude ever stops deriving the short id from the UUID prefix.
//
// Failure modes (each returns a clear error, never a hang or an arbitrary
// pick):
//   - zero matches by timeout -> capture timed out (R20, R22).
//   - more than one state.json claiming the same cwd -> ambiguity error (R21);
//     dispatch must roll back rather than guess.
//
// jobsDir, now, poll, and timeout are injectable so the whole path is offline-
// testable (D9). now defaults to time.Now and poll to a small interval when
// nil/zero.
func captureSessionID(jobsDir, instanceDir string, timeout time.Duration, now func() time.Time, poll time.Duration) (sessionID, shortID string, err error) {
	if now == nil {
		now = time.Now
	}
	if poll <= 0 {
		poll = 25 * time.Millisecond
	}

	targetDir := normalizePath(instanceDir)
	deadline := now().Add(timeout)

	for {
		id, short, ambiguous, err := matchSessionByCwd(jobsDir, targetDir)
		if err != nil {
			return "", "", err
		}
		if ambiguous {
			return "", "", fmt.Errorf("dispatch: capture ambiguous: multiple jobs claim cwd %q", instanceDir)
		}
		if id != "" {
			return id, short, nil
		}
		if !now().Before(deadline) {
			return "", "", fmt.Errorf("dispatch: capture timed out after %s waiting for a job with cwd %q", timeout, instanceDir)
		}
		time.Sleep(poll)
	}
}

// matchSessionByCwd does a single enumeration pass over <jobsDir>/*/state.json,
// returning the validated sessionId AND the directory basename (short id) of
// the unique job whose normalized cwd equals targetDir. It reports
// ambiguous=true when more than one state.json claims targetDir. A match with
// an invalid/empty sessionId is ignored (not counted as a match), so the caller
// keeps polling until a valid id appears. The returned short is the actual dir
// name (the `<short>` in `<jobsDir>/<short>/state.json`), the handle
// `claude attach/logs/stop` use.
func matchSessionByCwd(jobsDir, targetDir string) (id, short string, ambiguous bool, err error) {
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Jobs dir not yet present: no match this pass, keep polling.
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("dispatch: reading jobs dir: %w", err)
	}

	var found, foundShort string
	matches := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		js, ok := decodeJobState(filepath.Join(jobsDir, e.Name(), "state.json"))
		if !ok || js.Cwd == "" {
			continue
		}
		if normalizePath(js.Cwd) != targetDir {
			continue
		}
		// The cwd matches our unique instance dir. Two distinct jobs claiming
		// the same unique dir is an ambiguity we must surface, not resolve.
		matches++
		if matches > 1 {
			return "", "", true, nil
		}
		// Only treat it as a usable match when the id validates; otherwise it
		// is not yet ready (the worker may not have recorded a UUID yet). The
		// directory basename is the short id we return alongside the UUID.
		if workspace.ValidSessionID(js.SessionID) {
			found = js.SessionID
			foundShort = e.Name()
		}
	}

	if matches > 1 {
		return "", "", true, nil
	}
	return found, foundShort, false, nil
}

// normalizePath resolves symlinks then cleans path so two spellings of the
// same directory compare equal. EvalSymlinks fails on a path that does not
// exist (e.g. a stale job's cwd); in that case fall back to Clean alone so a
// non-resolvable candidate is simply not a match rather than an error.
func normalizePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}
