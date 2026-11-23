package removal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// listTimeout bounds the one process listing a pass takes. A variable so a
// test can watch it fire without waiting ten seconds.
var listTimeout = 10 * time.Second

// Process is one running process as the listing saw it.
type Process struct {
	PID, PPID int
	// Start is when the process started. Together with PID it tells one
	// process from a later one that reused the PID.
	Start time.Time
	// Cwd is the working directory and Files the open files, as absolute
	// paths exactly as the OS reports them; links in them are never followed.
	Cwd   string
	Files []string
	// Instance and BuildTrees are filled by Attribute.
	Instance   string
	BuildTrees []string
}

// Listing is one pass's view of the running processes. A nil Listing, or one
// with Err set, decides nothing: no process can be said to be absent from it.
type Listing struct {
	Procs []Process
	Err   error
}

// ListProcesses takes the pass's process listing under its own short context.
// The calling process is left out. Any failure, a timeout or output that
// doesn't parse sets Err; a listing is never returned partial.
func (e *Env) ListProcesses(ctx context.Context) *Listing {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	procs, err := e.readProcesses(ctx)
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("process listing: %v: %w", ctx.Err(), err)
		}
		return &Listing{Err: err}
	}
	self := os.Getpid()
	kept := make([]Process, 0, len(procs))
	for _, p := range procs {
		if p.PID != self {
			kept = append(kept, p)
		}
	}
	return &Listing{Procs: kept}
}

// dirForms is a directory as given, cleaned, plus its symlink-resolved form
// when that differs, so a process path the OS reports through the resolved
// form (macOS reports /private/var for /var) still matches.
type dirForms struct {
	name  string
	forms []string
}

func formsOf(dir string) (dirForms, bool) {
	if dir == "" {
		return dirForms{}, false
	}
	clean := filepath.Clean(dir)
	d := dirForms{name: dir, forms: []string{clean}}
	if r, err := filepath.EvalSymlinks(clean); err == nil && r != clean {
		d.forms = append(d.forms, r)
	}
	return d, true
}

func formsOfAll(dirs []string) []dirForms {
	var out []dirForms
	for _, dir := range dirs {
		if d, ok := formsOf(dir); ok {
			out = append(out, d)
		}
	}
	return out
}

// contains reports whether p, cleaned but never resolved, is at or below one
// of d's forms. The comparison is by path component, so /a/b doesn't contain
// /a/bc.
func (d dirForms) contains(p string) bool {
	if p == "" {
		return false
	}
	p = filepath.Clean(p)
	for _, f := range d.forms {
		if within(p, f) {
			return true
		}
	}
	return false
}

func (d dirForms) containsAny(paths []string) bool {
	for _, p := range paths {
		if d.contains(p) {
			return true
		}
	}
	return false
}

// Attribute fills each process's Instance and BuildTrees. Instance is the
// instance directory its working directory sits in or, failing that, the
// first one (in the order given) holding one of its open files. BuildTrees is
// every build tree its working directory or an open file sits in. Both are
// reported as given in the arguments.
func (l *Listing) Attribute(instances []string, buildTrees []string) {
	if l == nil {
		return
	}
	inst := formsOfAll(instances)
	trees := formsOfAll(buildTrees)
	for i := range l.Procs {
		p := &l.Procs[i]
		p.Instance = ""
		p.BuildTrees = nil
		for _, d := range inst {
			if d.contains(p.Cwd) {
				p.Instance = d.name
				break
			}
		}
		if p.Instance == "" {
			for _, d := range inst {
				if d.containsAny(p.Files) {
					p.Instance = d.name
					break
				}
			}
		}
		for _, d := range trees {
			if d.contains(p.Cwd) || d.containsAny(p.Files) {
				p.BuildTrees = append(p.BuildTrees, d.name)
			}
		}
	}
}

// CwdWithin returns the processes whose working directory is dir or lies
// under it.
func (l *Listing) CwdWithin(dir string) []Process {
	if l == nil {
		return nil
	}
	d, ok := formsOf(dir)
	if !ok {
		return nil
	}
	var out []Process
	for _, p := range l.Procs {
		if d.contains(p.Cwd) {
			out = append(out, p)
		}
	}
	return out
}

