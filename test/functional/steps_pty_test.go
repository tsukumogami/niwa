package functional

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// steps_pty_test.go holds the harness's terminal primitives: running a command
// under a real pty and quoting a command for the shell that `script` invokes.
//
// They live here rather than beside the first steps that needed them because
// they are not specific to any feature. Anything that has to look like a
// terminal to the NIWA binary -- an interactive prompt, an isTTY check, a
// one-time notice shown only where someone can read it -- goes through
// runUnderPTY, and a step author looking for that capability should find it
// under its own name.
//
// The exception is iStartInteractiveCodexAt, which builds its own `script`
// invocation. Almost nothing it needs matches: it runs the real codex binary
// rather than the niwa one, from a resolved location rather than the workspace
// root, on its own dwell timeout that is EXPECTED to expire because the
// interface never exits, and it keeps the transcript in its own field instead
// of the shared stdout/stderr/exit-code slots. It borrows shellQuote and
// scriptCommand from here and nothing else.

// ptyStepTimeout bounds the steps that hand the binary a standard input this
// harness controls: the four that reach runUnderPTY, whether or not they supply
// any input, and iRunWithStdinHeldOpen, which holds a pipe open without a pty
// at all. (The live-Codex interactive step is not among them; it runs on its
// own dwell.)
//
// Supplying no input is not the safe case. A pty hands the child a terminal
// rather than an immediate end-of-input, so a command that reads stdin waits
// exactly as long as one waiting on input that never comes. Without a bound
// either burns the suite's global deadline and takes every other scenario with
// it; with it, the failure is a step failure naming the command.
const ptyStepTimeout = 60 * time.Second

// ptyInputTail is how long the pty's input pipe stays open after its last
// byte. BSD `script` (macOS) that reads the input and end-of-input in the same
// pass writes the terminal's EOF character to the pty ahead of the input, so
// the child's first read comes back empty. A short gap before the close lets
// the input reach the pty first. util-linux `script` doesn't need it, and
// doesn't mind it.
const ptyInputTail = 100 * time.Millisecond

// scriptCommand returns the `script` invocation that runs inner, a command
// string for the shell, under a fresh pty and discards the transcript.
// util-linux `script` (Linux) takes the command with -c; BSD `script` (macOS)
// has no -c and takes the command as trailing arguments instead.
//
// The two also differ in what `script` itself exits with. BSD `script` exits
// with the child's status. util-linux `script` exits 0 whatever the child did
// unless it is given -e (--return), so without -e every pty scenario on Linux
// would read a refused command as a success. With -e both report the child's
// own status, which is what runUnderPTY hands to the exit-code assertions.
func scriptCommand(ctx context.Context, inner string) *exec.Cmd {
	if runtime.GOOS == "linux" {
		return exec.CommandContext(ctx, "script", "-q", "-e", "-c", inner, "/dev/null")
	}
	return exec.CommandContext(ctx, "script", "-q", "/dev/null", "/bin/sh", "-c", inner)
}

// ptyPromptQuiet and ptyPromptWait bound how long the input feed waits for the
// command to settle at its prompt under BSD `script`; ptyTranscript.awaitPrompt
// says why it waits at all.
const (
	ptyPromptQuiet = 300 * time.Millisecond
	ptyPromptWait  = 5 * time.Second
)

// ptyTranscript collects what the pty showed, and notes when it last showed
// anything, so the input feed can tell when the command has settled at a
// prompt. `script` writes to it from exec's copying goroutine while the feed
// reads it from its own, hence the lock.
type ptyTranscript struct {
	mu   sync.Mutex
	buf  strings.Builder
	last time.Time
}

func (t *ptyTranscript) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = time.Now()
	return t.buf.Write(p)
}

func (t *ptyTranscript) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

