package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
)

// syntheticLaunchSpec is a launch description shaped unlike the one niwa ships
// today: a subcommand and a fixed policy flag ahead of the pass-through, a
// prompt separator, and different flag spellings. It exists so the argv builder
// is exercised against two shapes rather than against the only shape that
// happens to be declared, which is what makes it a builder rather than one
// agent's argv written in a loop.
func syntheticLaunchSpec() agentplan.LaunchSpec {
	return agentplan.LaunchSpec{
		Binary:          "othertool",
		LeadingArgs:     []string{"exec", "--headless"},
		PromptSeparator: true,
		Flags: agentplan.LaunchFlags{
			Model:        "-m",
			DisplayName:  "--label",
			SubagentType: "",
		},
	}
}

// TestBuildLaunchArgs_Order verifies the leading arguments come first,
// pass-through values sit in the middle as separate elements, and the prompt is
// the last single element.
func TestBuildLaunchArgs_Order(t *testing.T) {
	got := buildLaunchArgs(claudeLaunchSpec(), "/inst", "do the thing", []string{"--model", "opus", "--permission-mode", "acceptEdits"})
	want := []string{"--bg", "--model", "opus", "--permission-mode", "acceptEdits", "do the thing"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildLaunchArgs = %#v, want %#v", got, want)
	}
}

// TestBuildLaunchArgs_NoPassthrough verifies the minimal argv.
func TestBuildLaunchArgs_NoPassthrough(t *testing.T) {
	got := buildLaunchArgs(claudeLaunchSpec(), "/inst", "hi", nil)
	want := []string{"--bg", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildLaunchArgs = %#v, want %#v", got, want)
	}
}

// TestBuildLaunchArgs_SeparatorShape verifies a spec that declares a prompt
// separator gets one, immediately before the prompt and after everything else.
// Without it, an agent whose parser reads a leading dash as a flag would take a
// prompt beginning with one as an unknown argument.
func TestBuildLaunchArgs_SeparatorShape(t *testing.T) {
	got := buildLaunchArgs(syntheticLaunchSpec(), "/inst", "--looks-like-a-flag", []string{"-m", "fast"})
	want := []string{"exec", "--headless", "-m", "fast", "--", "--looks-like-a-flag"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildLaunchArgs = %#v, want %#v", got, want)
	}
}

// TestBuildLaunchArgs_PromptRemainsSingleElement verifies a prompt full of
// shell metacharacters, quotes, spaces, and a newline stays one argv element
// and never bleeds into a flag position -- the flag-injection guard (D8). It
// runs against both shapes, because the guard has to hold for whichever agent
// is launched and not just for the one whose argv was written first.
func TestBuildLaunchArgs_PromptRemainsSingleElement(t *testing.T) {
	prompt := "fix the bug; rm -rf / --no-preserve-root\n--malicious 'quoted \"value\"' && echo pwned"

	for _, tc := range []struct {
		name        string
		spec        agentplan.LaunchSpec
		passthrough []string
		wantLen     int
		wantLead    []string
	}{
		{
			name:        "claude",
			spec:        claudeLaunchSpec(),
			passthrough: []string{"--agent", "reviewer"},
			wantLen:     4,
			wantLead:    []string{"--bg", "--agent", "reviewer"},
		},
		{
			name:        "synthetic",
			spec:        syntheticLaunchSpec(),
			passthrough: []string{"-m", "fast"},
			wantLen:     6,
			wantLead:    []string{"exec", "--headless", "-m", "fast", "--"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := buildLaunchArgs(tc.spec, "/inst", prompt, tc.passthrough)
			if len(got) != tc.wantLen {
				t.Fatalf("got %d args, want %d: %#v", len(got), tc.wantLen, got)
			}
			if !reflect.DeepEqual(got[:len(tc.wantLead)], tc.wantLead) {
				t.Errorf("leading argv = %#v, want %#v", got[:len(tc.wantLead)], tc.wantLead)
			}
			if got[len(got)-1] != prompt {
				t.Errorf("prompt mangled: last element = %q, want %q", got[len(got)-1], prompt)
			}
		})
	}
}

// codexLaunchSpec returns the Codex launch description, for the tests below
// that pin the shape of the argv niwa builds from it. Reading it from the table
// rather than restating it is the point: these assert what the launcher does
// with a declaration, not what the declaration says.
func codexLaunchSpec(t *testing.T) agentplan.LaunchSpec {
	t.Helper()
	spec, ok := agentplan.For(agent.AgentCodex).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for codex")
	}
	return spec
}

// TestBuildLaunchArgs_WorkdirGrant checks the grant is rendered with the
// working directory substituted, sits ahead of the pass-through so an explicit
// posture wins, and is absent entirely for an agent that declares none.
func TestBuildLaunchArgs_WorkdirGrant(t *testing.T) {
	got := buildLaunchArgs(codexLaunchSpec(t), "/tmp/ws/inst", "do the thing", []string{"--sandbox", "read-only"})

	grantAt := slices.Index(got, "-c")
	if grantAt < 0 {
		t.Fatalf("no grant in argv: %#v", got)
	}
	if want := `projects={"/tmp/ws/inst"={trust_level="trusted"}}`; got[grantAt+1] != want {
		t.Errorf("grant = %q, want %q", got[grantAt+1], want)
	}
	if sandboxAt := slices.Index(got, "--sandbox"); sandboxAt < grantAt {
		t.Errorf("an explicit posture at %d precedes the grant at %d; the developer's own flag must come last", sandboxAt, grantAt)
	}

	// An agent that declares no grant gets none, rather than an empty pair.
	if plain := buildLaunchArgs(claudeLaunchSpec(), "/tmp/ws/inst", "hi", nil); slices.Contains(plain, "-c") {
		t.Errorf("a spec with no grant produced one: %#v", plain)
	}
	// And a grant naming no directory is not emitted at all.
	if noDir := buildLaunchArgs(codexLaunchSpec(t), "", "hi", nil); slices.Contains(noDir, "-c") {
		t.Errorf("a grant was emitted with no working directory: %#v", noDir)
	}
}