// procKey identifies one process across listings: a PID reused by a later
// process has a different start.
type procKey struct {
	pid  int
	sec  int64
	nsec int
}

func keyOf(p Process) procKey {
	return procKey{pid: p.PID, sec: p.Start.Unix(), nsec: p.Start.Nanosecond()}
}

// Changed returns the processes in later that earlier doesn't hold: a PID
// earlier lacks, or one earlier holds with a different start. A nil earlier
// holds nothing.
func Changed(earlier, later *Listing) []Process {
	if later == nil {
		return nil
	}
	seen := map[procKey]bool{}
	if earlier != nil {
		for _, p := range earlier.Procs {
			seen[keyOf(p)] = true
		}
	}
	var out []Process
	for _, p := range later.Procs {
		if !seen[keyOf(p)] {
			out = append(out, p)
		}
	}
	return out
}

// isDecimal reports whether s is a non-empty run of ASCII digits.
func isDecimal(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func parseID(s string) (int, error) {
	if !isDecimal(s) {
		return 0, fmt.Errorf("%q is not a process id", s)
	}
	return strconv.Atoi(s)
}

// parseLsof reads lsof -n -P -w -F 0pRn output. With -F0 every field ends in
// NUL and every set ends in a newline after its last field. A process set is
// exactly p<pid> then R<ppid>; each file set after it is exactly f<fd> then
// n<name>. The file with fd cwd is the working directory; of the other names
// only absolute paths are kept, since lsof names pipes and sockets with
// other text. Anything else is an error.
func parseLsof(out []byte) ([]Process, error) {
	if len(out) == 0 {
		return nil, errors.New("process listing is empty")
	}
	if !bytes.HasSuffix(out, []byte("\x00\n")) {
		return nil, errors.New("process listing does not end a set")
	}
	var procs []Process
	seen := map[int]bool{}
	var cur *Process
	for _, set := range strings.Split(string(out[:len(out)-2]), "\x00\n") {
		fields := strings.Split(set, "\x00")
		for _, f := range fields {
			if f == "" || strings.Contains(f, "\n") {
				return nil, fmt.Errorf("process listing has a set that does not parse: %q", set)
			}
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("process listing has a set that does not parse: %q", set)
		}
		first, second := fields[0], fields[1]
		switch {
		case first[0] == 'p' && second[0] == 'R':
			pid, err := parseID(first[1:])
			if err != nil {
				return nil, fmt.Errorf("process listing: %v", err)
			}
			ppid, err := parseID(second[1:])
			if err != nil {
				return nil, fmt.Errorf("process listing: %v", err)
			}
			if seen[pid] {
				return nil, fmt.Errorf("process listing names process %d twice", pid)
			}
			seen[pid] = true
			procs = append(procs, Process{PID: pid, PPID: ppid})
			cur = &procs[len(procs)-1]
		case first[0] == 'f' && second[0] == 'n':
			if cur == nil {
				return nil, errors.New("process listing has a file before any process")
			}
			fd, name := first[1:], second[1:]
			if fd == "" {
				return nil, fmt.Errorf("process listing has a file with no descriptor: %q", set)
			}
			switch {
			case fd == "cwd":
				cur.Cwd = name
			case strings.HasPrefix(name, "/"):
				cur.Files = append(cur.Files, name)
			}
		default:
			return nil, fmt.Errorf("process listing has a set that does not parse: %q", set)
		}
	}
	return procs, nil
}

// parseBootTime reads the btime line of /proc/stat, the boot time in seconds
// since the epoch.
func parseBootTime(procStat []byte) (time.Time, error) {
	for _, line := range strings.Split(string(procStat), "\n") {
		key, value, ok := strings.Cut(line, " ")
		if !ok || key != "btime" {
			continue
		}
		value = strings.TrimSpace(value)
		if !isDecimal(value) {
			return time.Time{}, fmt.Errorf("boot time %q does not parse", value)
		}
		sec, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("boot time %q does not parse", value)
		}
		return time.Unix(sec, 0), nil
	}
	return time.Time{}, errors.New("no boot time in the system statistics")
}

// procSkippable reports whether a /proc read error means the process is
// gone or not ours to read, which drops it rather than failing the listing.
func procSkippable(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission)
}

