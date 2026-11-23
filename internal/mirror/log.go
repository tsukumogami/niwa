package mirror

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// The events a mirror log line records.
const (
	eventCheck         = "check"
	eventRestore       = "restore"
	eventRefresh       = "refresh"
	eventUnrecoverable = "unrecoverable"
	// eventRemove and eventKeep are a store line's events: a store removed,
	// or a store a removal would have taken that was kept, with the reason.
	eventRemove = "remove"
	eventKeep   = "keep"
)

// logMax is the size at which the mirror log rotates: a line is never
// started in a file this big, so .meta/ holds at most mirror.log and one
// older generation, mirror.log.1, each about this size.
const logMax = 1 << 20

// logLine is one line of .meta/mirror.log. TookMs is set on a restore or
// refresh line only. SalvagedFrom is set on a restore line only, and only
// when the new store was built with salvage: it names the old stores the
// salvage read, relative to the mirrors directory, so a reader can tell a
// restore that kept what the old store held from one that started over.
// NetworkNotes, on a restore or refresh line only, names what the event's
// network calls honoured from the user's environment that weakens or
// reroutes the connection (noteSSLNoVerify, noteProxyCommand). It carries
// names only: no environment value ever reaches the log. Probed, on a
// restore line only, lists the borrower clones the restore's probe read, in
// the order it read them, so a reader can tell which instances a restore
// looked at, kept and half-created ones included.
type logLine struct {
	Time         string   `json:"time"`
	Command      string   `json:"command"`
	Mirror       string   `json:"mirror"`
	Event        string   `json:"event"`
	Fault        Fault    `json:"fault"`
	Result       Outcome  `json:"result"`
	CheckMs      int64    `json:"check_ms"`
	TookMs       *int64   `json:"took_ms,omitempty"`
	SalvagedFrom []string `json:"salvaged_from,omitempty"`
	NetworkNotes []string `json:"network_notes,omitempty"`
	Probed       []string `json:"probed,omitempty"`
}

// cut returns l with every string a caller, a remote or a person could have
// chosen cut by cleanName, so the line stays one bounded line of printable
// ASCII. Time, Event and NetworkNotes are niwa's own and kept.
func (l logLine) cut() logLine {
	l.Command = cleanName(l.Command)
	l.Mirror = cleanName(l.Mirror)
	l.Fault = Fault(cleanName(string(l.Fault)))
	l.Result = Outcome(cleanName(string(l.Result)))
	if l.SalvagedFrom != nil {
		from := make([]string, len(l.SalvagedFrom))
		for i, name := range l.SalvagedFrom {
			from[i] = cleanName(name)
		}
		l.SalvagedFrom = from
	}
	if l.Probed != nil {
		probed := make([]string, len(l.Probed))
		for i, name := range l.Probed {
			probed[i] = cleanName(name)
		}
		l.Probed = probed
	}
	return l
}

// storeLine is the line logged when a store is removed (eventRemove) or a
// store a removal considered is kept (eventKeep): the call's time, command
// and mirror, the store as ".store/<name>", and why. Most removals and
// every keep are logged by a later command than the restore that retired
// the store, after that command's own check, restore and unrecoverable
// lines.
type storeLine struct {
	logLine
	Store  string `json:"store"`
	Reason string `json:"reason"`
}

// cut returns l with every string cut as logLine.cut does, the store's name
// included: a record can hold any name.
func (l storeLine) cut() storeLine {
	l.logLine = l.logLine.cut()
	l.Store = cleanName(l.Store)
	l.Reason = cleanName(l.Reason)
	return l
}

// unrecoverableLine is the line logged for each ref a restore left lost: the
// restore line's time, command, mirror, fault and result, and the LostRef's
// fields under keys no other event's line carries.
type unrecoverableLine struct {
	logLine
	Instance       string     `json:"instance"`
	Ref            string     `json:"ref"`
	MissingCommit  string     `json:"missing_commit"`
	RemoteTracking bool       `json:"remote_tracking"`
	OwnCommits     []string   `json:"own_commits"`
	LostFiles      []LostFile `json:"lost_files"`
}

// cut returns l with every string cut as logLine.cut does, each own commit
// and lost file path included. The slices are copied, never changed in
// place, since they're the caller's LostRef's.
func (l unrecoverableLine) cut() unrecoverableLine {
	l.logLine = l.logLine.cut()
	l.Instance = cleanName(l.Instance)
	l.Ref = cleanName(l.Ref)
	l.MissingCommit = cleanName(l.MissingCommit)
	own := make([]string, len(l.OwnCommits))
	for i, id := range l.OwnCommits {
		own[i] = cleanName(id)
	}
	l.OwnCommits = own
	files := make([]LostFile, len(l.LostFiles))
	for i, f := range l.LostFiles {
		files[i] = LostFile{Path: cleanName(f.Path), InWorkingTree: f.InWorkingTree}
	}
	l.LostFiles = files
	return l
}

