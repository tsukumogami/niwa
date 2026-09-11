package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The explanation, written out here rather than rebuilt from the constants,
// the way TestInboundLinesExactText pins the other three stderr lines. This is
// the whole point of the duplication: expectations derived from
// inboundExplanationLine would reword themselves along with a reworded
// constant, and every test in this file would keep passing while the paragraph
// said something else.
const (
	explanationGuideURL = "https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md"

	explanationBodyText = `niwa dispatch: note: accepting messages without asking is inbound only. A message this worker sends into a session launched without it, such as a coordinator dispatched earlier, one dispatched with the behavior off, or one another tool started, still waits for approval there when the two run in different permission modes; dispatching that session again with the behavior on clears it. Your own interactive Claude Code sessions are one such case, and they are governed by your Claude Code user settings, which niwa doesn't change. To accept there too, set "Messages from your other sessions" to accept in Claude Code's /config, or add "crossSessionInbound": "accept" to ~/.claude/settings.json. That change applies to every Claude Code session you run and to messages from any session able to reach yours, on this machine or elsewhere.`

	terminalClose    = "niwa won't show this again; it's also at " + explanationGuideURL
	nonTerminalClose = "niwa will show this again until it's been shown at a terminal; it's also at " + explanationGuideURL

	explanationTerminalLine    = explanationBodyText + " " + terminalClose
	explanationNonTerminalLine = explanationBodyText + " " + nonTerminalClose
)

// explanationMarker is a substring no other niwa output carries. Tests asserting
// the paragraph is ABSENT match on it rather than on a whole line, so that a
// reworded explanation still counts as printed and the absence assertion stays
// strict. Tests asserting it is present match on the whole line instead.
const explanationMarker = "accepting messages without asking is inbound only"

// TestInboundExplanationExactText pins the paragraph and the marker file name,
// both of which are contracts beyond this package: the guide quotes the text,
// and the functional scenarios look the marker up by name.
func TestInboundExplanationExactText(t *testing.T) {
	if inboundNoticeMarker != "accept-session-messages-notice" {
		t.Errorf("inboundNoticeMarker = %q, want %q; the name is what suppresses the notice and what the guide tells a developer to delete",
			inboundNoticeMarker, "accept-session-messages-notice")
	}
	if got := inboundExplanationLine(true); got != explanationTerminalLine {
		t.Errorf("terminal explanation =\n%q\nwant\n%q", got, explanationTerminalLine)
	}
	if got := inboundExplanationLine(false); got != explanationNonTerminalLine {
		t.Errorf("non-terminal explanation =\n%q\nwant\n%q", got, explanationNonTerminalLine)
	}
}

// alwaysTTY and neverTTY are the two isTTY stubs the helper tests pass.
func alwaysTTY() bool { return true }
func neverTTY() bool  { return false }

// markerIn returns the marker path inside dir.
func markerIn(dir string) string { return filepath.Join(dir, inboundNoticeMarker) }

// requireMarker fails unless the marker is a regular, empty file at mode 0o600.
func requireMarker(t *testing.T, dir string) {
	t.Helper()
	info, err := os.Lstat(markerIn(dir))
	if err != nil {
		t.Fatalf("marker missing: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("marker mode = %v, want a regular file", info.Mode())
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("marker permissions = %#o, want 0600", perm)
	}
	if info.Size() != 0 {
		t.Errorf("marker size = %d, want 0; the name is the whole record", info.Size())
	}
}

// requireNoMarker fails when anything exists at the marker path.
func requireNoMarker(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(markerIn(dir)); err == nil {
		t.Fatalf("marker exists at %s, want nothing", markerIn(dir))
	}
}

// skipAsRoot skips a test whose subject is a permission the superuser ignores.
func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permissions this test is about")
	}
}

// showTo runs the helper against dir and returns what it wrote.
func showTo(t *testing.T, dir string, dirErr error, isTTY func() bool) string {
	t.Helper()
	var buf bytes.Buffer
	showInboundExplanation(&buf, dir, dirErr, isTTY)
	return buf.String()
}

