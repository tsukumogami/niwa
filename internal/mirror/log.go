package mirror

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The events a mirror log line records.
const (
	eventCheck         = "check"
	eventRestore       = "restore"
	eventRefresh       = "refresh"
	eventUnrecoverable = "unrecoverable"
)

// logLine is one line of .meta/mirror.log. TookMs is set on a restore or
// refresh line only.
type logLine struct {
	Time    string  `json:"time"`
	Command string  `json:"command"`
	Mirror  string  `json:"mirror"`
	Event   string  `json:"event"`
	Fault   Fault   `json:"fault"`
	Result  Outcome `json:"result"`
	CheckMs int64   `json:"check_ms"`
	TookMs  *int64  `json:"took_ms,omitempty"`
}

// unrecoverableLine is the line logged for each ref a restore left lost: the
// restore line's time, command, mirror, fault and result, and the LostRef's
// fields under keys no other event's line carries. Its names were cleaned
// when the LostRef was built, so the line stays one line.
type unrecoverableLine struct {
	logLine
	Instance       string     `json:"instance"`
	Ref            string     `json:"ref"`
	MissingCommit  string     `json:"missing_commit"`
	RemoteTracking bool       `json:"remote_tracking"`
	OwnCommits     []string   `json:"own_commits"`
	LostFiles      []LostFile `json:"lost_files"`
}

// unrecoverableLines builds one unrecoverable line per lost ref from the
// restore line it follows.
func unrecoverableLines(restoreLine logLine, lost []LostRef) []any {
	var lines []any
	for _, l := range lost {
		base := restoreLine
		base.Event = eventUnrecoverable
		base.TookMs = nil
		line := unrecoverableLine{
			logLine:        base,
			Instance:       l.Instance,
			Ref:            l.Ref,
			MissingCommit:  l.MissingCommit,
			RemoteTracking: l.RemoteTracking,
			OwnCommits:     l.OwnCommits,
			LostFiles:      l.LostFiles,
		}
		if line.OwnCommits == nil {
			line.OwnCommits = []string{}
		}
		if line.LostFiles == nil {
			line.LostFiles = []LostFile{}
		}
		lines = append(lines, line)
	}
	return lines
}

// logPath is the mirror log of a workspace's mirrors.
func logPath(mdir string) string { return filepath.Join(metaDir(mdir), "mirror.log") }

// appendLog writes each line, a logLine or an unrecoverableLine, to the
// mirror log, one write per line so a line lands whole under O_APPEND. The
// file is created 0600. A failure is returned for the caller to drop: the log
// never changes what a check or restore did.
func appendLog(mdir string, lines ...any) error {
	if err := ensurePrivateDir(metaDir(mdir)); err != nil {
		return err
	}
	f, err := os.OpenFile(logPath(mdir), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("opening the mirror log: %w", err)
	}
	defer f.Close()
	for _, line := range lines {
		data, err := json.Marshal(line)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			return fmt.Errorf("writing the mirror log: %w", err)
		}
	}
	return nil
}

// logTime formats an event's time for the log.
func logTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }
