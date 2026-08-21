package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// The foreground path runs the worker's whole turn in the caller's terminal, so
// every test here drives a real process. Nothing else can tell a launch that
// waits from one that returns, streamed output from output collected and dumped
// at the end, or a closed stdin from an inherited one.
//
// The shell stands in for the agent binary. What is being checked is how niwa
// starts a process and what it hands it, which is the same question whatever
// the process turns out to be.

// syncBuffer is a Buffer that can be read while the process writing into it is
// still running. The streaming test needs exactly that, and bytes.Buffer alone
// would race the copier goroutine os/exec runs for a non-file writer.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// foregroundSpec is a launch description for a runner that executes its turn in
// the foreground, built on the shell so the turn can be scripted. It declares a
// detached-only argument as well, so a test can tell "the foreground path drops
// it" from "the spec never had one".
func foregroundSpec() agentplan.LaunchSpec {
	return agentplan.LaunchSpec{
		Binary:       "sh",
		Runner:       agentplan.RunnerForeground,
		DetachedArgs: []string{"--machine-readable"},
	}
}

// foregroundLaunch runs one scripted turn through the real launcher on the
// foreground path and returns what the caller's streams received. The script
// rides the pass-through, so the prompt stays where a prompt goes: the last
// element, on its own.
func foregroundLaunch(t *testing.T, instance, script, prompt string, stdout, stderr *syncBuffer) error {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("no shell to launch: %v", err)
	}
	return realDispatchLaunch(context.Background(), launchRequest{
		Spec:        foregroundSpec(),
		Mode:        agentplan.LaunchForeground,
		InstanceDir: instance,
		Body:        prompt,
		Passthrough: []string{"-c", script},
		Stdout:      stdout,
		Stderr:      stderr,
	})
}

// TestForegroundWorkerRunsTheTurnInFrontOfTheCaller is the property the whole
// mode exists for: the developer watches the work.
//
// It asserts the three halves that make that true and that a plausible
// implementation drops. The output reaches the caller's streams *while the turn
// is still running* -- an implementation that collected it and handed it over at
// the end would pass a "the output is there" check and fail this one, and it is
// the difference between watching a run and reading its transcript. The two
// streams stay apart, because merging them corrupts anything structured and a
// healthy run writes to both. And the call does not return until the turn ends.
func TestForegroundWorkerRunsTheTurnInFrontOfTheCaller(t *testing.T) {
	instance := t.TempDir()
	var stdout, stderr syncBuffer

	// Writes to both streams, then keeps working for long enough that a launch
	// which returned early, or output which arrived only at the end, is
	// observable rather than a matter of timing luck.
	script := `echo first; echo problem 1>&2; sleep 1; echo second`

	done := make(chan error, 1)
	go func() { done <- foregroundLaunch(t, instance, script, "do the thing", &stdout, &stderr) }()

	sawEarly := false
	deadline := time.Now().Add(10 * time.Second)
	for !sawEarly {
		select {
		case err := <-done:
			// Either the launch did not wait for the turn, or it waited and
			// handed the output over at the end. Both leave the developer with
			// a transcript instead of a run to watch, and this is where they
			// show up: the call is over and nothing arrived while it ran.
			t.Fatalf("the launch returned (error %v) before any of the turn's output reached the caller's stdout; a foreground dispatch waits for the work AND streams it.\nstdout: %q", err, stdout.String())
		default:
		}
		if strings.Contains(stdout.String(), "first") {
			sawEarly = true
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("nothing reached the caller's stdout while the turn was running")
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("foreground launch: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the launch never returned")
	}

	if got := stdout.String(); !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Errorf("stdout = %q, want the whole turn's output", got)
	}
	if got := stdout.String(); strings.Contains(got, "problem") {
		t.Errorf("stdout = %q, carries the error stream too; merging them corrupts anything structured", got)
	}
	if got := stderr.String(); !strings.Contains(got, "problem") {
		t.Errorf("stderr = %q, want the worker's error output", got)
	}

	// And nothing was written into the instance. The terminal has the output
	// here, and a second copy niwa keeps in sync with a process it is already
	// waiting on is machinery with no reader. The instance log belongs to the
	// detached path, where nobody is watching.
	entries, err := os.ReadDir(filepath.Join(instance, ".niwa"))
	if err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "dispatch-") {
				t.Errorf("a foreground launch wrote %s into the instance; the terminal already has the output", e.Name())
			}
		}
	}
}

// TestForegroundWorkerGetsNoStdin is the measured one, and the one an
// implementation that reaches for "inherit stdio and be done" loses.
//
// An agent that reads stdin in addition to its positional prompt blocks on an
// inherited one: twenty seconds with no output, no session record, and no
// request made. Attaching the terminal's stdout and stderr does not require
// attaching its stdin, and a foreground worker that hangs is a worse outcome
// than the detached one it replaces.
//
// The check has to seed this process's own stdin with something readable,
// because a test binary's stdin is already empty -- against that, an inherited
// stdin and a closed one look identical and the test would prove nothing.
func TestForegroundWorkerGetsNoStdin(t *testing.T) {
	const sentinel = "THIS-CAME-FROM-THE-CALLERS-STDIN"

	seeded := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(seeded, []byte(sentinel+"\n"), 0o600); err != nil {
		t.Fatalf("writing the seeded stdin: %v", err)
	}
	f, err := os.Open(seeded)
	if err != nil {
		t.Fatalf("opening the seeded stdin: %v", err)
	}
	defer f.Close()
	prev := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = prev })

	var stdout, stderr syncBuffer
	if err := foregroundLaunch(t, t.TempDir(), `cat`, "do the thing", &stdout, &stderr); err != nil {
		t.Fatalf("foreground launch: %v", err)
	}
	if strings.Contains(stdout.String(), sentinel) {
		t.Errorf("the worker read the caller's stdin: %q\nAn agent that reads stdin alongside its prompt blocks on an inherited one, and a foreground worker that hangs leaves nothing on disk to diagnose it by.", stdout.String())
	}
}

