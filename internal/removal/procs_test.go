package removal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// recordedLsof is lsof -n -P -w -F 0pRn output in the framing lsof uses:
// every field ends in NUL and every set ends in a newline after its last
// field. It holds a process with no files, names lsof gives pipes and
// sockets, and a file whose name has a space.
var recordedLsof = []byte("" +
	"p591\x00R1\x00\n" +
	"fcwd\x00n/\x00\n" +
	"ftxt\x00n/usr/sbin/distnoted\x00\n" +
	"ftxt\x00n/usr/lib/dyld\x00\n" +
	"f0\x00n/dev/null\x00\n" +
	"f3\x00n->0xa05607116c32eab\x00\n" +
	"p600\x00R591\x00\n" +
	"p712\x00R600\x00\n" +
	"fcwd\x00n/Users/dana/ws/inst-1/public/niwa\x00\n" +
	"ftxt\x00n/Users/dana/ws/inst-1/public/niwa/bin/niwa\x00\n" +
	"f4\x00n/Users/dana/ws/inst-1/notes with space.txt\x00\n" +
	"f5\x00nlocalhost:8080->localhost:52311\x00\n")

func TestParseLsofReadsARecordedListing(t *testing.T) {
	got, err := parseLsof(recordedLsof)
	if err != nil {
		t.Fatal(err)
	}
	want := []Process{
		{PID: 591, PPID: 1, Cwd: "/", Files: []string{"/usr/sbin/distnoted", "/usr/lib/dyld", "/dev/null"}},
		{PID: 600, PPID: 591},
		{PID: 712, PPID: 600, Cwd: "/Users/dana/ws/inst-1/public/niwa", Files: []string{
			"/Users/dana/ws/inst-1/public/niwa/bin/niwa",
			"/Users/dana/ws/inst-1/notes with space.txt",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parsed:\n got %+v\nwant %+v", got, want)
	}
}

func TestParseLsofRejectsMalformedInput(t *testing.T) {
	cases := map[string]string{
		"empty":                      "",
		"last set not ended":         "p1\x00R0\x00",
		"field not NUL-terminated":   "p1\x00R0\n",
		"unknown leading field":      "x1\x00R0\x00\n",
		"unknown second field":       "p1\x00c0\x00\n",
		"non-numeric pid":            "pabc\x00R1\x00\n",
		"signed pid":                 "p+1\x00R1\x00\n",
		"empty pid":                  "p\x00R1\x00\n",
		"non-numeric parent":         "p1\x00Rx\x00\n",
		"process with no parent":     "p1\x00\n",
		"name with no preceding fd":  "p1\x00R0\x00\nn/x\x00\n",
		"fd with no name":            "p1\x00R0\x00\nf3\x00\n",
		"file before any process":    "fcwd\x00n/\x00\n",
		"empty fd":                   "p1\x00R0\x00\nf\x00n/x\x00\n",
		"two files in one set":       "p1\x00R0\x00\nf3\x00n/a\x00f4\x00n/b\x00\n",
		"empty field":                "p1\x00\x00R0\x00\n",
		"same process twice":         "p1\x00R0\x00\np1\x00R0\x00\n",
		"newline inside a set":       "p1\x00R0\x00\nf3\x00n/a\nb\x00\n",
		"extra field in process set": "p1\x00R0\x00g1\x00\n",
		"set with only one field":    "p1\x00R0\x00\njunk\x00\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := parseLsof([]byte(in)); err == nil {
				t.Errorf("parsed %q as %+v, want an error", in, got)
			}
		})
	}
}

func TestParseBootTimeReadsTheBtimeLine(t *testing.T) {
	stat := []byte("cpu  10 0 5 100 0 0 0 0 0 0\nintr 1 2 3\nctxt 99\nbtime 1700000000\nprocesses 42\n")
	got, err := parseBootTime(stat)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("boot time %v, want %v", got, time.Unix(1700000000, 0))
	}
	for _, bad := range []string{"cpu 1 2\nprocesses 3\n", "btime soon\n", "btime -5\n", "btime \n"} {
		if _, err := parseBootTime([]byte(bad)); err == nil {
			t.Errorf("parsed %q, want an error", bad)
		}
	}
}

// statLine is a /proc/<pid>/stat line with the given command name, parent
// and start time in ticks at fields 2, 4 and 22.
func statLine(pid int, comm string, ppid int, startTicks uint64) string {
	return fmt.Sprintf("%d (%s) S %d 0 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 %d 0 0\n", pid, comm, ppid, startTicks)
}

