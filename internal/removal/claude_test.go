package removal

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// vetFixture is a home directory with ~/.claude/jobs/<id> holding a file
// and a subdirectory with a file, every directory 0700.
type vetFixture struct {
	base, home, jobs, id, jobDir string
}

func newVetFixture(t *testing.T) vetFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := vetFixture{base: base, home: filepath.Join(base, "home"), id: "abcdef12"}
	f.jobs = filepath.Join(f.home, ".claude", "jobs")
	f.jobDir = filepath.Join(f.jobs, f.id)
	for _, d := range []string{f.home, filepath.Join(f.home, ".claude"), f.jobs, f.jobDir, filepath.Join(f.jobDir, "scratch")} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(f.jobDir, "state.json"), filepath.Join(f.jobDir, "scratch", "out")} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// outside makes a directory outside the home holding a marker file.
func (f vetFixture) outside(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(f.base, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// vetErr runs v and returns its *VetError, failing the test on a pass or
// another error type.
func vetErr(t *testing.T, err error) *VetError {
	t.Helper()
	if err == nil {
		t.Fatal("the vet passed; want it to fail")
	}
	var ve *VetError
	if !errors.As(err, &ve) {
		t.Fatalf("the vet failed with %T %v; want a *VetError", err, err)
	}
	return ve
}

func TestVetJobDirPassesAPlainJobDirectory(t *testing.T) {
	f := newVetFixture(t)
	if err := VetJobDir(context.Background(), f.jobs, f.home, f.id); err != nil {
		t.Fatalf("the vet failed on a plain job directory: %v", err)
	}
	// The full session id finds the leading-slice directory Claude Code
	// writes.
	if err := VetJobDir(context.Background(), f.jobs, f.home, f.id+"-3456-4789-8abc-def012345678"); err != nil {
		t.Fatalf("the vet failed on the leading-slice layout: %v", err)
	}
}

func TestVetJobDirNamesAMissingJobDirectory(t *testing.T) {
	f := newVetFixture(t)
	ve := vetErr(t, VetJobDir(context.Background(), f.jobs, f.home, "99999999"))
	if want := filepath.Join(f.jobs, "99999999"); ve.Path != want || !strings.Contains(ve.Reason, want) {
		t.Fatalf("missing job directory named %q (%s); want %q", ve.Path, ve.Reason, want)
	}
}

// With no directory under the full id and more than one directory whose
// name is a leading slice of it, the vet can't tell which one Claude Code
// will remove, so it keeps, naming the jobs root, though JobDirFor (the
// rule's lookup) still picks the first.
func TestVetJobDirRefusesAnAmbiguousLeadingSlice(t *testing.T) {
	f := newVetFixture(t)
	second := filepath.Join(f.jobs, f.id+"3")
	if err := os.Mkdir(second, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(second, 0o700); err != nil {
		t.Fatal(err)
	}
	full := f.id + "3456-4789-8abc-def012345678"
	ve := vetErr(t, VetJobDir(context.Background(), f.jobs, f.home, full))
	if ve.Path != f.jobs || !strings.Contains(ve.Reason, "more than one job directory") {
		t.Fatalf("an ambiguous leading slice: %q (%s); want the jobs root named", ve.Path, ve.Reason)
	}
	if got := JobDirFor(f.jobs, full); got != f.jobDir {
		t.Fatalf("JobDirFor changed: %q; want %q", got, f.jobDir)
	}
	// A directory under the full id is the one, as for the rule.
	exact := filepath.Join(f.jobs, full)
	if err := os.Mkdir(exact, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(exact, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := VetJobDir(context.Background(), f.jobs, f.home, full); err != nil {
		t.Fatalf("the vet with a directory under the full id: %v", err)
	}
}

func TestVetJobDirRefusesAnIDThatIsAPath(t *testing.T) {
	f := newVetFixture(t)
	for _, id := range []string{"", ".", "..", "../abcdef12", "a/b", "a\x00b"} {
		if err := VetJobDir(context.Background(), f.jobs, f.home, id); err == nil {
			t.Errorf("the vet passed id %q", id)
		}
	}
}

func TestVetJobDirRefusesALinkAtEachLevel(t *testing.T) {
	cases := []struct {
		name string
		// link replaces the directory at this path with a link to a copy
		// of it outside the home, and returns the path the vet must name.
		at func(f vetFixture) string
	}{
		{"config directory", func(f vetFixture) string { return filepath.Join(f.home, ".claude") }},
		{"jobs root", func(f vetFixture) string { return f.jobs }},
		{"job directory", func(f vetFixture) string { return f.jobDir }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newVetFixture(t)
			at := c.at(f)
			target := filepath.Join(f.base, "outside")
			if err := os.Rename(at, target); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "marker"), []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, at); err != nil {
				t.Fatal(err)
			}
			ve := vetErr(t, VetJobDir(context.Background(), f.jobs, f.home, f.id))
			if ve.Path != at || !strings.Contains(ve.Reason, "symbolic link") {
				t.Fatalf("vet named %q (%s); want the link at %s", ve.Path, ve.Reason, at)
			}
			if _, err := os.Stat(filepath.Join(target, "marker")); err != nil {
				t.Fatalf("the vet disturbed the link's target: %v", err)
			}
		})
	}
}

func TestVetJobDirAllowsAHomeThatIsALink(t *testing.T) {
	f := newVetFixture(t)
	linked := filepath.Join(f.base, "home-link")
	if err := os.Symlink(f.home, linked); err != nil {
		t.Fatal(err)
	}
	if err := VetJobDir(context.Background(), filepath.Join(linked, ".claude", "jobs"), linked, f.id); err != nil {
		t.Fatalf("a home reached through a link failed the vet: %v", err)
	}
}

func TestVetJobDirChecksAConfigDirectoryOutsideTheHomeFromItsOwnRoot(t *testing.T) {
	f := newVetFixture(t)
	cfg := filepath.Join(f.base, "cfg")
	if err := os.Rename(filepath.Join(f.home, ".claude"), cfg); err != nil {
		t.Fatal(err)
	}
	jobs := filepath.Join(cfg, "jobs")
	if err := VetJobDir(context.Background(), jobs, f.home, f.id); err != nil {
		t.Fatalf("a real config directory outside the home failed the vet: %v", err)
	}
	linked := filepath.Join(f.base, "cfg-link")
	if err := os.Symlink(cfg, linked); err != nil {
		t.Fatal(err)
	}
	ve := vetErr(t, VetJobDir(context.Background(), filepath.Join(linked, "jobs"), f.home, f.id))
	if ve.Path != linked {
		t.Fatalf("vet named %q (%s); want the linked config directory %s", ve.Path, ve.Reason, linked)
	}
}

func TestVetJobDirRefusesGroupOrOtherWrite(t *testing.T) {
	for _, mode := range []os.FileMode{0o720, 0o702, 0o777} {
		for _, level := range []string{"jobs root", "job directory"} {
			f := newVetFixture(t)
			at := f.jobs
			if level == "job directory" {
				at = f.jobDir
			}
			if err := os.Chmod(at, mode); err != nil {
				t.Fatal(err)
			}
			ve := vetErr(t, VetJobDir(context.Background(), f.jobs, f.home, f.id))
			if ve.Path != at || !strings.Contains(ve.Reason, "writable by group or others") {
				t.Errorf("%s at mode %o: vet named %q (%s)", level, mode, ve.Path, ve.Reason)
			}
		}
	}
}

func TestVetJobDirRefusesAJobsRootThatIsNotADirectory(t *testing.T) {
	f := newVetFixture(t)
	if err := os.RemoveAll(f.jobs); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.jobs, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ve := vetErr(t, VetJobDir(context.Background(), f.jobs, f.home, f.id))
	if ve.Path != f.jobs {
		t.Fatalf("vet named %q (%s); want %s", ve.Path, ve.Reason, f.jobs)
	}
}

// statOverride is a FileInfo whose stat result is changed, for the
// other-uid and other-device cases a test host can't make without root.
type statOverride struct {
	fs.FileInfo
	st syscall.Stat_t
}

func (s statOverride) Sys() any { return &s.st }

// vetWithOverride is the default vet whose lstat of path reports change's
// edit of the real stat result.
func vetWithOverride(path string, change func(*syscall.Stat_t)) jobDirVet {
	v := defaultJobDirVet()
	v.lstat = func(p string) (os.FileInfo, error) {
		info, err := os.Lstat(p)
		if err != nil || p != path {
			return info, err
		}
		st := *info.Sys().(*syscall.Stat_t)
		change(&st)
		return statOverride{FileInfo: info, st: st}, nil
	}
	return v
}

func TestVetJobDirKeepsAJobDirectoryOfAnotherUID(t *testing.T) {
	f := newVetFixture(t)
	for _, at := range []string{f.jobs, f.jobDir} {
		v := vetWithOverride(at, func(st *syscall.Stat_t) { st.Uid++ })
		ve := vetErr(t, v.run(context.Background(), f.jobs, f.home, f.id))
		if ve.Path != at || !strings.Contains(ve.Reason, "owned by uid") {
			t.Errorf("another uid at %s: vet named %q (%s)", at, ve.Path, ve.Reason)
		}
	}
}

func TestVetJobDirKeepsAJobDirectoryOnAnotherDevice(t *testing.T) {
	f := newVetFixture(t)
	nested := filepath.Join(f.jobDir, "scratch")
	for _, at := range []string{f.jobDir, nested, filepath.Join(nested, "out")} {
		v := vetWithOverride(at, func(st *syscall.Stat_t) { st.Dev++ })
		ve := vetErr(t, v.run(context.Background(), f.jobs, f.home, f.id))
		if ve.Path != at || ve.Reason != "another filesystem at "+at {
			t.Errorf("another device at %s: vet named %q (%s)", at, ve.Path, ve.Reason)
		}
	}
}

func TestVetJobDirKeepsOnEveryNonRegularEntry(t *testing.T) {
	cases := []struct {
		name string
		mode fs.FileMode
		want string
	}{
		{"named pipe", fs.ModeNamedPipe, "a named pipe"},
		{"socket", fs.ModeSocket, "a socket"},
		{"device", fs.ModeDevice, "a device"},
		{"character device", fs.ModeDevice | fs.ModeCharDevice, "a device"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newVetFixture(t)
			at := filepath.Join(f.jobDir, "scratch", "out")
			v := defaultJobDirVet()
			v.lstat = func(p string) (os.FileInfo, error) {
				info, err := os.Lstat(p)
				if err != nil || p != at {
					return info, err
				}
				return modeOverride{FileInfo: info, mode: c.mode | 0o600}, nil
			}
			ve := vetErr(t, v.run(context.Background(), f.jobs, f.home, f.id))
			if ve.Path != at || ve.Reason != c.want+" at "+at {
				t.Fatalf("vet named %q (%s); want %s at %s", ve.Path, ve.Reason, c.want, at)
			}
		})
	}
}

// modeOverride is a FileInfo reporting another file type.
type modeOverride struct {
	fs.FileInfo
	mode fs.FileMode
}

func (m modeOverride) Mode() fs.FileMode { return m.mode }
func (m modeOverride) IsDir() bool       { return m.mode.IsDir() }

func TestVetJobDirKeepsOnARealLinkAndPipeInside(t *testing.T) {
	f := newVetFixture(t)
	outside := f.outside(t, "outside")
	out := filepath.Join(f.jobDir, "scratch", "out")
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, out); err != nil {
		t.Fatal(err)
	}
	ve := vetErr(t, VetJobDir(context.Background(), f.jobs, f.home, f.id))
	if ve.Path != out || ve.Reason != "a symbolic link at "+out {
		t.Fatalf("vet named %q (%s); want the link at %s", ve.Path, ve.Reason, out)
	}
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(out, 0o600); err != nil {
		t.Fatal(err)
	}
	ve = vetErr(t, VetJobDir(context.Background(), f.jobs, f.home, f.id))
	if ve.Path != out || ve.Reason != "a named pipe at "+out {
		t.Fatalf("vet named %q (%s); want the pipe at %s", ve.Path, ve.Reason, out)
	}
}

