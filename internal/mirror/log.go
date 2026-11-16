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
	eventCheck   = "check"
	eventRestore = "restore"
)

// logLine is one line of .meta/mirror.log. TookMs is set on a restore line
// only.
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

// logPath is the mirror log of a workspace's mirrors.
func logPath(mdir string) string { return filepath.Join(metaDir(mdir), "mirror.log") }

// appendLog writes each line to the mirror log, one write per line so a line
// lands whole under O_APPEND. The file is created 0600. A failure is returned
// for the caller to drop: the log never changes what a check or restore did.
func appendLog(mdir string, lines ...logLine) error {
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