// fakeProc adds one process directory to a fake /proc tree. An empty stat
// leaves the stat file out; a nil fds leaves the fd directory out.
func fakeProc(t *testing.T, root string, pid int, stat, cwd string, fds map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if stat != "" {
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if cwd != "" {
		if err := os.Symlink(cwd, filepath.Join(dir, "cwd")); err != nil {
			t.Fatal(err)
		}
	}
	if fds != nil {
		fdDir := filepath.Join(dir, "fd")
		if err := os.Mkdir(fdDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for fd, target := range fds {
			if err := os.Symlink(target, filepath.Join(fdDir, fd)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func TestReadProcReadsAFakeTree(t *testing.T) {
	root := t.TempDir()
	boot := time.Unix(1700000000, 0)
	fakeProc(t, root, 100, statLine(100, "bash", 1, 12345), "/ws/inst-1", map[string]string{
		"0": "/dev/null",
		"1": "pipe:[4021]",
		"2": "socket:[4022]",
		"3": "/ws/inst-1/build/out.o (deleted)",
		"4": "anon_inode:[eventfd]",
	})
	// A command name with spaces and parentheses: fields count from the
	// last ')'.
	fakeProc(t, root, 101, statLine(101, "a) b (c) S 9 9", 100, 250), "/home/dana", map[string]string{})
	// Gone between the directory read and the process's own reads.
	fakeProc(t, root, 102, "", "", nil)
	// Entries that aren't processes.
	if err := os.Symlink("100", filepath.Join(root, "self")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "stat"), []byte("btime 1700000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readProc(root, boot, 100)
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(got, func(a, b Process) int { return a.PID - b.PID })
	for i := range got {
		slices.Sort(got[i].Files)
	}
	want := []Process{
		{PID: 100, PPID: 1, Start: boot.Add(123450 * time.Millisecond), Cwd: "/ws/inst-1",
			Files: []string{"/dev/null", "/ws/inst-1/build/out.o (deleted)"}},
		{PID: 101, PPID: 100, Start: boot.Add(2500 * time.Millisecond), Cwd: "/home/dana"},
	}
	if len(got) != len(want) {
		t.Fatalf("read %+v, want %+v", got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.PID != w.PID || g.PPID != w.PPID || !g.Start.Equal(w.Start) || g.Cwd != w.Cwd || !slices.Equal(g.Files, w.Files) {
			t.Errorf("process %d:\n got %+v\nwant %+v", w.PID, g, w)
		}
	}
}

func TestReadProcSkipsAProcessWhoseDescriptorsAreUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions don't hold for root")
	}
	root := t.TempDir()
	fakeProc(t, root, 200, statLine(200, "sh", 1, 1), "/", map[string]string{"0": "/dev/null"})
	dir := fakeProc(t, root, 201, statLine(201, "sshd", 1, 1), "/", map[string]string{"0": "/dev/null"})
	fdDir := filepath.Join(dir, "fd")
	if err := os.Chmod(fdDir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(fdDir, 0o755) })

	got, err := readProc(root, time.Unix(0, 0), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PID != 200 {
		t.Errorf("read %+v, want only process 200", got)
	}
}

func TestReadProcFailsOnAStatusThatDoesNotParse(t *testing.T) {
	cases := map[string]string{
		"no parenthesis":     "300 bash S 1 0 0\n",
		"pid mismatch":       statLine(301, "bash", 1, 1),
		"too few fields":     "300 (bash) S 1 0 0 0\n",
		"non-numeric parent": "300 (bash) S x 0 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 5 0 0\n",
		"non-numeric start":  "300 (bash) S 1 0 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 soon 0 0\n",
		"empty":              "\n",
	}
	for name, stat := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			fakeProc(t, root, 300, stat, "/", map[string]string{})
			if got, err := readProc(root, time.Unix(0, 0), 100); err == nil {
				t.Errorf("read %+v, want an error", got)
			}
		})
	}
}

func TestReadProcFailsOnOtherErrors(t *testing.T) {
	if _, err := readProc(filepath.Join(t.TempDir(), "missing"), time.Unix(0, 0), 100); err == nil {
		t.Error("a missing process table read without an error")
	}
	if _, err := readProc(t.TempDir(), time.Unix(0, 0), 0); err == nil {
		t.Error("zero ticks per second read without an error")
	}
	// A working directory that is a plain file rather than a link can't be
	// read as a link, and that isn't a process going away.
	root := t.TempDir()
	dir := fakeProc(t, root, 400, statLine(400, "sh", 1, 1), "", map[string]string{})
	if err := os.WriteFile(filepath.Join(dir, "cwd"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := readProc(root, time.Unix(0, 0), 100); err == nil {
		t.Errorf("read %+v, want an error", got)
	}
}

func TestAttributeNamesInstanceAndBuildTrees(t *testing.T) {
	l := &Listing{Procs: []Process{
		{PID: 1, Cwd: "/ws/inst-1/public/niwa"},
		{PID: 2, Cwd: "/home/dana", Files: []string{"/tmp/x", "/ws/inst-2/notes.txt", "/cache/build/inst-1/obj.o"}},
		{PID: 3, Cwd: "/ws/inst-10", Files: []string{"/cache/build/inst-10/a"}},
		{PID: 4, Cwd: "/ws/inst-1", Files: []string{"/ws/inst-2/x", "/cache/build/inst-1", "/cache/build/inst-2/y"}},
		{PID: 5, Cwd: "/", Files: []string{"/dev/null"}},
		{PID: 6, Cwd: "/ws/inst-1/../inst-2/./sub"},
	}}
	l.Attribute([]string{"/ws/inst-1", "/ws/inst-2/"}, []string{"/cache/build/inst-1", "/cache/build/inst-2"})
	want := map[int]struct {
		inst  string
		trees []string
	}{
		1: {"/ws/inst-1", nil},
		2: {"/ws/inst-2/", []string{"/cache/build/inst-1"}},
		3: {"", nil},
		4: {"/ws/inst-1", []string{"/cache/build/inst-1", "/cache/build/inst-2"}},
		5: {"", nil},
		6: {"/ws/inst-2/", nil},
	}
	for _, p := range l.Procs {
		w := want[p.PID]
		if p.Instance != w.inst || !slices.Equal(p.BuildTrees, w.trees) {
			t.Errorf("process %d: instance %q build trees %q, want %q and %q", p.PID, p.Instance, p.BuildTrees, w.inst, w.trees)
		}
	}

	// A second pass replaces what the first one filled.
	l.Attribute(nil, nil)
	for _, p := range l.Procs {
		if p.Instance != "" || p.BuildTrees != nil {
			t.Errorf("process %d kept instance %q build trees %q after an empty attribution", p.PID, p.Instance, p.BuildTrees)
		}
	}
	var nilListing *Listing
	nilListing.Attribute([]string{"/ws"}, nil)
}

// linkedDir makes a real directory and a symlink to it, returning both with
// the real one fully resolved, as the OS reports a process path.
func linkedDir(t *testing.T) (real, link string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real = filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	return real, link
}

func TestAttributeResolvesTheInstanceButNotProcessPaths(t *testing.T) {
	real, link := linkedDir(t)
	elsewhere := filepath.Join(filepath.Dir(real), "elsewhere")
	if err := os.Mkdir(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(elsewhere, "to-real")
	if err := os.Symlink(real, inner); err != nil {
		t.Fatal(err)
	}
	l := &Listing{Procs: []Process{
		{PID: 1, Cwd: filepath.Join(real, "sub")},
		{PID: 2, Cwd: filepath.Join(inner, "sub")},
		{PID: 3, Cwd: "/", Files: []string{filepath.Join(real, "f")}},
	}}
	l.Attribute([]string{link}, []string{link})
	if got := l.Procs[0]; got.Instance != link || !slices.Equal(got.BuildTrees, []string{link}) {
		t.Errorf("a process in the resolved directory: %+v", got)
	}
	if got := l.Procs[1]; got.Instance != "" || got.BuildTrees != nil {
		t.Errorf("a process reached through a link of its own was attributed: %+v", got)
	}
	if got := l.Procs[2]; got.Instance != link || !slices.Equal(got.BuildTrees, []string{link}) {
		t.Errorf("a process with a file in the resolved directory: %+v", got)
	}
}

func TestCwdWithinMatchesByComponentAndResolvedDir(t *testing.T) {
	l := &Listing{Procs: []Process{
		{PID: 1, Cwd: "/a/b"},
		{PID: 2, Cwd: "/a/b/c/d"},
		{PID: 3, Cwd: "/a/bc"},
		{PID: 4, Cwd: "/a", Files: []string{"/a/b/f"}},
		{PID: 5},
	}}
	var pids []int
	for _, p := range l.CwdWithin("/a/b/") {
		pids = append(pids, p.PID)
	}
	if !slices.Equal(pids, []int{1, 2}) {
		t.Errorf("processes within /a/b: %v, want [1 2]", pids)
	}

	real, link := linkedDir(t)
	l = &Listing{Procs: []Process{{PID: 7, Cwd: real}, {PID: 8, Cwd: filepath.Dir(real)}}}
	got := l.CwdWithin(link)
	if len(got) != 1 || got[0].PID != 7 {
		t.Errorf("processes within the linked directory: %+v, want process 7", got)
	}

	var nilListing *Listing
	if got := nilListing.CwdWithin("/"); got != nil {
		t.Errorf("a nil listing gave %+v", got)
	}
	if got := l.CwdWithin(""); got != nil {
		t.Errorf("an empty directory gave %+v", got)
	}
}

func TestChangedComparesPIDAndStart(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	earlier := &Listing{Procs: []Process{
		{PID: 10, Start: t0},
		{PID: 11, Start: t0.Add(time.Second)},
		{PID: 12, Start: t0.Add(2 * time.Second)},
	}}
	later := &Listing{Procs: []Process{
		{PID: 10, Start: t0},
		{PID: 11, Start: t0.Add(time.Second + time.Millisecond)},
		{PID: 13, Start: t0.Add(3 * time.Second)},
	}}
	var pids []int
	for _, p := range Changed(earlier, later) {
		pids = append(pids, p.PID)
	}
	if !slices.Equal(pids, []int{11, 13}) {
		t.Errorf("changed processes %v, want [11 13]", pids)
	}
	// The same instant in another location is the same start.
	same := &Listing{Procs: []Process{{PID: 10, Start: t0.In(time.FixedZone("x", 3600))}}}
	if got := Changed(earlier, same); len(got) != 0 {
		t.Errorf("the same start in another zone counted as changed: %+v", got)
	}
	if got := Changed(nil, later); len(got) != 3 {
		t.Errorf("against a nil earlier listing, %d changed, want 3", len(got))
	}
	if got := Changed(earlier, nil); got != nil {
		t.Errorf("a nil later listing gave %+v", got)
	}
}

func TestListProcessesIsUndecidableWithoutLsofOnDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("lsof is the reader only on darwin")
	}
	var nilEnv *Env
	for _, e := range []*Env{nilEnv, {}} {
		if l := e.ListProcesses(context.Background()); l == nil || l.Err == nil {
			t.Errorf("a listing with no lsof decided something: %+v", l)
		}
	}
}

// hostEnv is an Env that can take a real listing on this host, or skips the
// test where there is no reader.
func hostEnv(t *testing.T) *Env {
	t.Helper()
	switch runtime.GOOS {
	case "linux":
		return &Env{PATH: "/usr/bin:/bin"}
	case "darwin":
		if _, err := os.Stat("/usr/sbin/lsof"); err != nil {
			t.Skip("no lsof on this host")
		}
		return &Env{PATH: "/usr/bin:/bin:/usr/sbin", Lsof: "/usr/sbin/lsof"}
	}
	t.Skip("no process listing on this platform")
	return nil
}

// sleeper starts a process that waits in dir until the test ends.
func sleeper(t *testing.T, dir string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "60")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	return cmd
}

func TestListProcessesSeesARunningChild(t *testing.T) {
	e := hostEnv(t)
	dir := t.TempDir()
	first := sleeper(t, dir)

	before := e.ListProcesses(context.Background())
	if before.Err != nil {
		t.Fatal(before.Err)
	}
	for _, p := range before.Procs {
		if p.PID == os.Getpid() {
			t.Error("the listing holds the calling process")
		}
		if p.Start.IsZero() {
			t.Errorf("process %d has no start time", p.PID)
		}
	}
	in := before.CwdWithin(dir)
	if len(in) != 1 || in[0].PID != first.Process.Pid || in[0].PPID != os.Getpid() {
		t.Fatalf("processes in the child's directory: %+v, want the child %d", in, first.Process.Pid)
	}
	before.Attribute([]string{dir}, nil)
	for _, p := range before.Procs {
		if p.PID == first.Process.Pid && p.Instance != dir {
			t.Errorf("the child was attributed to %q, want %q", p.Instance, dir)
		}
	}

	second := sleeper(t, dir)
	after := e.ListProcesses(context.Background())
	if after.Err != nil {
		t.Fatal(after.Err)
	}
	var changed []int
	for _, p := range Changed(before, after) {
		changed = append(changed, p.PID)
	}
	if !slices.Contains(changed, second.Process.Pid) {
		t.Errorf("the new child %d is missing from the changed processes %v", second.Process.Pid, changed)
	}
	if slices.Contains(changed, first.Process.Pid) {
		t.Errorf("the first child %d counted as changed", first.Process.Pid)
	}
}

// fakeLsof writes an executable script standing in for lsof and returns an
// Env that runs it.
func fakeLsof(t *testing.T, body string) *Env {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("lsof is the reader only on darwin")
	}
	path := filepath.Join(t.TempDir(), "lsof")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Env{PATH: "/usr/bin:/bin", Lsof: path}
}

// childListing writes an lsof listing naming a live child and returns its
// path and the child's pid.
func childListing(t *testing.T) (string, int) {
	t.Helper()
	child := sleeper(t, t.TempDir())
	listing := fmt.Sprintf("p%d\x00R%d\x00\nfcwd\x00n/somewhere\x00\n", child.Process.Pid, os.Getpid())
	path := filepath.Join(t.TempDir(), "listing")
	if err := os.WriteFile(path, []byte(listing), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, child.Process.Pid
}

func TestListProcessesRunsLsofWithFixedArgumentsAndEnvironment(t *testing.T) {
	listing, pid := childListing(t)
	out := t.TempDir()
	e := fakeLsof(t, fmt.Sprintf(
		"printf '%%s\\n' \"$@\" > %s/args\n/usr/bin/env > %s/env\n/bin/pwd > %s/pwd\n/bin/cat %s\n",
		out, out, out, listing))
	l := e.ListProcesses(context.Background())
	if l.Err != nil {
		t.Fatal(l.Err)
	}
	if len(l.Procs) != 1 || l.Procs[0].PID != pid || l.Procs[0].Cwd != "/somewhere" || l.Procs[0].Start.IsZero() {
		t.Errorf("listing %+v, want the child %d with its start time", l.Procs, pid)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read("args"); got != "-n\n-P\n-w\n-F\n0pRn\n" {
		t.Errorf("lsof arguments %q", got)
	}
	if got := read("pwd"); got != "/\n" {
		t.Errorf("lsof ran from %q, want /", got)
	}
	env := strings.Split(read("env"), "\n")
	if !slices.Contains(env, "PATH=/usr/bin:/bin") || !slices.Contains(env, "LANG=C") {
		t.Errorf("lsof environment %q lacks the filtered PATH or LANG=C", env)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "USER=") {
			t.Errorf("lsof inherited %q", kv)
		}
	}
}

func TestListProcessesAcceptsLsofExitOneWithOutput(t *testing.T) {
	listing, pid := childListing(t)
	l := fakeLsof(t, "/bin/cat "+listing+"\nexit 1\n").ListProcesses(context.Background())
	if l.Err != nil {
		t.Fatal(l.Err)
	}
	if len(l.Procs) != 1 || l.Procs[0].PID != pid {
		t.Errorf("listing %+v, want the child %d", l.Procs, pid)
	}
}

func TestListProcessesFailsOnOtherLsofFailures(t *testing.T) {
	listing, _ := childListing(t)
	cases := map[string]string{
		"exit two with output":      "/bin/cat " + listing + "\nexit 2\n",
		"exit one with no output":   "exit 1\n",
		"output that doesn't parse": "printf 'garbage\\n'\n",
		"no output":                 "exit 0\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			l := fakeLsof(t, body).ListProcesses(context.Background())
			if l.Err == nil {
				t.Errorf("listing %+v, want an error", l.Procs)
			}
		})
	}
}

func TestListProcessesDropsAProcessThatExited(t *testing.T) {
	gone := exec.Command("/usr/bin/true")
	if err := gone.Run(); err != nil {
		t.Skip("can't start a short-lived process")
	}
	listing, pid := childListing(t)
	extra := fmt.Sprintf("p%d\x00R1\x00\n", gone.Process.Pid)
	path := filepath.Join(t.TempDir(), "with-gone")
	b, err := os.ReadFile(listing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte(extra), b...), 0o644); err != nil {
		t.Fatal(err)
	}
	l := fakeLsof(t, "/bin/cat "+path+"\n").ListProcesses(context.Background())
	if l.Err != nil {
		t.Fatal(l.Err)
	}
	if len(l.Procs) != 1 || l.Procs[0].PID != pid {
		t.Errorf("listing %+v, want only the live child %d", l.Procs, pid)
	}
}

func TestListProcessesTimesOut(t *testing.T) {
	e := fakeLsof(t, "exec /bin/sleep 30\n")
	old := listTimeout
	listTimeout = 200 * time.Millisecond
	t.Cleanup(func() { listTimeout = old })
	start := time.Now()
	l := e.ListProcesses(context.Background())
	if l.Err == nil {
		t.Errorf("a listing that never finished decided something: %+v", l.Procs)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the listing took %v to give up", d)
	}
}