// TestInboundExplanationText pins the paragraph the design settled. Each
// fragment is a promise about what a developer is told, so a rewording that
// drops one is a change to the command's documented behavior, not a refactor.
func TestInboundExplanationText(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		line := showTo(t, t.TempDir(), nil, func() bool { return terminal })

		if !strings.HasPrefix(line, "niwa dispatch: note: ") {
			t.Errorf("terminal=%v: line does not start with the note prefix:\n%s", terminal, line)
		}
		if !strings.HasSuffix(line, "\n") {
			t.Errorf("terminal=%v: line does not end in a newline:\n%q", terminal, line)
		}
		if n := strings.Count(strings.TrimSuffix(line, "\n"), "\n"); n != 0 {
			t.Errorf("terminal=%v: explanation spans %d extra lines, want one line:\n%s", terminal, n, line)
		}

		for _, want := range []string{
			"accepting messages without asking is inbound only",
			"dispatching that session again with the behavior on clears it",
			"Your own interactive Claude Code sessions are one such case, and they are governed by your Claude Code user settings, which niwa doesn't change",
			`"Messages from your other sessions"`,
			`"crossSessionInbound": "accept"`,
			"~/.claude/settings.json",
			"That change applies to every Claude Code session you run and to messages from any session able to reach yours, on this machine or elsewhere",
			explanationGuideURL,
		} {
			if !strings.Contains(line, want) {
				t.Errorf("terminal=%v: explanation is missing %q:\n%s", terminal, want, line)
			}
		}

		hasTerminal := strings.Contains(line, terminalClose)
		hasNonTerminal := strings.Contains(line, nonTerminalClose)
		if hasTerminal == hasNonTerminal {
			t.Errorf("terminal=%v: want exactly one closing sentence, got terminal=%v non-terminal=%v:\n%s",
				terminal, hasTerminal, hasNonTerminal, line)
		}
		if hasTerminal != terminal {
			t.Errorf("terminal=%v: closing sentence is the wrong one:\n%s", terminal, line)
		}
	}
}

// TestShowInboundExplanation_TerminalRemembers: at a terminal the helper prints
// once and writes the marker, and the next call is silent. This is the whole
// point of the feature -- a paragraph a developer reads once.
func TestShowInboundExplanation_TerminalRemembers(t *testing.T) {
	dir := t.TempDir()

	first := showTo(t, dir, nil, alwaysTTY)
	if !strings.Contains(first, terminalClose) {
		t.Fatalf("first call did not print the terminal closing sentence:\n%s", first)
	}
	requireMarker(t, dir)

	if second := showTo(t, dir, nil, alwaysTTY); second != "" {
		t.Errorf("second call printed %q, want nothing once the marker exists", second)
	}
}

// TestShowInboundExplanation_NonTerminalDoesNotRemember: with nobody reading
// stderr the paragraph does not count as shown, so nothing is written down and
// it comes back on every call until a terminal finally sees it.
func TestShowInboundExplanation_NonTerminalDoesNotRemember(t *testing.T) {
	dir := t.TempDir()

	first := showTo(t, dir, nil, neverTTY)
	if !strings.Contains(first, nonTerminalClose) {
		t.Fatalf("non-terminal call did not print the non-terminal closing sentence:\n%s", first)
	}
	requireNoMarker(t, dir)

	if second := showTo(t, dir, nil, neverTTY); !strings.Contains(second, nonTerminalClose) {
		t.Errorf("second non-terminal call printed %q, want the explanation again", second)
	}
	requireNoMarker(t, dir)

	third := showTo(t, dir, nil, alwaysTTY)
	if !strings.Contains(third, terminalClose) {
		t.Errorf("the terminal call after two non-terminal ones printed %q, want the terminal version", third)
	}
	requireMarker(t, dir)
}

// TestShowInboundExplanation_ExistingMarkerSilences: the marker is the record,
// whoever wrote it. A developer who creates it by hand never sees the
// paragraph.
func TestShowInboundExplanation_ExistingMarkerSilences(t *testing.T) {
	for _, tc := range []struct {
		name  string
		isTTY func() bool
	}{
		{"at a terminal", alwaysTTY},
		{"not at a terminal", neverTTY},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(markerIn(dir), []byte("placed by hand"), 0o644); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(markerIn(dir))
			if err != nil {
				t.Fatal(err)
			}

			if out := showTo(t, dir, nil, tc.isTTY); out != "" {
				t.Errorf("printed %q with a marker already present, want nothing", out)
			}
			after, err := os.ReadFile(markerIn(dir))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("marker contents changed from %q to %q; the helper never writes through an existing marker", before, after)
			}
		})
	}
}

