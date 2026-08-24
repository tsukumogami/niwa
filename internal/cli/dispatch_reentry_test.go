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
		Binary:     "invented-agent",
		ResumeArgs: []string{"reopen", "--by-id"},
		// The resume verb is deliberately NOT first. With it first, an
		// implementation that just grants line zero -- never reading
		// ResumeArgs at all -- passes every assertion below, and both real
		// agents also declare their resume verb first, so the production
		// table could never catch it either.
		HintVerbs:        []string{"tail", "reopen", "halt"},
		WorkdirGrantArgs: []string{"--vouch", `dir=%q,level="full"`},
	}
}

// grantlessSpec declares no grant at all: the shape that must come out exactly
// as it did before this file existed.
func grantlessSpec() agentplan.LaunchSpec {
	return agentplan.LaunchSpec{
		Binary:     "plain-agent",
		ResumeArgs: []string{"attach"},
		HintVerbs:  []string{"logs", "attach"},
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
	eq(t, reentryArgs(grantSpec(), "sess-1", ""),
		[]string{"reopen", "--by-id", "sess-1"},
		"reentryArgs with no workdir")
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

// wantBareBytes is the set of bytes shellToken may leave unquoted, written out
// here rather than read from the production constant.
//
// That distinction is the whole value of this test. An earlier version derived
// the expectation from shellSafeToken itself, which meant widening the constant
// widened the expectation with it: adding `*` -- so a printed path would then
// glob -- passed. A literal is what makes the sweep able to fail.
const wantBareBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-"

// TestShellTokenAllowlistIsExact walks every printable ASCII byte and requires
// bare output exactly when the byte is one this test says is safe. The
// hand-written table above covers the shapes a reader cares about; this covers
// the ones nobody thought to write down.
func TestShellTokenAllowlistIsExact(t *testing.T) {
	for b := byte(0x20); b <= 0x7e; b++ {
		in := string(b)
		bare := shellToken(in) == in
		safe := strings.ContainsRune(wantBareBytes, rune(b))
		if bare != safe {
			t.Errorf("shellToken(%q): passed through bare = %v, want %v", in, bare, safe)
		}
	}
}

// TestReentryHintsRefuseWhenNoVerbResumes covers the shape where no hint verb
// matches the resume verb. Every line is then a plain management hint carrying
// the handle, and without a gate on each line niwa would print an unvalidated
// handle it never checked.
func TestReentryHintsRefuseWhenNoVerbResumes(t *testing.T) {
	spec := grantSpec()
	spec.HintVerbs = []string{"tail", "halt"}
	eq(t, reentryHints(spec, "sess-1", reentryTestDir),
		[]string{"invented-agent tail sess-1", "invented-agent halt sess-1"},
		"reentryHints with no verb matching the resume verb")

	if got := reentryHints(spec, "sess 1; rm -rf /", reentryTestDir); got != nil {
		t.Errorf("reentryHints = %#v, want nil for an unsafe handle", got)
	}
	spec.ResumeArgs = nil
	if got := reentryHints(spec, "sess-1", reentryTestDir); got != nil {
		t.Errorf("reentryHints = %#v, want nil when the declaration names no resume verb", got)
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
		{"carriage return in the declaration", func(s *agentplan.LaunchSpec) {
			s.ResumeArgs = []string{"reopen", "--mode=a\rb"}
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
		"invented-agent tail sess-1",
		`invented-agent reopen --by-id --vouch 'dir="/tmp/inst dir",level="full"' sess-1`,
		"invented-agent halt sess-1",
	}
	if len(got) != len(want) {
		t.Fatalf("reentryHints returned %d lines, want %d: %#v", len(got), len(want), got)
	}
	eq(t, got, want, "reentryHints")
}

func TestReentryHintsWithoutAGrantAreUnchanged(t *testing.T) {
	eq(t, reentryHints(grantlessSpec(), "job-7", reentryTestDir),
		[]string{"plain-agent logs job-7", "plain-agent attach job-7"},
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

	hints := reentryHints(spec, "sess-1", reentryTestDir)
	// Pinned: a renderer that returned only the resume line would otherwise
	// satisfy every per-line assertion below by never reaching them.
	if len(hints) != 3 {
		t.Fatalf("reentryHints returned %d lines, want 3: %#v", len(hints), hints)
	}
	for i, line := range hints {
		want := [][]string{
			{"tail", "sess-1"},
			wantResume,
			{"halt", "sess-1"},
		}[i]
		eq(t, recordArgv(t, spec.Binary, line), want, "argv for hint line "+line)
	}

	// The hardest tokens go through a real shell rather than being compared as
	// strings: an embedded single quote, a dollar, and a backtick are where a
	// quoter that looks right on paper comes apart.
	for _, dir := range []string{`/tmp/it's dir`, `/tmp/$HOME dir`, "/tmp/`id` dir"} {
		s2 := spec
		s2.WorkdirGrantArgs = []string{"--vouch", "dir=%s"}
		cmd := reentryCommand(s2, "sess-1", dir)
		eq(t, recordArgv(t, s2.Binary, cmd),
			[]string{"reopen", "--by-id", "--vouch", "dir=" + dir, "sess-1"},
			"argv for a directory containing "+dir)
	}
}