// TestCodexLaunchArgv pins the whole argv a Codex dispatch builds, because
// several of its elements are measured requirements rather than preferences and
// each fails in its own quiet way.
//
// `--skip-git-repo-check` is mandatory: an instance root is not a git
// repository and the run refuses to start in one without it, and trusting the
// directory does not substitute. `--json` is what makes the launch log
// parseable. The `--` separator is what stops a prompt beginning with a dash
// being read as a flag. And `--ephemeral` must never appear: it runs without
// persisting the session record, which is the substrate capture reads, so a
// dispatch carrying it would launch a worker niwa could never find again.
func TestCodexLaunchArgv(t *testing.T) {
	got := buildLaunchArgs(codexLaunchSpec(t), "/tmp/ws/inst", "--do the thing", nil)

	want := []string{
		"exec", "--json", "--skip-git-repo-check",
		"-c", `projects={"/tmp/ws/inst"={trust_level="trusted"}}`,
		"--", "--do the thing",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("codex launch argv =\n  %#v\nwant\n  %#v", got, want)
	}
	if slices.Contains(got, "--ephemeral") {
		t.Error("the launch argv suppresses the session record capture reads")
	}

	// And the process model, which the completeness suite only checks for
	// membership in the closed set. Flipped to backgrounded, every dispatch
	// would block for the length of the task and then die with a cancelled
	// context, and nothing else in the suite would notice: the detached-launch
	// test builds its own spec rather than reading the table.
	if spec := codexLaunchSpec(t); spec.Mode != agentplan.LaunchDetached {
		t.Errorf("codex launch mode = %d, want detached; it runs its turn in the foreground", spec.Mode)
	}
}

// TestStartDetachedWorker runs the detached path against a real process, which
// is the only way to check the things that make it detached: that it returns
// without waiting for the work, that the worker gets its own session, that its
// output lands in files inside the instance with the two streams kept apart,
// and that it keeps running after the launch call returns.
func TestStartDetachedWorker(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no shell to launch: %v", err)
	}
	instance := t.TempDir()
	done := filepath.Join(instance, "done")

	// Prints to both streams, reports its own process group, then finishes
	// after the launch call has certainly returned.
	//
	// The probe asks for pgid rather than sid because pgid is POSIX and sid is
	// not: macOS ps rejects `-o sid` with "keyword not found", which failed
	// this test twice over -- once on the identity assertion and once on the
	// stderr assertion, because ps wrote its complaint into the worker's
	// stderr log. Process group is also the property that matters here. Setsid
	// sets both, and it is the group that decides whether a signal aimed at
	// the launcher reaches the worker.
	script := `echo out; echo err 1>&2; ps -o pgid= -p $$ > "` + done + `.pgid"; sleep 0.3; echo finished > "` + done + `"`
	spec := agentplan.LaunchSpec{Binary: "sh", Mode: agentplan.LaunchDetached}

	start := time.Now()
	if err := startDetachedWorker(spec, sh, []string{"-c", script}, instance, os.Environ()); err != nil {
		t.Fatalf("startDetachedWorker: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Errorf("the launch waited %s for a worker that sleeps 300ms; it must return without waiting", elapsed)
	}
	if _, err := os.Stat(done); err == nil {
		t.Error("the worker had already finished when the launch returned; the test proves nothing about detaching")
	}

	// The worker outlives the call, so wait for its own completion marker.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(done); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the detached worker never finished")
		}
		time.Sleep(20 * time.Millisecond)
	}

	out, err := os.ReadFile(filepath.Join(instance, ".niwa", "dispatch-sh.out"))
	if err != nil {
		t.Fatalf("reading the worker's stdout log: %v", err)
	}
	errOut, err := os.ReadFile(filepath.Join(instance, ".niwa", "dispatch-sh.err"))
	if err != nil {
		t.Fatalf("reading the worker's stderr log: %v", err)
	}
	if strings.TrimSpace(string(out)) != "out" {
		t.Errorf("stdout log = %q, want just the stdout line", string(out))
	}
	if strings.TrimSpace(string(errOut)) != "err" {
		t.Errorf("stderr log = %q, want just the stderr line; the streams must not be merged", string(errOut))
	}

	// Its own process group, which is what Setsid buys: a signal sent to the
	// launcher's group does not reach it. Compared against this process's
	// actual group rather than its pid, because a test binary is not
	// necessarily its own group leader and comparing to the pid would pass for
	// the wrong reason whenever it is not.
	pgid, err := os.ReadFile(done + ".pgid")
	if err != nil {
		t.Fatalf("reading the worker's process group: %v", err)
	}
	if strings.TrimSpace(string(pgid)) == strconv.Itoa(syscall.Getpgrp()) {
		t.Error("the worker shares this process's group; a signal aimed at the launcher would take it down too")
	}
}

// TestRealDispatchLaunch_EmptyPromptRejected verifies an empty prompt is
// rejected before any exec (R43). It does not depend on the binary being
// present.
func TestRealDispatchLaunch_EmptyPromptRejected(t *testing.T) {
	err := realDispatchLaunch(context.Background(), claudeLaunchSpec(), t.TempDir(), "", "", nil, nil)
	if err == nil {
		t.Fatal("expected an error for an empty prompt, got nil")
	}
}