// TestShowInboundExplanation_SymlinkMarker: presence is os.Lstat, so a symlink
// at the marker path counts as the record whether or not it resolves, and the
// helper never follows it. A dangling link that counted as absent would make an
// exclusive create fail every time, printing the paragraph forever; one written
// through would clobber whatever the link points at.
func TestShowInboundExplanation_SymlinkMarker(t *testing.T) {
	t.Run("dangling", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "nowhere")
		if err := os.Symlink(target, markerIn(dir)); err != nil {
			t.Fatal(err)
		}

		if out := showTo(t, dir, nil, alwaysTTY); out != "" {
			t.Errorf("printed %q for a dangling symlink marker, want nothing", out)
		}
		if _, err := os.Lstat(target); err == nil {
			t.Error("the helper created the symlink's target; it must never write through the marker path")
		}
	})

	t.Run("pointing at a file", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "somewhere")
		if err := os.WriteFile(target, []byte("do not touch"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, markerIn(dir)); err != nil {
			t.Fatal(err)
		}

		if out := showTo(t, dir, nil, alwaysTTY); out != "" {
			t.Errorf("printed %q for a symlink marker, want nothing", out)
		}
		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "do not touch" {
			t.Errorf("the symlink's target now reads %q; the helper wrote through the link", got)
		}
	})
}

// TestShowInboundExplanation_MissingDirectory: the configuration directory need
// not exist yet. A developer who has never run `niwa config set` still gets the
// paragraph once, and the directory niwa creates for it is the one the config
// writer would have made.
func TestShowInboundExplanation_MissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not", "there")

	out := showTo(t, dir, nil, alwaysTTY)
	if !strings.Contains(out, terminalClose) {
		t.Fatalf("printed %q, want the terminal explanation", out)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("the directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", dir)
	}
	requireMarker(t, dir)
}