func TestVetJobDirNamesTheFirstEntryByNameOnEveryRun(t *testing.T) {
	f := newVetFixture(t)
	outside := f.outside(t, "outside")
	for _, name := range []string{"zz-link", "mm-link", "aa-link"} {
		if err := os.Symlink(outside, filepath.Join(f.jobDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	want := filepath.Join(f.jobDir, "aa-link")
	for i := 0; i < 5; i++ {
		ve := vetErr(t, VetJobDir(context.Background(), f.jobs, f.home, f.id))
		if ve.Path != want {
			t.Fatalf("run %d named %q; want %s", i, ve.Path, want)
		}
	}
}

func TestVetJobDirKeepsOnAFailingReadOrLstat(t *testing.T) {
	f := newVetFixture(t)
	nested := filepath.Join(f.jobDir, "scratch")

	v := defaultJobDirVet()
	v.readNames = func(dir string, want os.FileInfo) ([]string, error) {
		if dir == nested {
			return nil, errors.New("permission denied")
		}
		return vetDirNames(dir, want)
	}
	ve := vetErr(t, v.run(context.Background(), f.jobs, f.home, f.id))
	if ve.Path != nested || !strings.Contains(ve.Reason, "permission denied") {
		t.Fatalf("a failing read: vet named %q (%s)", ve.Path, ve.Reason)
	}

	file := filepath.Join(f.jobDir, "state.json")
	v = defaultJobDirVet()
	v.lstat = func(p string) (os.FileInfo, error) {
		if p == file {
			return nil, errors.New("input/output error")
		}
		return os.Lstat(p)
	}
	ve = vetErr(t, v.run(context.Background(), f.jobs, f.home, f.id))
	if ve.Path != file || !strings.Contains(ve.Reason, "input/output error") {
		t.Fatalf("a failing Lstat: vet named %q (%s)", ve.Path, ve.Reason)
	}

	// A real directory the person can't read.
	if err := os.Chmod(nested, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(nested, 0o700) })
	if os.Getuid() != 0 {
		ve = vetErr(t, VetJobDir(context.Background(), f.jobs, f.home, f.id))
		if ve.Path != nested {
			t.Fatalf("an unreadable directory: vet named %q (%s)", ve.Path, ve.Reason)
		}
	}
}

func TestVetJobDirKeepsWhenItsContextEnds(t *testing.T) {
	f := newVetFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ve := vetErr(t, VetJobDir(ctx, f.jobs, f.home, f.id))
	if !strings.Contains(ve.Reason, "ran out of time") {
		t.Fatalf("an ended context: %s", ve.Reason)
	}

	// A deadline that passes part-way through the walk stops it there.
	v := defaultJobDirVet()
	ctx, cancel = context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	v.lstat = func(p string) (os.FileInfo, error) {
		if strings.HasPrefix(p, f.jobDir+string(filepath.Separator)) {
			cancel()
		}
		return os.Lstat(p)
	}
	ve = vetErr(t, v.run(ctx, f.jobs, f.home, f.id))
	if !strings.Contains(ve.Reason, "ran out of time") {
		t.Fatalf("a deadline mid-walk: %s", ve.Reason)
	}
}

func TestVetJobDirRefusesAJobsRootThatIsNotClean(t *testing.T) {
	f := newVetFixture(t)
	for _, root := range []string{"", "relative/jobs", f.jobs + "/", f.home + "/./.claude/jobs"} {
		if err := VetJobDir(context.Background(), root, f.home, f.id); err == nil {
			t.Errorf("the vet passed jobs root %q", root)
		}
	}
}

func TestJobDirForRefusesIDsThatArePaths(t *testing.T) {
	f := newVetFixture(t)
	for _, id := range []string{"", ".", "..", "../x", "a/b"} {
		if got := JobDirFor(f.jobs, id); got != "" {
			t.Errorf("JobDirFor(%q) = %q", id, got)
		}
	}
	if got := JobDirFor(f.jobs, f.id); got != f.jobDir {
		t.Errorf("JobDirFor(%q) = %q, want %s", f.id, got, f.jobDir)
	}
}

// claudeEnvFixture is a workspace root, a home and a PATH directory holding
// an executable claude.
func claudeEnvFixture(t *testing.T) (root, home, bin string) {
	t.Helper()
	home, root = newScratchFixture(t)
	bin = filepath.Join(filepath.Dir(home), "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, home, bin
}

func TestClaudeCommandRunsFromTheScratchDirectoryWithTheFilteredPATH(t *testing.T) {
	root, home, bin := claudeEnvFixture(t)
	e := NewClaudeEnv(root, home, bin)
	defer e.Close()
	cmd, err := e.ClaudeCommand(context.Background(), "agents", "--json", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != filepath.Join(bin, "claude") {
		t.Errorf("claude started as %q", cmd.Path)
	}
	if cmd.Dir != filepath.Join(e.Scratch.Dir, scratchClaude) {
		t.Errorf("claude runs from %q, want the scratch directory's empty claude directory", cmd.Dir)
	}
	entries, err := os.ReadDir(cmd.Dir)
	if err != nil || len(entries) != 0 {
		t.Errorf("claude's directory is not empty: %v %v", entries, err)
	}
	if !slices.Contains(cmd.Env, "PATH="+bin) {
		t.Errorf("claude's PATH isn't the filtered one: %q", cmd.Env)
	}
	n := 0
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "PATH=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("claude's environment sets PATH %d times", n)
	}
	if !slices.Equal(cmd.Args[1:], []string{"agents", "--json", "--all"}) {
		t.Errorf("claude's arguments are %q", cmd.Args[1:])
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Error("claude doesn't run in its own process group")
	}
}

func TestClaudeCommandRefusesTheOverrideFlags(t *testing.T) {
	root, home, bin := claudeEnvFixture(t)
	e := NewClaudeEnv(root, home, bin)
	defer e.Close()
	for _, flag := range []string{"--discard-unpushed", "--force-remove-worktree", "--force-remove-worktree=true"} {
		if _, err := e.ClaudeCommand(context.Background(), "rm", flag, "abcdef12"); err == nil {
			t.Errorf("a claude call with %s was built", flag)
		}
	}
}

func TestClaudeCommandRefusesAClaudeInsideTheWorkspaceAndABadScratchDirectory(t *testing.T) {
	root, home, _ := claudeEnvFixture(t)
	inside := filepath.Join(root, "bin")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inside, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := NewClaudeEnv(root, home, inside)
	defer e.Close()
	if _, err := e.ClaudeCommand(context.Background(), "agents"); err == nil {
		t.Fatal("a claude inside the workspace root was used")
	}

	root, home, bin := claudeEnvFixture(t)
	e = NewClaudeEnv(root, home, bin)
	defer e.Close()
	if err := os.WriteFile(filepath.Join(e.Scratch.Dir, scratchClaude, "settings.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ClaudeCommand(context.Background(), "agents"); err == nil {
		t.Fatal("a claude call was built from a scratch directory that isn't empty")
	}
	if err := os.Chmod(e.Scratch.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ClaudeCommand(context.Background(), "agents"); err == nil {
		t.Fatal("a claude call was built from a scratch directory with a wrong mode")
	}
}

func TestNewScratchMakesThePrivateClaudeDirectory(t *testing.T) {
	home, root := newScratchFixture(t)
	s, err := NewScratch(home, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Remove()
	info, err := os.Lstat(filepath.Join(s.Dir, scratchClaude))
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("the claude directory is %v", info.Mode())
	}
}
