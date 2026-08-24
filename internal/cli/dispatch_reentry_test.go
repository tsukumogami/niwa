package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// Every expectation in this file is written here, never read from launchSpecs.
// That is the point of the fixtures: a suite that derives what it wants from
// the same table production reads passes just as happily when the table is
// wrong, which is how a previous change in this area stayed green while the
// production binary name was mutated underneath it.

const reentryTestDir = "/tmp/inst dir"

// grantSpec declares a workdir grant shaped nothing like any real agent's, so a
// test asserting on it cannot be satisfied by code that reached for the real
// table instead of the spec it was handed.
func grantSpec() agentplan.LaunchSpec {
	return agentplan.LaunchSpec{
		Binary:           "invented-agent",
		ResumeArgs:       []string{"reopen", "--by-id"},
		HintVerbs:        []string{"reopen", "tail", "halt"},
		WorkdirGrantArgs: []string{"--vouch", `dir=%q,level="full"`},
	}
}

// grantlessSpec declares no grant at all: the shape that must come out exactly
// as it did before this file existed.
func grantlessSpec() agentplan.LaunchSpec {
	return agentplan.LaunchSpec{
		Binary:     "plain-agent",
		ResumeArgs: []string{"attach"},
		HintVerbs:  []string{"attach", "logs"},
	}
}

func eq(t *testing.T, got, want []string, what string) {
	t.Helper()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("%s =\n  %#v\nwant\n  %#v", what, got, want)
	}
}

func TestReentryArgsCarriesTheDeclaredGrant(t *testing.T) {
	eq(t, reentryArgs(grantSpec(), "sess-1", reentryTestDir),
		[]string{"reopen", "--by-id", "--vouch", `dir="/tmp/inst dir",level="full"`, "sess-1"},
		"reentryArgs with a grant")
}

func TestReentryArgsFollowsTheDeclarationWhenItChanges(t *testing.T) {
	// The grant is read from the declaration rather than known here: change it
	// in the spec alone and the argv changes with it. A second table on the
	// re-entry path -- the drift this guards against -- would not.
	spec := grantSpec()
	spec.WorkdirGrantArgs = []string{"-c", "trusted=%q"}
	eq(t, reentryArgs(spec, "sess-1", "/w"),
		[]string{"reopen", "--by-id", "-c", `trusted="/w"`, "sess-1"},
		"reentryArgs after the declaration changed")
}

func TestReentryArgsWithoutAGrantIsUnchanged(t *testing.T) {
	eq(t, reentryArgs(grantlessSpec(), "job-7", reentryTestDir),
		[]string{"attach", "job-7"},
		"reentryArgs for an agent declaring no grant")
}

func TestReentryArgsWithoutAnInstanceDirectoryCarriesNoGrant(t *testing.T) {
	// The degraded case is no grant, never a grant naming nothing.
	got := reentryArgs(grantSpec(), "sess-1", "")
	eq(t, got, []string{"reopen", "--by-id", "sess-1"}, "reentryArgs with no workdir")
	for _, a := range got {
		if strings.Contains(a, "dir=") {
			t.Fatalf("a grant was built from an empty working directory: %#v", got)
		}
	}
}

func TestReentryArgsRefusesAnAgentWithNoWayBackIn(t *testing.T) {
	spec := grantSpec()
	spec.ResumeArgs = nil
	if got := reentryArgs(spec, "sess-1", reentryTestDir); got != nil {
		t.Errorf("reentryArgs = %#v, want nil for a declaration naming no resume verb", got)
	}
}