// parseProcStat reads the parent PID (field 4) and start time in clock ticks
// since boot (field 22) from a /proc/<pid>/stat line. The command name in
// field 2 can hold spaces and parentheses, so fields are counted from after
// its last ')'.
func parseProcStat(pid int, stat []byte) (ppid int, startTicks uint64, err error) {
	s := strings.TrimSuffix(string(stat), "\n")
	head, _, ok := strings.Cut(s, " (")
	end := strings.LastIndexByte(s, ')')
	if !ok || end < 0 || head != strconv.Itoa(pid) {
		return 0, 0, fmt.Errorf("process %d status does not parse", pid)
	}
	fields := strings.Fields(s[end+1:])
	// fields[0] is field 3, so field n is fields[n-3].
	if len(fields) < 20 {
		return 0, 0, fmt.Errorf("process %d status does not parse", pid)
	}
	ppid, err = parseID(fields[1])
	if err != nil {
		return 0, 0, fmt.Errorf("process %d status does not parse: %v", pid, err)
	}
	if !isDecimal(fields[19]) {
		return 0, 0, fmt.Errorf("process %d status does not parse: start %q", pid, fields[19])
	}
	startTicks, err = strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("process %d status does not parse: start %q", pid, fields[19])
	}
	return ppid, startTicks, nil
}

// readProc lists the processes in a /proc tree at root. A process that
// vanished during the read, or whose working directory or descriptors aren't
// readable, is left out; any other error, or a status line that doesn't
// parse, fails the whole listing.
func readProc(root string, bootTime time.Time, ticksPerSec int64) ([]Process, error) {
	if ticksPerSec <= 0 {
		return nil, fmt.Errorf("clock ticks per second %d is not positive", ticksPerSec)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("reading the process table: %v", err)
	}
	var procs []Process
	for _, ent := range entries {
		if !isDecimal(ent.Name()) {
			continue
		}
		pid, err := strconv.Atoi(ent.Name())
		if err != nil {
			continue
		}
		p, ok, err := readProcOne(filepath.Join(root, ent.Name()), pid, bootTime, ticksPerSec)
		if err != nil {
			return nil, err
		}
		if ok {
			procs = append(procs, p)
		}
	}
	return procs, nil
}

// readProcOne reads one /proc/<pid> directory. ok is false when the process
// is to be left out.
func readProcOne(dir string, pid int, bootTime time.Time, ticksPerSec int64) (p Process, ok bool, err error) {
	stat, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		if procSkippable(err) {
			return Process{}, false, nil
		}
		return Process{}, false, fmt.Errorf("reading process %d status: %v", pid, err)
	}
	ppid, ticks, err := parseProcStat(pid, stat)
	if err != nil {
		return Process{}, false, err
	}
	cwd, err := os.Readlink(filepath.Join(dir, "cwd"))
	if err != nil {
		if procSkippable(err) {
			return Process{}, false, nil
		}
		return Process{}, false, fmt.Errorf("reading process %d working directory: %v", pid, err)
	}
	fdDir := filepath.Join(dir, "fd")
	fds, err := os.ReadDir(fdDir)
	if err != nil {
		if procSkippable(err) {
			return Process{}, false, nil
		}
		return Process{}, false, fmt.Errorf("reading process %d descriptors: %v", pid, err)
	}
	var files []string
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
		switch {
		case err == nil:
		case errors.Is(err, fs.ErrNotExist):
			// The descriptor closed since the directory was read; if the
			// whole process is gone, its other reads say so.
			continue
		case errors.Is(err, fs.ErrPermission):
			return Process{}, false, nil
		default:
			return Process{}, false, fmt.Errorf("reading process %d descriptor %s: %v", pid, fd.Name(), err)
		}
		// Pipes, sockets and anonymous inodes read as text like
		// socket:[123]; only paths are files.
		if strings.HasPrefix(target, "/") {
			files = append(files, target)
		}
	}
	tps := uint64(ticksPerSec)
	start := bootTime.Add(time.Duration(ticks/tps)*time.Second + time.Duration(ticks%tps)*time.Second/time.Duration(tps))
	return Process{PID: pid, PPID: ppid, Start: start, Cwd: cwd, Files: files}, true, nil
}