// TestShowInboundExplanation_UnwritableDirectory: a marker niwa cannot write
// costs one repeated paragraph, never a failure. Nothing here returns an error
// to fail a dispatch with, and the explanation comes back next time.
func TestShowInboundExplanation_UnwritableDirectory(t *testing.T) {
	skipAsRoot(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	first := showTo(t, dir, nil, alwaysTTY)
	if !strings.Contains(first, terminalClose) {
		t.Fatalf("printed %q, want the terminal explanation", first)
	}
	requireNoMarker(t, dir)

	if second := showTo(t, dir, nil, alwaysTTY); !strings.Contains(second, terminalClose) {
		t.Errorf("second call printed %q; with no marker written the explanation shows again", second)
	}
}

// TestShowInboundExplanation_UnsearchableDirectory: an os.Lstat that fails for
// any reason counts as an absent marker, so a directory niwa cannot read
// produces the paragraph rather than silence.
func TestShowInboundExplanation_UnsearchableDirectory(t *testing.T) {
	skipAsRoot(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if out := showTo(t, dir, nil, alwaysTTY); !strings.Contains(out, explanationMarker) {
		t.Errorf("printed %q, want the explanation: an unreadable directory means the marker is unknown, not present", out)
	}
}

// TestShowInboundExplanation_UnresolvableDirectory: with no directory to
// remember anything in, niwa says so. It does not consult the terminal check at
// all, because the terminal version promises silence niwa could not deliver.
func TestShowInboundExplanation_UnresolvableDirectory(t *testing.T) {
	dir := t.TempDir()
	consulted := false
	isTTY := func() bool { consulted = true; return true }

	var buf bytes.Buffer
	showInboundExplanation(&buf, dir, errors.New("determining home directory: no home"), isTTY)

	if consulted {
		t.Error("the terminal check was consulted with an unresolvable configuration directory; there is nothing to remember either way")
	}
	if !strings.Contains(buf.String(), nonTerminalClose) {
		t.Errorf("printed %q, want the non-terminal closing sentence", buf.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the directory holds %v; nothing is created when the path could not be resolved", entries)
	}
}

// TestShowInboundExplanation_ConcurrentFirstCalls: parallel dispatches race for
// the same marker, and O_EXCL is what makes that safe. Every caller returns,
// exactly one file exists afterwards, and at least one developer-facing stream
// got the paragraph.
func TestShowInboundExplanation_ConcurrentFirstCalls(t *testing.T) {
	dir := t.TempDir()
	const callers = 8

	var wg sync.WaitGroup
	outs := make([]string, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var buf bytes.Buffer
			showInboundExplanation(&buf, dir, nil, alwaysTTY)
			outs[i] = buf.String()
		}()
	}
	wg.Wait()

	printed := 0
	for _, out := range outs {
		if strings.Contains(out, explanationMarker) {
			printed++
		}
	}
	if printed == 0 {
		t.Error("no caller printed the explanation; the first dispatch must show it")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the directory holds %d entries, want exactly one marker", len(entries))
	}
	requireMarker(t, dir)
}

// hostConfigDir returns the configuration directory setHostConfig pointed
// XDG_CONFIG_HOME at, which is where the marker belongs.
func hostConfigDir(t *testing.T) string {
	t.Helper()
	home := os.Getenv("XDG_CONFIG_HOME")
	if home == "" {
		t.Fatal("XDG_CONFIG_HOME is unset; call setHostConfig first")
	}
	return filepath.Join(home, "niwa")
}

// stubStderrTTY points the terminal check at a fixed answer for one test.
func stubStderrTTY(t *testing.T, terminal bool) {
	t.Helper()
	prev := IsStderrTTY
	IsStderrTTY = func() bool { return terminal }
	t.Cleanup(func() { IsStderrTTY = prev })
}

// TestDispatch_Notice_OnlyWhenTheBehaviorApplies: the explanation follows
// inboundApplied and nothing else. A dispatch that did not turn the behavior on
// -- and one where the agent could not receive it -- says nothing and remembers
// nothing, so a later dispatch that does turn it on still explains itself.
func TestDispatch_Notice_OnlyWhenTheBehaviorApplies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		host    string
		flag    *bool
		harness string
	}{
		{"nothing set", "", nil, ""},
		{"=false over the machine setting", hostInboundOn, inboundFlag(false), ""},
		{"an agent that cannot receive it", "", inboundFlag(true), string(agent.AgentCodex)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, tc.host)
			installDispatchFakes(t, root)
			stubStderrTTY(t, true)
			dispatchDetach = true
			dispatchAcceptSessionMessages = tc.flag
			if tc.harness != "" {
				t.Setenv("NIWA_DISPATCH_HARNESS", tc.harness)
			}

			_, stderr, err := runDispatchCmd(t, "do a thing")
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if strings.Contains(stderr, explanationMarker) {
				t.Errorf("the explanation printed for a dispatch the behavior never applied to; stderr:\n%s", stderr)
			}
			requireNoMarker(t, hostConfigDir(t))
		})
	}
}

// TestDispatch_Notice_DetachedFollowsTheAuditLine: with nothing taking the
// terminal, the explanation is the line right behind the audit line, so a
// developer reads what happened and then why it matters.
func TestDispatch_Notice_DetachedFollowsTheAuditLine(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	stubStderrTTY(t, true)
	dispatchDetach = true
	dispatchAcceptSessionMessages = inboundFlag(true)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	requireAdjacentLines(t, stderr, auditMarker, explanationMarker)
	if !strings.Contains(stderr, terminalClose) {
		t.Errorf("stderr does not carry the terminal closing sentence:\n%s", stderr)
	}
	requireMarker(t, hostConfigDir(t))
	if f.attachCalled != 0 {
		t.Errorf("attach called %d times on a detached dispatch", f.attachCalled)
	}
}

// requireAdjacentLines fails unless the line holding second comes directly
// after the line holding first.
func requireAdjacentLines(t *testing.T, stderr, first, second string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	for i, line := range lines {
		if !strings.Contains(line, first) {
			continue
		}
		if i+1 >= len(lines) {
			t.Fatalf("%q is the last line; %q must follow it. stderr:\n%s", first, second, stderr)
		}
		if !strings.Contains(lines[i+1], second) {
			t.Fatalf("the line after %q is %q, want it to hold %q. stderr:\n%s", first, lines[i+1], second, stderr)
		}
		return
	}
	t.Fatalf("no line holds %q. stderr:\n%s", first, stderr)
}