func TestShellTokenQuotesWhatAShellWouldReinterpret(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"codex", "codex"},
		{"01a0-beef", "01a0-beef"},
		{"/abs/path.d/x_1", "/abs/path.d/x_1"},
		{"a@b%c+d=e:f,g", "a@b%c+d=e:f,g"},
		{"", "''"},
		{"has space", "'has space'"},
		{`projects={"/d"={trust_level="trusted"}}`, `'projects={"/d"={trust_level="trusted"}}'`},
		{"semi;colon", "'semi;colon'"},
		{"dollar$sub", "'dollar$sub'"},
		{"back`tick`", "'back`tick`'"},
		// Neither is dangerous; both would silently hand the binary something
		// other than what was printed, which is the same failure.
		{"~/home", "'~/home'"},
		{"bang!", "'bang!'"},
		{"it's", `'it'\''s'`},
	} {
		if got := shellToken(tc.in); got != tc.want {
			t.Errorf("shellToken(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPrintableTokenRefusesWhatRedrawsTheLine(t *testing.T) {
	for _, ok := range []string{"codex", "/a/b c", `x={"y"}`} {
		if !printableToken(ok) {
			t.Errorf("printableToken(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"a\rb", "a\nb", "a\x1b[2Kb", "a\x07b"} {
		if printableToken(bad) {
			t.Errorf("printableToken(%q) = true, want false", bad)
		}
	}
}

func TestReentryCommandFailsClosed(t *testing.T) {
	spec := grantSpec()
	for _, tc := range []struct {
		name   string
		mutate func(*agentplan.LaunchSpec)
		handle string
		dir    string
	}{
		{"no binary", func(s *agentplan.LaunchSpec) { s.Binary = "" }, "sess-1", reentryTestDir},
		{"no resume verb", func(s *agentplan.LaunchSpec) { s.ResumeArgs = nil }, "sess-1", reentryTestDir},
		{"unsafe handle", func(*agentplan.LaunchSpec) {}, "sess 1; rm -rf /", reentryTestDir},
		{"empty handle", func(*agentplan.LaunchSpec) {}, "", reentryTestDir},
		{"escape in the declaration", func(s *agentplan.LaunchSpec) {
			s.ResumeArgs = []string{"reopen", "--mode=\x1b[2K"}
		}, "sess-1", reentryTestDir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := spec
			tc.mutate(&s)
			if got := reentryCommand(s, tc.handle, tc.dir); got != "" {
				t.Errorf("reentryCommand = %q, want \"\"", got)
			}
		})
	}
}

// TestGrantRendersAControlByteInThePathAsText records why the print gate does
// not fire on a hostile instance directory: the declaration's own quoting verb
// renders the path as a quoted string, so a control byte in it arrives on the
// line as the four characters \x1b rather than as an escape the terminal acts
// on. The gate still stands, for tokens that reach it unquoted -- the binary
// name and the declaration's own arguments -- but the path is neutralised
// before it gets there, and that is worth pinning rather than assuming.
func TestGrantRendersAControlByteInThePathAsText(t *testing.T) {
	got := reentryCommand(grantSpec(), "sess-1", "/tmp/\x1b[2Kevil")
	if got == "" {
		t.Fatal("the command was refused; the path should have been rendered as text, not rejected")
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("a raw escape byte reached the printed command: %q", got)
	}
	if !strings.Contains(got, `\x1b[2Kevil`) {
		t.Errorf("the escape was not rendered as text: %q", got)
	}
}

func TestReentryHintsGrantOnlyTheResumeLine(t *testing.T) {
	// The load-bearing fixture. The real table cannot fail on this: the one
	// agent that declares a grant declares exactly one hint verb, so "every
	// hint verb carries the grant" and "only the resume verb does" produce
	// identical output there. Three verbs and a grant tell them apart.
	got := reentryHints(grantSpec(), "sess-1", reentryTestDir)
	want := []string{
		`invented-agent reopen --by-id --vouch 'dir="/tmp/inst dir",level="full"' sess-1`,
		"invented-agent tail sess-1",
		"invented-agent halt sess-1",
	}
	eq(t, got, want, "reentryHints")
}

func TestReentryHintsWithoutAGrantAreUnchanged(t *testing.T) {
	eq(t, reentryHints(grantlessSpec(), "job-7", reentryTestDir),
		[]string{"plain-agent attach job-7", "plain-agent logs job-7"},
		"reentryHints for an agent declaring no grant")
}

// recordArgv runs one printed command the way a developer would -- through a
// POSIX shell -- against a stub standing in for the agent's binary, and returns
// the argv the stub actually received.
//
// This is what makes the quoting claim a measurement rather than an assertion.
// Comparing the string niwa printed against the string niwa built would pass
// even with the quoter inverted, because both sides would be wrong together.
func recordArgv(t *testing.T, binary, command string) []string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "argv")
	stub := filepath.Join(dir, binary)
	script := "#!/bin/sh\n: > " + out + "\nfor a in \"$@\"; do printf '%s\\0' \"$a\" >> " + out + "; done\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("running %q: %v\n%s", command, err, b)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	if len(got) == 1 && got[0] == "" {
		return nil
	}
	return got
}

func TestPrintedCommandsSurviveAPosixShell(t *testing.T) {
	spec := grantSpec()
	wantResume := []string{"reopen", "--by-id", "--vouch", `dir="/tmp/inst dir",level="full"`, "sess-1"}

	eq(t, recordArgv(t, spec.Binary, reentryCommand(spec, "sess-1", reentryTestDir)),
		wantResume, "argv the shell handed the binary for reentryCommand")

	for i, line := range reentryHints(spec, "sess-1", reentryTestDir) {
		want := [][]string{
			wantResume,
			{"tail", "sess-1"},
			{"halt", "sess-1"},
		}[i]
		eq(t, recordArgv(t, spec.Binary, line), want, "argv for hint line "+line)
	}
}