// awaitPrompt returns once the command has shown something and then gone quiet
// for ptyPromptQuiet, or after ptyPromptWait, whichever is first.
//
// BSD `script` (macOS) forwards input to the pty the moment it arrives. Input
// that lands before the command has switched the terminal to raw mode goes
// through the canonical line discipline instead, which turns a submitting \r
// into \n, so a raw-mode reader such as dispatch's prompt capture never sees
// its Enter. A command at an interactive prompt has printed it and is blocked
// reading, which is what "shown something, then quiet" detects. util-linux
// `script` doesn't need the wait, so on Linux this returns at once.
func (t *ptyTranscript) awaitPrompt() {
	if runtime.GOOS == "linux" {
		return
	}
	deadline := time.Now().Add(ptyPromptWait)
	for time.Now().Before(deadline) {
		t.mu.Lock()
		last := t.last
		t.mu.Unlock()
		if !last.IsZero() && time.Since(last) >= ptyPromptQuiet {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ptyInput returns the read end of a pipe that carries raw to a `script`
// process's standard input and then closes. It waits for the command to
// settle at its prompt first (see ptyTranscript.awaitPrompt), reading out the
// transcript the same `script` process writes. It feeds the pty in chunks
// rather than one burst: `script` performs a short write to the pty master and
// does not retry the remainder, so a large single write silently loses
// everything past the first few kilobytes and the child waits forever for
// input that was never delivered. Chunking keeps each write inside the pty
// buffer. This is a property of the harness, not of any reader under test. The
// close waits ptyInputTail after the last chunk; the constant says why.
func ptyInput(raw string, out *ptyTranscript) (*os.File, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("creating pty input pipe: %w", err)
	}
	go func() {
		defer pw.Close()
		out.awaitPrompt()
		const chunk = 2048
		for i := 0; i < len(raw); i += chunk {
			end := i + chunk
			if end > len(raw) {
				end = len(raw)
			}
			if _, err := io.WriteString(pw, raw[i:end]); err != nil {
				return
			}
			if end < len(raw) {
				time.Sleep(20 * time.Millisecond)
			}
		}
		time.Sleep(ptyInputTail)
	}()
	return pr, nil
}

// iRunUnderPTYWithInput drives the niwa binary under `script` (see
// scriptCommand), which allocates a real pty and connects it to the
// child's stdin/stdout. This is the test seam for R13 TTY-Y / TTY-N
// scenarios that need the binary's IsStdinTTY() check to return true.
// The input is written to the pty in chunks; ptyInput says why it
// cannot be one write.
//
// `script` ships on every Linux CI image (util-linux) and on macOS
// (BSD); scriptCommand covers the difference in their syntax. Adding a
// Go pty library (github.com/creack/pty) was considered and rejected to
// avoid a new dependency.
func iRunUnderPTYWithInput(ctx context.Context, command, input string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	run, err := runUnderPTY(ctx, s, command, input)
	if err != nil {
		return ctx, err
	}
	s.stdout = run.stdout
	s.stderr = run.stderr
	s.exitCode = run.exitCode
	s.shellPwd = ""
	return ctx, nil
}

// ptyRun is one command's outcome under `script`: what the terminal showed and
// what the command exited with. The parallel step keeps one of these per run,
// which is why this is a value rather than fields written straight into
// testState.
type ptyRun struct {
	// stdout is what the pty surface carried.
	stdout string
	// stderr is the same bytes as stdout plus anything `script` itself wrote
	// off-pty; see runUnderPTY for why the two are not separable here.
	stderr string
	// exitCode is the command's own exit status, which `script` propagates.
	exitCode int
}

// runUnderPTY builds and runs one `script` invocation and
// returns its transcript and exit code without touching testState. Both the
// single-run step above and the parallel step in session_message_steps_test.go
// go through it, so the pty construction -- the cd-and-exec inner command, the
// chunked stdin feed, the timeout, and the stdout/stderr merge -- exists once.
//
// A non-nil error is a harness failure (no `script` on PATH, a pipe that could
// not be created, a run that outlived the deadline), never a non-zero exit from
// the command, which is reported in the returned run.
func runUnderPTY(ctx context.Context, s *testState, command, input string) (ptyRun, error) {
	var run ptyRun
	if _, err := exec.LookPath("script"); err != nil {
		return run, fmt.Errorf("`script` not on PATH; cannot drive PTY scenario: %w", err)
	}

	// Substitute {repo:<name>} placeholders for symmetry with iRunFromWorkspaceRoot.
	for repoName, repoURL := range s.repoURLs {
		command = strings.ReplaceAll(command, "{repo:"+repoName+"}", repoURL)
	}

	args := strings.Fields(command)
	if len(args) > 0 && args[0] == "niwa" {
		args[0] = s.binPath
	}

	// Build the inner command. We change directory to workspaceRoot
	// then exec the binary so it inherits the pty `script` allocated.
	// Without `exec`, an intermediate bash would steal the pty and the
	// child's IsStdinTTY check would observe a pipe.
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	innerCmd := "cd " + shellQuote(s.workspaceRoot) + " && exec " + strings.Join(quoted, " ")

	// `script` runs cmd under a pty and writes the terminal-output
	// transcript to /dev/null. We capture the child's output via the
	// script process's stdout (which mirrors the pty master side). The
	// child's stdin is fed by writing to script's own stdin via
	// cmd.Stdin — script forwards stdin bytes to the pty so the child
	// sees them as terminal input.
	// A scenario whose input never terminates must fail as a step rather than
	// run out the suite's global deadline. godog's context carries no deadline
	// of its own, so one is imposed here.
	ptyCtx, cancel := context.WithTimeout(ctx, ptyStepTimeout)
	defer cancel()

	cmd := scriptCommand(ptyCtx, innerCmd)
	cmd.Env = s.buildEnv()
	// Convert escapes so feature files can write `y\n` and paste markers.
	rawInput := strings.ReplaceAll(input, `\n`, "\n")
	rawInput = strings.ReplaceAll(rawInput, `\r`, "\r")
	rawInput = strings.ReplaceAll(rawInput, `\e`, "\x1b")

	var stdout ptyTranscript
	var stderr strings.Builder
	pr, err := ptyInput(rawInput, &stdout)
	if err != nil {
		return run, err
	}
	cmd.Stdin = pr
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	pr.Close()
	if ptyCtx.Err() == context.DeadlineExceeded {
		return run, fmt.Errorf("pty step did not terminate within %s; the command is waiting on input that never arrives", ptyStepTimeout)
	}
	run.stdout = stdout.String()
	// util-linux `script` interleaves stdout and stderr on its single
	// PTY surface; the child's stderr is mirrored on stdout under PTY.
	// Treat the combined output as both for assertion purposes — both
	// fields contain the same bytes so any "error output contains"
	// step sees the prompt + Detail+Suggestion text.
	run.stderr = stdout.String() + stderr.String()
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			run.exitCode = exitErr.ExitCode()
			return run, nil
		}
		return run, fmt.Errorf("pty run failed: %w; stderr: %s", runErr, run.stderr)
	}
	return run, nil
}

// shellQuote escapes s for use inside a `bash -c` string. Single-quotes
// are doubled with the standard `'\”` trick. It is what lets a command
// string, and the directory it runs in, travel through `script` safely:
// runUnderPTY quotes both, and the live-Codex interactive step quotes its own
// directory the same way.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