// unrecoverableLines builds one unrecoverable line per lost ref from the
// restore line it follows.
func unrecoverableLines(restoreLine logLine, lost []LostRef) []any {
	var lines []any
	for _, l := range lost {
		base := restoreLine
		base.Event = eventUnrecoverable
		base.TookMs = nil
		base.SalvagedFrom = nil
		base.NetworkNotes = nil
		base.Probed = nil
		lines = append(lines, unrecoverableLine{
			logLine:        base,
			Instance:       l.Instance,
			Ref:            l.Ref,
			MissingCommit:  l.MissingCommit,
			RemoteTracking: l.RemoteTracking,
			OwnCommits:     l.OwnCommits,
			LostFiles:      l.LostFiles,
		})
	}
	return lines
}

// logPath is the mirror log of a workspace's mirrors.
func logPath(mdir string) string { return filepath.Join(metaDir(mdir), "mirror.log") }

// encodeLogLine cuts and encodes one line, a logLine, an unrecoverableLine
// or a storeLine; any other type is refused. This is the one place the log's strings are
// cut, so no caller can write a line that breaks the one-line-per-event
// form, whatever names it carries.
func encodeLogLine(line any) ([]byte, error) {
	var v any
	switch l := line.(type) {
	case logLine:
		v = l.cut()
	case unrecoverableLine:
		v = l.cut()
	case storeLine:
		v = l.cut()
	default:
		return nil, fmt.Errorf("mirror log: refusing a line of type %T", line)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// appendLog writes each line, a logLine, an unrecoverableLine or a
// storeLine, to the mirror log. Every line is encoded before anything is written, so a line of
// any other type fails the call with nothing written. Each line is one write
// to a file opened O_APPEND, so it lands whole beside another command's
// lines, and before each one the log rotates once it reaches logMax (see
// rotateLog). The file is created 0600. A failure is returned; the caller
// keeps it as the Result's LogErr, and it never changes what a check or
// restore did.
//
// Nothing is written unless the workspace key directory and mirrors/ are
// real directories the user owns and .meta/ is one too (ensurePrivateDir
// makes it when missing), so no line is ever written through a link to
// somewhere else.
func appendLog(mdir string, lines ...any) error {
	encoded := make([][]byte, 0, len(lines))
	for _, line := range lines {
		data, err := encodeLogLine(line)
		if err != nil {
			return err
		}
		encoded = append(encoded, data)
	}
	for _, dir := range []string{filepath.Dir(mdir), mdir} {
		if !realOwnedDir(dir) {
			return fmt.Errorf("mirror log: %s is not a directory the user owns", dir)
		}
	}
	if err := ensurePrivateDir(metaDir(mdir)); err != nil {
		return err
	}
	path := logPath(mdir)
	f, err := openLog(path)
	if err != nil {
		return err
	}
	defer func() { f.Close() }()
	var firstErr error
	for _, data := range encoded {
		// A file reopened because another command rotated first is checked
		// again; that it's full too takes several commands rotating at once,
		// so a few tries bound the loop.
		for try := 0; try < 3; try++ {
			next, err := rotateLog(f, path)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			if next == f {
				break
			}
			f = next
		}
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("writing the mirror log: %w", err)
		}
	}
	return firstErr
}

// openLog opens the mirror log at path for appending, creating it 0600. It
// never follows a link at path and never waits on a fifo there, and it
// refuses anything that, once open, isn't a regular file the user owns, so
// nothing is written through a link, a fifo or another user's file.
func openLog(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the mirror log: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("inspecting the mirror log: %w", err)
	}
	if !info.Mode().IsRegular() || !ownedByUser(info) {
		f.Close()
		return nil, fmt.Errorf("the mirror log %s is not a regular file the user owns", path)
	}
	return f, nil
}

// rotateLog returns the file the next line goes to. While f is smaller than
// logMax, or isn't a regular file, that's f. Once it's full, and the path
// still names it, mirror.log is renamed over mirror.log.1 and a new
// mirror.log is opened; when the path names another file, another command
// rotated first, and that file is opened instead. f is closed whenever
// another file is returned.
//
// The check and the rename run under an exclusive flock on the full file:
// two commands that both find it full take turns, and the second finds the
// path naming the first one's new file and reopens it, rather than renaming
// that new file over the generation just kept. A failure leaves the line
// going to f, the full file, and is returned to be reported.
func rotateLog(f *os.File, path string) (*os.File, error) {
	info, err := f.Stat()
	if err != nil {
		return f, fmt.Errorf("inspecting the mirror log: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() < logMax {
		return f, nil
	}
	fd := int(f.Fd())
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return f, fmt.Errorf("locking the mirror log to rotate it: %w", err)
	}
	next, err := rotateLocked(info, path)
	if err != nil {
		_ = unix.Flock(fd, unix.LOCK_UN)
		return f, err
	}
	// Closing the full file's descriptor releases its lock. It's never
	// unlocked by number after the close, since another goroutine's open
	// could have been given the same number by then.
	f.Close()
	return next, nil
}

// rotateLocked is rotateLog's work once it holds the full file's lock: info
// is that file's. It renames path over path.1 when path still names that
// file, then opens path. path.1 is never opened: the rename replaces
// whatever is there, a link included, rather than following it.
func rotateLocked(info os.FileInfo, path string) (*os.File, error) {
	named, err := os.Lstat(path)
	switch {
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("inspecting the mirror log: %w", err)
	case err == nil && os.SameFile(info, named):
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, fmt.Errorf("rotating the mirror log: %w", err)
		}
	}
	return openLog(path)
}

// logTime formats an event's time for the log.
func logTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }
