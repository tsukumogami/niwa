package removal

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestGitCommandEnvironmentAndArguments(t *testing.T) {
	e := &Env{Root: "/ws", Home: "/home/dana", PATH: "/usr/bin:/bin", Git: "/usr/bin/git"}
	r := repoDirs{GitDir: "/ws/inst/tools/app/.git", WorkTree: "/ws/inst/tools/app"}
	cmd, err := e.gitCommand(context.Background(), r, callOpts{excludesUnset: true}, "status", "--porcelain=v2", "-z")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != "/usr/bin/git" {
		t.Errorf("git started as %q, want the absolute path", cmd.Path)
	}
	wantEnv := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/dana",
		"LANG=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"GIT_ALLOW_PROTOCOL=https:ssh",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ForwardAgent=no",
		"GIT_PAGER=cat",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_GRAFT_FILE=/dev/null",
		"GIT_CEILING_DIRECTORIES=/ws",
	}
	if !reflect.DeepEqual(cmd.Env, wantEnv) {
		t.Errorf("environment:\n got %q\nwant %q", cmd.Env, wantEnv)
	}
	wantArgs := []string{"/usr/bin/git", "--no-pager",
		"--git-dir=/ws/inst/tools/app/.git", "--work-tree=/ws/inst/tools/app",
		"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "core.pager=cat",
		"-c", "credential.helper=", "-c", "submodule.recurse=false", "-c", "fetch.recurseSubmodules=false",
		"-c", "core.useReplaceRefs=false", "-c", "core.commitGraph=false",
		"-c", "log.showSignature=false", "-c", "status.showUntrackedFiles=all", "-c", "core.untrackedCache=false",
		"-c", "http.proxy=", "-c", "http.sslVerify=true",
		"-c", "core.trustctime=true", "-c", "core.checkStat=default",
		"-c", "core.excludesFile=",
		"status", "--porcelain=v2", "-z", "--ignore-submodules=all", "--untracked-files=all",
	}
	if !reflect.DeepEqual(cmd.Args, wantArgs) {
		t.Errorf("arguments:\n got %q\nwant %q", cmd.Args, wantArgs)
	}
	if cmd.Dir != r.WorkTree {
		t.Errorf("runs from %q, want the work tree", cmd.Dir)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Error("git does not run in its own process group")
	}
}

func TestGitCommandOptionalArguments(t *testing.T) {
	e := &Env{Root: "/ws", Home: "/h", PATH: "/usr/bin", Git: "/usr/bin/git"}
	r := repoDirs{GitDir: "/g", WorkTree: "/w"}
	cmd, err := e.gitCommand(context.Background(), r, callOpts{}, "stash", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Args, "--no-show-signature") {
		t.Errorf("stash list without --no-show-signature: %q", cmd.Args)
	}
	if slices.Contains(cmd.Args, "core.excludesFile=") {
		t.Errorf("excludes override added for a repository that names an excludes file: %q", cmd.Args)
	}
	cmd, err = e.gitCommand(context.Background(), r, callOpts{}, "for-each-ref")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(cmd.Args, "--ignore-submodules=all") || slices.Contains(cmd.Args, "--no-show-signature") {
		t.Errorf("status or log flags on for-each-ref: %q", cmd.Args)
	}

	if _, err := e.gitCommand(context.Background(), repoDirs{}, callOpts{}, "ls-remote", "x"); err == nil {
		t.Error("a call in no repository built without a directory to run from")
	}
	if _, err := (&Env{}).gitCommand(context.Background(), r, callOpts{}, "status"); err == nil {
		t.Error("a call built with no git resolved")
	}

	e.allowFile = true
	e.Scratch = &Scratch{Dir: "/home/.niwa/w-x/scratch/p"}
	cmd, err = e.gitCommand(context.Background(), repoDirs{}, callOpts{dir: "/d"}, "ls-remote", "x")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Env, "GIT_ALLOW_PROTOCOL=https:ssh:file") {
		t.Errorf("file remotes not admitted when asked: %q", cmd.Env)
	}
	if !slices.Contains(cmd.Env, "GIT_CEILING_DIRECTORIES=/ws:/home/.niwa/w-x/scratch/p") {
		t.Errorf("ceiling does not stop discovery at the scratch directory: %q", cmd.Env)
	}
	for _, a := range cmd.Args {
		if strings.HasPrefix(a, "--git-dir") || strings.HasPrefix(a, "--work-tree") {
			t.Errorf("a call in no repository names one: %q", cmd.Args)
		}
	}
}

func TestFilterPATH(t *testing.T) {
	base := t.TempDir()
	base, _ = filepath.EvalSymlinks(base)
	root := filepath.Join(base, "ws")
	inRoot := filepath.Join(root, "bin")
	outside := filepath.Join(base, "bin")
	for _, d := range []string{inRoot, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	linkIntoRoot := filepath.Join(base, "sneaky")
	if err := os.Symlink(inRoot, linkIntoRoot); err != nil {
		t.Fatal(err)
	}
	linkOutside := filepath.Join(base, "alias")
	if err := os.Symlink(outside, linkOutside); err != nil {
		t.Fatal(err)
	}
	pathEnv := strings.Join([]string{"", "relative/bin", ".", inRoot, linkIntoRoot, filepath.Join(base, "missing"), outside, linkOutside}, ":")
	got := filterPATH(pathEnv, root)
	want := []string{outside}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterPATH = %q, want %q", got, want)
	}
}

func TestLookInRefusesALinkIntoTheRoot(t *testing.T) {
	base := t.TempDir()
	base, _ = filepath.EvalSymlinks(base)
	root := filepath.Join(base, "ws")
	bin := filepath.Join(base, "bin")
	for _, d := range []string{root, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	planted := filepath.Join(root, "git")
	if err := os.WriteFile(planted, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(planted, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	if got := lookIn([]string{bin}, "git", root); got != "" {
		t.Fatalf("lookIn found %q, a link into the workspace root", got)
	}
	if err := os.WriteFile(filepath.Join(bin, "lsof"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lookIn([]string{bin}, "lsof", root); got != "" {
		t.Fatalf("lookIn found %q, which isn't executable", got)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := lookIn([]string{bin}, "claude", root), filepath.Join(bin, "claude"); got != want {
		t.Fatalf("lookIn = %q, want %q", got, want)
	}
}

func TestNewEnvResolvesProgramsFromTheFilteredPATH(t *testing.T) {
	base := t.TempDir()
	base, _ = filepath.EvalSymlinks(base)
	root := filepath.Join(base, "ws")
	bin := filepath.Join(base, "bin")
	rootBin := filepath.Join(root, "bin")
	for _, d := range []string{rootBin, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"git", "lsof", "claude"} {
		for _, d := range []string{rootBin, bin} {
			if err := os.WriteFile(filepath.Join(d, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	home := filepath.Join(base, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(root, home, rootBin+"::"+bin)
	defer e.Close()
	if e.Git != filepath.Join(bin, "git") || e.Lsof != filepath.Join(bin, "lsof") || e.Claude != filepath.Join(bin, "claude") {
		t.Fatalf("resolved git=%q lsof=%q claude=%q, want each from %s", e.Git, e.Lsof, e.Claude, bin)
	}
	if e.PATH != bin {
		t.Fatalf("PATH = %q, want %q", e.PATH, bin)
	}
}

func TestNamesProgram(t *testing.T) {
	cases := []struct {
		key, value string
		want       bool
	}{
		{"filter.x.clean", "cat", true},
		{"filter.lfs.smudge", "x", true},
		{"filter.x.process", "x", true},
		{"filter.x.required", "true", false},
		{"diff.external", "x", true},
		{"diff.x.textconv", "x", true},
		{"diff.x.command", "x", true},
		{"merge.x.driver", "x", true},
		{"credential.helper", "store", true},
		{"credential.https://host.helper", "x", true},
		{"core.fsmonitor", "true", false},
		{"core.fsmonitor", "false", false},
		{"core.fsmonitor", "/tmp/hook.sh", true},
		{"sequence.editor", "x", false},
		{"remote.origin.url", "x", false},
		{"core.bare", "false", false},
		{"user.name", "x", false},
	}
	for _, c := range cases {
		if got := namesProgram(c.key, c.value); got != c.want {
			t.Errorf("namesProgram(%q, %q) = %v, want %v", c.key, c.value, got, c.want)
		}
	}
}

func TestParseConfigList(t *testing.T) {
	out := []byte("local\x00core.bare\nfalse\x00local\x00filter.x.clean\ncat\x00command\x00credential.helper\n\x00local\x00some.flag\x00")
	got, err := parseConfigList(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []configEntry{
		{"local", "core.bare", "false"},
		{"local", "filter.x.clean", "cat"},
		{"command", "credential.helper", ""},
		{"local", "some.flag", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseConfigList = %+v, want %+v", got, want)
	}
	for _, bad := range []string{
		"local\x00core.bare\nfalse",              // not NUL-terminated
		"local\x00core.bare\nfalse\x00local\x00", // odd fields
		"weird\x00core.bare\nfalse\x00",          // unknown scope
		"local\x00\nfalse\x00",                   // empty key
	} {
		if _, err := parseConfigList([]byte(bad)); err == nil {
			t.Errorf("parseConfigList(%q) parsed", bad)
		}
	}
}

func TestParseRefs(t *testing.T) {
	a := strings.Repeat("a", 40)
	b := strings.Repeat("b", 64)
	got, err := parseRefs([]byte(a + "\x00refs/heads/main\x00\n" + b + "\x00refs/tags/v1\x00\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []ref{{a, "refs/heads/main"}, {b, "refs/tags/v1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseRefs = %+v, want %+v", got, want)
	}
	for _, bad := range []string{
		a + "\x00refs/heads/main\n",     // no terminator
		"xyz\x00refs/heads/main\x00\n",  // not an object id
		a + "\x00HEAD\x00\n",            // not under refs/
		a + "refs/heads/main\x00\n",     // no separator
		a + "\x00refs/heads/a\nb\x00\n", // a newline in the name
	} {
		if _, err := parseRefs([]byte(bad)); err == nil {
			t.Errorf("parseRefs(%q) parsed", bad)
		}
	}
}

func TestParseStatus(t *testing.T) {
	out := "1 .M N... 100644 100644 100644 " + strings.Repeat("a", 40) + " " + strings.Repeat("a", 40) + " src/a b.go\x00" +
		"2 R. N... 100644 100644 100644 " + strings.Repeat("a", 40) + " " + strings.Repeat("a", 40) + " R100 new.go\x00old.go\x00" +
		"? notes\ntxt\x00"
	got, err := parseStatus([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := []statusEntry{{"changed", "src/a b.go"}, {"renamed", "new.go"}, {"untracked", "notes\ntxt"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseStatus = %+v, want %+v", got, want)
	}
	for _, bad := range []string{
		"? notes.txt",                         // no terminator
		"x notes.txt\x00",                     // unknown kind
		"1 .M N...\x00",                       // too few fields
		"2 R. N... 1 1 1 a a R100 new.go\x00", // rename with no original
	} {
		if _, err := parseStatus([]byte(bad)); err == nil {
			t.Errorf("parseStatus(%q) parsed", bad)
		}
	}
}

func TestParseLsRemote(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	got, err := parseLsRemote([]byte(a + "\tHEAD\n" + b + "\trefs/tags/v1^{}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !got[a] || !got[b] || len(got) != 2 {
		t.Fatalf("parseLsRemote = %v", got)
	}
	if _, err := parseLsRemote([]byte("garbage\n")); err == nil {
		t.Error("parseLsRemote parsed garbage")
	}
}