// TestDispatch_Notice_ForegroundFollowsTheAuditLine: a foreground turn has
// already ended, so there is no attach to wait for. The explanation goes out
// behind the audit line and ahead of step 14's closing line.
func TestDispatch_Notice_ForegroundFollowsTheAuditLine(t *testing.T) {
	base, ok := agentplan.For(agent.AgentClaude).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for the default agent")
	}
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	spec := base
	spec.Runner = agentplan.RunnerForeground
	substituteLaunchSpec(t, spec)
	stubStderrTTY(t, true)
	dispatchAcceptSessionMessages = inboundFlag(true)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	requireAdjacentLines(t, stderr, auditMarker, explanationMarker)

	const turnEnded = "niwa: the turn ended."
	endedAt := strings.Index(stderr, turnEnded)
	if endedAt < 0 {
		t.Fatalf("the foreground closing line is missing; this test is not driving a foreground launch. stderr:\n%s", stderr)
	}
	if at := strings.Index(stderr, explanationMarker); at > endedAt {
		t.Errorf("the explanation printed after the closing line. stderr:\n%s", stderr)
	}
	requireMarker(t, hostConfigDir(t))
	if f.attachCalled != 0 {
		t.Errorf("attach called %d times on a foreground dispatch", f.attachCalled)
	}
}

// TestDispatch_Notice_WaitsForTheAttach: when `claude attach` is about to take
// the terminal, a paragraph printed first is one the developer never sees --
// and niwa would then have remembered showing it. The stub inspects the world
// at the moment of the attach and finds neither the explanation nor the marker.
func TestDispatch_Notice_WaitsForTheAttach(t *testing.T) {
	for _, tc := range []struct {
		name      string
		attachErr error
	}{
		{"the attach succeeds", nil},
		{"the attach fails", errors.New("no such session")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, "")
			installDispatchFakes(t, root)
			stubStderrTTY(t, true)
			dispatchAcceptSessionMessages = inboundFlag(true)
			cfgDir := hostConfigDir(t)

			var atAttach string
			var markerAtAttach bool
			var errBuf bytes.Buffer
			dispatchAttach = func(agentplan.LaunchSpec, string, string) error {
				atAttach = errBuf.String()
				_, statErr := os.Lstat(markerIn(cfgDir))
				markerAtAttach = statErr == nil
				return tc.attachErr
			}

			cmd := newTestCommand(&bytes.Buffer{}, &errBuf)
			if err := runDispatch(cmd, []string{"do a thing"}); err != nil {
				t.Fatalf("dispatch: %v", err)
			}

			if atAttach == "" {
				t.Fatal("the attach stub was never called; this test is not driving the attach path")
			}
			if !strings.Contains(atAttach, auditMarker) {
				t.Errorf("stderr at the attach is missing the audit line:\n%s", atAttach)
			}
			if strings.Contains(atAttach, explanationMarker) {
				t.Errorf("the explanation printed before the attach took the terminal:\n%s", atAttach)
			}
			if markerAtAttach {
				t.Error("the marker existed before the attach returned; a paragraph nobody read is not remembered")
			}

			stderr := errBuf.String()
			if !strings.HasSuffix(stderr, explanationTerminalLine+"\n") {
				t.Errorf("stderr does not end with the terminal explanation:\n%s", stderr)
			}
			requireMarker(t, cfgDir)

			if tc.attachErr != nil {
				for _, want := range []string{
					"niwa: warning: could not attach to session",
					"niwa: the session is running; attach later with:",
				} {
					if !strings.Contains(stderr, want) {
						t.Errorf("a failed attach no longer prints %q:\n%s", want, stderr)
					}
				}
			}
		})
	}
}

// newTestCommand builds the cobra command runDispatch writes through, with both
// streams pointed at the given buffers. Tests that need to read stderr while
// runDispatch is still running use this rather than runDispatchCmd, which only
// hands the buffers back at the end.
func newTestCommand(out, err *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetOut(out)
	cmd.SetErr(err)
	cmd.SetContext(context.Background())
	return cmd
}

// TestDispatch_Notice_NonTerminalDispatchRemembersNothing: a dispatch from a
// script or a CI job prints the paragraph with the sentence that admits it will
// print again, and writes nothing beside config.toml.
func TestDispatch_Notice_NonTerminalDispatchRemembersNothing(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostInboundOn)
	installDispatchFakes(t, root)
	stubStderrTTY(t, false)
	dispatchDetach = true
	cfgDir := hostConfigDir(t)

	before := dirEntryNames(t, cfgDir)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(stderr, nonTerminalClose) {
		t.Errorf("stderr does not carry the non-terminal closing sentence:\n%s", stderr)
	}
	if got := dirEntryNames(t, cfgDir); !slicesEqual(before, got) {
		t.Errorf("the configuration directory went from %v to %v; a non-terminal dispatch creates nothing", before, got)
	}
}

// dirEntryNames lists the names in dir, or nothing when dir does not exist.
func dirEntryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDispatch_Notice_FollowsTheConfigurationPath: the marker lives beside
// config.toml, wherever the environment puts it. A developer who moved their
// configuration with XDG_CONFIG_HOME does not get a second copy of the record
// under their home directory, and one who has not set it gets the default.
func TestDispatch_Notice_FollowsTheConfigurationPath(t *testing.T) {
	t.Run("XDG_CONFIG_HOME", func(t *testing.T) {
		root := setupDispatchWorkspace(t)
		chdir(t, root)
		setHostConfig(t, "")
		home := t.TempDir()
		t.Setenv("HOME", home)
		installDispatchFakes(t, root)
		stubStderrTTY(t, true)
		dispatchDetach = true
		dispatchAcceptSessionMessages = inboundFlag(true)

		if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		requireMarker(t, hostConfigDir(t))
		if _, err := os.Lstat(filepath.Join(home, ".config", "niwa")); err == nil {
			t.Error("a niwa directory appeared under HOME while XDG_CONFIG_HOME was set")
		}
		if _, err := os.Lstat(filepath.Join(home, ".claude")); err == nil {
			t.Error("something was created under the agent's own home directory; this feature never touches it")
		}
	})

	t.Run("HOME fallback", func(t *testing.T) {
		root := setupDispatchWorkspace(t)
		chdir(t, root)
		home := t.TempDir()
		// GlobalConfigPath treats an empty value as unset, which is what a
		// developer who never exported it has.
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", home)
		installDispatchFakes(t, root)
		stubStderrTTY(t, true)
		dispatchDetach = true
		dispatchAcceptSessionMessages = inboundFlag(true)

		if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		requireMarker(t, filepath.Join(home, ".config", "niwa"))
		if _, err := os.Lstat(filepath.Join(home, ".claude")); err == nil {
			t.Error("something was created under the agent's own home directory; this feature never touches it")
		}
	})
}

// TestDispatch_Notice_CreatesTheConfigurationDirectory: a developer who has
// never written a niwa configuration still gets the paragraph remembered, in a
// directory niwa makes for it.
func TestDispatch_Notice_CreatesTheConfigurationDirectory(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	// An empty body leaves XDG_CONFIG_HOME pointing at a temp dir with no
	// niwa/ inside it.
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	stubStderrTTY(t, true)
	dispatchDetach = true
	dispatchAcceptSessionMessages = inboundFlag(true)

	cfgDir := hostConfigDir(t)
	if _, err := os.Lstat(cfgDir); err == nil {
		t.Fatalf("%s already exists; this test is about creating it", cfgDir)
	}

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(stderr, explanationMarker) {
		t.Fatalf("the explanation did not print:\n%s", stderr)
	}
	info, err := os.Stat(cfgDir)
	if err != nil {
		t.Fatalf("the configuration directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", cfgDir)
	}
	requireMarker(t, cfgDir)
}

