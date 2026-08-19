package cli

import (
	"fmt"
	"time"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// captureSessionID recovers a launched background worker's session id and the
// handle a developer uses to reach it, by correlating the agent's own session
// records against the instance directory the worker was launched in. Because
// that directory is freshly created and unique, cwd equality is an exact
// disambiguation key -- stronger than any id prefix.
//
// It polls the record store until a match appears or timeout elapses, comparing
// both sides of the path after resolving symlinks and cleaning, so a symlinked
// instance path still matches. On exactly one match whose id validates, it
// returns that id together with the handle the agent's declaration says a
// developer types. A record that matches the directory but carries no id yet is
// treated as not-yet-ready and polling continues.
//
// The id keys the durable mapping, which requires a canonical UUID; the handle
// is the user-facing one and is whatever the agent's records say it is -- for
// one agent the name of the directory its record sits in, for another the id
// itself. Nothing here decides which.
//
// Failure modes each return a clear error, never a hang and never an arbitrary
// pick:
//   - zero matches by timeout -> capture timed out.
//   - more than one record claiming the same directory -> ambiguity; the
//     dispatch rolls back rather than guess.
//
// records, root, now, and poll are all injected, so the whole path is testable
// offline against a fixture store of any shape.
func captureSessionID(records agentplan.SessionRecords, root, instanceDir string, timeout time.Duration, now func() time.Time, poll time.Duration) (sessionID, handle string, err error) {
	if now == nil {
		now = time.Now
	}
	if poll <= 0 {
		poll = 25 * time.Millisecond
	}

	targetDir := normalizePath(instanceDir)
	deadline := now().Add(timeout)

	for {
		rec, ambiguous, err := matchRecordByCwd(root, records, targetDir)
		if err != nil {
			return "", "", fmt.Errorf("dispatch: %w", err)
		}
		if ambiguous {
			return "", "", fmt.Errorf("dispatch: capture ambiguous: multiple sessions claim cwd %q", instanceDir)
		}
		if rec.ID != "" {
			return rec.ID, rec.Handle, nil
		}
		if !now().Before(deadline) {
			return "", "", fmt.Errorf("dispatch: capture timed out after %s waiting for a session with cwd %q", timeout, instanceDir)
		}
		time.Sleep(poll)
	}
}
