package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// launchRecordGrace is how young a launch record is kept regardless of its
// transcript. A dispatch writes its record once it has captured the session
// id, which can be before Claude Code has written the first line of the
// transcript, and a reap running alongside it must not read that gap as a
// transcript that is gone.
const launchRecordGrace = time.Hour

// sweepLaunchRecords removes the launch records whose transcripts Claude Code
// no longer holds, and keeps every other one. It returns how many it removed.
//
// A record is the only way back into a worker's launch posture once its
// instance is gone, so the rule only ever removes on positive evidence: the
// transcript's path answers "does not exist" under a projects directory that
// does. A transcript that exists, a projects directory that is missing or
// can't be read, a home that can't be found, an agent whose transcripts niwa
// doesn't know how to find, and a record younger than launchRecordGrace all
// keep the record. A file that isn't a record niwa can read is kept with one
// warning line naming it, and the sweep goes on with the others.
func sweepLaunchRecords(workspaceRoot, home string, w io.Writer, now time.Time) int {
	entries, err := workspace.ListLaunchRecords(workspaceRoot)
	if err != nil {
		fmt.Fprintf(w, "niwa: warning: could not read the launch store %s: %v\n", workspace.LaunchDir(workspaceRoot), err)
		return 0
	}
	if len(entries) == 0 {
		return 0
	}
	projectsReadable := false
	if home != "" {
		if info, err := os.Stat(filepath.Join(home, ".claude", "projects")); err == nil && info.IsDir() {
			projectsReadable = true
		}
	}
	removed := 0
	for _, e := range entries {
		path := filepath.Join(workspace.LaunchDir(workspaceRoot), e.Name)
		if e.Err != nil {
			fmt.Fprintf(w, "niwa: warning: kept launch record %s: %v\n", path, e.Err)
			continue
		}
		rec := e.Record
		if !workspace.ValidSessionID(rec.SessionID) || !validInstanceName(rec.InstanceName) {
			fmt.Fprintf(w, "niwa: warning: kept launch record %s: it does not name a valid session and instance\n", path)
			continue
		}
		if !projectsReadable {
			continue
		}
		if ag, err := agent.ParseAgent(rec.Agent); err != nil || ag != agent.AgentClaude {
			continue
		}
		if now.Sub(rec.WrittenAt) < launchRecordGrace {
			continue
		}
		transcript := sessionattach.TranscriptPath(home, filepath.Join(workspaceRoot, rec.InstanceName), rec.SessionID)
		if _, err := os.Lstat(transcript); err == nil || !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err := workspace.RemoveLaunchRecord(workspaceRoot, rec.DispatchID); err != nil {
			fmt.Fprintf(w, "niwa: warning: could not remove launch record %s: %v\n", path, err)
			continue
		}
		removed++
	}
	return removed
}

// validInstanceName reports whether name is one path component naming an
// instance under the workspace root, so a record can't point the transcript
// lookup anywhere else.
func validInstanceName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}