// TestDispatch_Notice_FailedDispatchExplainsNothing: every failure returns
// before the audit line, so a dispatch that never produced a durable session
// neither explains the behavior nor remembers having done so.
func TestDispatch_Notice_FailedDispatchExplainsNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(t *testing.T, root string, f *dispatchFakes)
	}{
		{"the launch fails", func(_ *testing.T, _ string, f *dispatchFakes) {
			dispatchLaunch = func(context.Context, launchRequest) error {
				f.launchCalled++
				return errors.New("launch refused")
			}
		}},
		{"the capture fails", func(_ *testing.T, _ string, f *dispatchFakes) {
			dispatchCapture = func(agentplan.SessionRecords, string, string, time.Duration, func() time.Time, time.Duration) (string, string, error) {
				f.captureCalled++
				return "", "", errors.New("no session record")
			}
		}},
		{"the mapping write fails", func(t *testing.T, root string, _ *dispatchFakes) {
			// A file where the store's directory belongs makes the write fail
			// without touching the seam runDispatch calls.
			sessions := filepath.Join(root, workspace.StateDir, "sessions")
			if err := os.MkdirAll(filepath.Dir(sessions), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(sessions, []byte("not a directory"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, hostInboundOn)
			f := installDispatchFakes(t, root)
			stubStderrTTY(t, true)
			dispatchDetach = true
			tc.break_(t, root, f)

			_, stderr, err := runDispatchCmd(t, "do a thing")
			if err == nil {
				t.Fatalf("dispatch succeeded; this test needs it to fail. stderr:\n%s", stderr)
			}
			if strings.Contains(stderr, explanationMarker) {
				t.Errorf("a failed dispatch printed the explanation:\n%s", stderr)
			}
			requireNoMarker(t, hostConfigDir(t))
		})
	}
}

// TestDispatch_Notice_UnwritableConfigDirDoesNotFailTheDispatch: the marker is
// a convenience, never a precondition. A configuration directory niwa cannot
// write costs the developer a repeated paragraph and nothing else.
func TestDispatch_Notice_UnwritableConfigDirDoesNotFailTheDispatch(t *testing.T) {
	skipAsRoot(t)
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostInboundOn)
	cfgDir := hostConfigDir(t)
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfgDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cfgDir, 0o700) })
	installDispatchFakes(t, root)
	stubStderrTTY(t, true)
	dispatchDetach = true

	for _, pass := range []string{"first", "second"} {
		_, stderr, err := runDispatchCmd(t, "do a thing")
		if err != nil {
			t.Fatalf("%s dispatch failed because the marker could not be written: %v", pass, err)
		}
		if !strings.Contains(stderr, explanationMarker) {
			t.Errorf("%s dispatch printed no explanation; with nothing remembered it shows every time:\n%s", pass, stderr)
		}
	}
	requireNoMarker(t, cfgDir)
}

// TestDispatch_Notice_LeavesConfigTomlAlone: the record is a file niwa creates
// beside the configuration, never an edit to it. A developer's comments,
// ordering and mtime survive untouched.
func TestDispatch_Notice_LeavesConfigTomlAlone(t *testing.T) {
	const body = "# my machine\n[global]\naccept_session_messages_on_dispatch = true\n"
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, body)
	installDispatchFakes(t, root)
	stubStderrTTY(t, true)
	dispatchDetach = true

	cfgDir := hostConfigDir(t)
	cfgFile := filepath.Join(cfgDir, "config.toml")
	beforeInfo, err := os.Stat(cfgFile)
	if err != nil {
		t.Fatal(err)
	}

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(stderr, explanationMarker) {
		t.Fatalf("the explanation did not print, so this test proves nothing:\n%s", stderr)
	}
	requireMarker(t, cfgDir)

	afterBytes, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterBytes) != body {
		t.Errorf("config.toml now reads:\n%s\nwant it unchanged:\n%s", afterBytes, body)
	}
	afterInfo, err := os.Stat(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Errorf("config.toml's modification time moved from %v to %v; niwa never opened it for writing",
			beforeInfo.ModTime(), afterInfo.ModTime())
	}
}

// TestDispatch_Notice_StaysOffStdout: stdout is the dispatch's machine-readable
// half. The explanation is stderr's business whether or not there is a terminal
// on the other end.
func TestDispatch_Notice_StaysOffStdout(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
	}{
		{"at a terminal", true},
		{"not at a terminal", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, hostInboundOn)
			installDispatchFakes(t, root)
			stubStderrTTY(t, tc.terminal)
			dispatchDetach = true

			stdout, stderr, err := runDispatchCmd(t, "do a thing")
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if !strings.Contains(stderr, explanationMarker) {
				t.Fatalf("the explanation did not print at all:\n%s", stderr)
			}
			if strings.Contains(stdout, explanationMarker) || strings.Contains(stdout, explanationGuideURL) {
				t.Errorf("stdout carries the explanation:\n%s", stdout)
			}
		})
	}
}