// TestForegroundWorkerKeepsThePromptOneArgvElement checks at the exec boundary
// what the argv builder is unit-tested for: a prompt full of metacharacters,
// quotes, spaces, and a newline arrives as one element with its bytes intact,
// rather than being split into flag positions on the way.
func TestForegroundWorkerKeepsThePromptOneArgvElement(t *testing.T) {
	prompt := "fix the bug; rm -rf / --no-preserve-root\n--malicious 'quoted \"value\"' && echo pwned"

	var stdout, stderr syncBuffer
	// The prompt lands as $0 for a shell invoked with -c, so echoing it back is
	// the process reporting the single element it actually received.
	if err := foregroundLaunch(t, t.TempDir(), `printf '%s' "$0"`, prompt, &stdout, &stderr); err != nil {
		t.Fatalf("foreground launch: %v", err)
	}
	if got := stdout.String(); got != prompt {
		t.Errorf("the worker received %q, want the prompt verbatim as one element:\n%q", got, prompt)
	}
}

// TestForegroundWorkerExitStatusIsNotTaskFailure holds the reporting posture
// the whole feature is built on: this agent's exit code says the turn ended, not
// that the work happened. A read-only sandbox failure exits 0.
//
// So a non-zero exit is not a failed dispatch. Treating it as one would roll the
// instance back out from under work that may well have happened, and the
// developer watched the run anyway -- whatever it said is already on their
// terminal. A process that could not be started at all is a different thing:
// then no turn ran, and there is nothing to keep.
func TestForegroundWorkerExitStatusIsNotTaskFailure(t *testing.T) {
	var stdout, stderr syncBuffer
	if err := foregroundLaunch(t, t.TempDir(), `echo working; exit 1`, "do the thing", &stdout, &stderr); err != nil {
		t.Errorf("a turn that ended non-zero was reported as a failed launch (%v); the exit status of this kind of run is not a verdict on the task, and failing here destroys the instance", err)
	}

	// The other direction: nothing ran, so say so. A file that exists and
	// cannot be executed is the closest thing to a binary that dies before it
	// is a process.
	unrunnable := filepath.Join(t.TempDir(), "not-a-binary")
	if err := os.WriteFile(unrunnable, []byte("not executable\n"), 0o600); err != nil {
		t.Fatalf("writing the unrunnable file: %v", err)
	}
	err := runForegroundWorker(foregroundSpec(), unrunnable, nil, t.TempDir(), os.Environ(), &stdout, &stderr)
	if err == nil {
		t.Error("a worker that never started was reported as a turn that ended")
	}
}

// TestCtrlCReachesAForegroundWorkerAndNotADetachedOne asserts both directions of
// the difference --detach makes to a running worker, because each is only
// meaningful against the other.
//
// A foreground worker shares the caller's process group, so the interrupt a
// developer sends to a command running in front of them reaches it -- which is
// what they expect of any such command. A detached one gets a session of its
// own (Setsid), so the same interrupt does not, which is what lets it survive
// the terminal it was launched from.
//
// Process group is the property under test rather than an actual signal: it is
// what the kernel routes a terminal interrupt by, and sending one here would
// take the test binary with it. It is also POSIX, where session id is not --
// macOS ps rejects `-o sid`.
func TestCtrlCReachesAForegroundWorkerAndNotADetachedOne(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("no shell to launch: %v", err)
	}
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skipf("no ps to read a process group with: %v", err)
	}
	mine := strconv.Itoa(syscall.Getpgrp())

	// Foreground: the launch waits, so the group is on disk by the time it
	// returns.
	instance := t.TempDir()
	pgidFile := filepath.Join(instance, "pgid")
	var stdout, stderr syncBuffer
	if err := foregroundLaunch(t, instance, `ps -o pgid= -p $$ > "`+pgidFile+`"`, "do the thing", &stdout, &stderr); err != nil {
		t.Fatalf("foreground launch: %v", err)
	}
	raw, err := os.ReadFile(pgidFile)
	if err != nil {
		t.Fatalf("reading the foreground worker's process group: %v\nstderr: %s", err, stderr.String())
	}
	if got := strings.TrimSpace(string(raw)); got != mine {
		t.Errorf("the foreground worker is in group %s and the launcher in %s; an interrupt aimed at the command the developer is watching would miss it", got, mine)
	}

	// Detached: its own group, so the same interrupt cannot reach it. The
	// launch returns without waiting, so poll for the file.
	detachedInstance := t.TempDir()
	detachedPgid := filepath.Join(detachedInstance, "pgid")
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no shell to launch: %v", err)
	}
	if err := startDetachedWorker(foregroundSpec(), sh, []string{"-c", `ps -o pgid= -p $$ > "` + detachedPgid + `"`}, detachedInstance, os.Environ()); err != nil {
		t.Fatalf("startDetachedWorker: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(detachedPgid); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the detached worker never reported its process group")
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw, err = os.ReadFile(detachedPgid)
	if err != nil {
		t.Fatalf("reading the detached worker's process group: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got == mine {
		t.Errorf("the detached worker shares the launcher's group %s; an interrupt to the terminal it was launched from would take it down, which is what detaching is for", got)
	}
}
