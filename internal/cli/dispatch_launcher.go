package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// launchRequest is everything one launch needs: which agent, which process
// model, where, with what prompt, and where its output goes when the developer
// is the one reading it.
//
// Mode is a field rather than something read off the spec because the process
// model is not a property of the agent alone. The spec says what the agent's
// runner is; the caller resolves that against the invocation's --detach
// (agentplan.RunnerKind.ModeFor) and passes the answer here. A launcher that
// went back to the declaration for it could not see the flag, which is the
// defect this shape exists to prevent.
type launchRequest struct {
	// Spec is the launched agent's declaration.
	Spec agentplan.LaunchSpec

	// Mode is the process model for THIS invocation, resolved from the spec's
	// runner and the invocation's --detach.
	Mode agentplan.LaunchMode

	// InstanceDir is the instance the worker is rooted in, and cmd.Dir.
	InstanceDir string

	// Prefix is niwa-authored prompt text; Body is the developer's task. They
	// are kept apart all the way into the exec layer -- see below.
	Prefix string
	Body   string

	// Passthrough are the developer's own flags, already split into discrete
	// argv elements by the caller.
	Passthrough []string

	// Env is the worker's environment. No caller sets it today: every dispatch
	// leaves it nil, so the worker inherits the full parent environment
	// (os.Environ()). It is an extension point the launcher honors rather than
	// a seam anything currently goes through -- a non-nil Env is used
	// verbatim, so a caller that needs to hand the worker an allowlisted or
	// credential-scrubbed environment has one place to put it.
	Env []string

	// Stdout and Stderr are where a foreground worker's output goes: the
	// caller's own streams, so the developer watches the turn as it happens.
	// They are unused by the other two modes, which either background the
	// worker themselves or write its output into the instance. A nil pair on a
	// foreground launch falls back to this process's own streams.
	Stdout io.Writer
	Stderr io.Writer
}

// dispatchLaunch is the package-level launcher seam. Production wires it to
// realDispatchLaunch; tests substitute a fake to assert the constructed argv
// and cmd.Dir without spawning a real worker. It launches a worker rooted in
// the request's instance directory.
var dispatchLaunch = realDispatchLaunch

// realDispatchLaunch runs the launched agent's worker binary with cmd.Dir set
// to the instance directory, forwarding the pass-through as already-split
// discrete argv elements. The prompt is passed as a single argv element --
// never shell-interpolated -- so quotes, newlines, and metacharacters in it
// cannot inject a command (D8). That holds for every process model: it is a
// property of how the argv is built, not of how the process is started.
//
// Which of the three models runs is req.Mode, and nothing here re-derives it.
//
// The prompt arrives in two pieces. Body is the developer's text and is the
// only part a spill may move to a file; Prefix is niwa-authored text (today,
// the keep-alive arming instruction) that always rides the argv element. They
// are kept apart all the way down to here because the caller cannot know
// whether a spill will happen -- that depends on the composed length -- and
// once concatenated the distinction cannot be recovered without the exec layer
// knowing about the keep-alive feature.
//
// An empty prompt is rejected before any exec. The check binds to Body, NOT to
// the composed string: Prefix is a long constant whenever keep-alive is armed,
// so testing the pair would silently stop rejecting an empty task.
func realDispatchLaunch(ctx context.Context, req launchRequest) error {
	spec, instanceDir, prefix, body := req.Spec, req.InstanceDir, req.Prefix, req.Body
	if body == "" {
		return fmt.Errorf("dispatch: empty prompt")
	}
	// The spill runs BEFORE the assertions below, and this order is the whole
	// point. Reversed, an oversized prompt would be refused at the guard
	// instead of spilled, reinstating the wall on exactly the path this exists
	// to keep open.
	//
	// Two things trigger it. Length is the obvious one. A NUL is the other: an
	// argv element cannot carry one, so such a prompt dies at exec with an
	// opaque "invalid argument" -- a defect that predates the spill and is one
	// paste away, since the capture preserves raw control bytes deliberately.
	// A prompt argv cannot carry changes vehicle rather than failing, whatever
	// the reason argv cannot carry it. The file takes raw bytes, so the NUL
	// survives where the developer put it.
	prompt := prefix + body
	if len(prompt) > maxArgStringBytes || strings.ContainsRune(body, 0) {
		token, err := spillToken()
		if err != nil {
			return fmt.Errorf("dispatch: %w", err)
		}
		path, err := spillPrompt(instanceDir, token, body)
		if err != nil {
			return fmt.Errorf("dispatch: %w", err)
		}
		prompt = composeSpillPointer(prefix, path, body, token)
	}

	// Assertions, not user-facing checks. Under the spill above neither is
	// reachable in normal operation, which is the point: they guard against a
	// future prepend that forgets the spill decision, exactly as the size one
	// used to guard against a prepend that forgot the reserve.
	if len(prompt) > maxArgStringBytes {
		return fmt.Errorf("dispatch: prompt is %d bytes, over the %d-byte single-argument exec limit", len(prompt), maxArgStringBytes)
	}
	if i := strings.IndexByte(prompt, 0); i >= 0 {
		return fmt.Errorf("dispatch: prompt contains a NUL byte at offset %d; an argv element cannot carry one", i)
	}

	bin, err := exec.LookPath(spec.Binary)
	if err != nil {
		return fmt.Errorf("dispatch: %s binary not found in PATH", spec.Binary)
	}

	args := buildLaunchArgs(spec, req.Mode, instanceDir, prompt, req.Passthrough)
	// The parent environment by default, so the worker sees the same context
	// the supervisor does; a non-nil req.Env replaces it wholesale. See
	// launchRequest.Env.
	worker := os.Environ()
	if req.Env != nil {
		worker = req.Env
	}

	switch req.Mode {
	case agentplan.LaunchDetached:
		return startDetachedWorker(spec, bin, args, instanceDir, worker)
	case agentplan.LaunchForeground:
		return runForegroundWorker(spec, bin, args, instanceDir, worker, req.Stdout, req.Stderr)
	case agentplan.LaunchBackgrounded:
		// The binary backgrounds its own worker and exits, so the process
		// started here is the hand-off rather than the work. Waiting for it
		// costs the hand-off and its exit status says whether that happened.
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = instanceDir
		cmd.Env = worker
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("dispatch: launching %s: %w", spec.Binary, err)
		}
		return nil
	default:
		// Named rather than folded into the backgrounded branch, because the
		// zero LaunchMode used to land there silently. A caller that forgets to
		// set Mode would then run a foreground agent's whole turn under
		// cmd.Run() with no stdout or stderr wired -- every byte discarded, the
		// command blocking for the length of the task with nothing on screen.
		// The dead-field guard proves each field is read, not that each call
		// site sets it, so this is the check that covers the difference.
		return fmt.Errorf("dispatch: launch mode %d is not a process model this launcher implements", req.Mode)
	}
}

// runForegroundWorker runs the worker's whole turn in the caller's terminal and
// does not return until it ends. For a runner that executes the turn in the
// foreground natively, this is what attaching to the session would have been:
// the developer watches the work rather than being told where to find it.
//
// Three things carry over from the detached path, each measured rather than
// chosen for tidiness. They are also the three an implementation that reaches
// for "inherit stdio and be done" drops.
//
// Stdin is /dev/null, even here. The measured hang is on stdin specifically: an
// agent that reads it in addition to its prompt blocks on an inherited one --
// twenty seconds with no output, no session record, and no request made.
// Attaching the terminal's stdout and stderr does not require attaching its
// stdin, and a foreground worker that hangs is a worse outcome than the
// detached one it replaces.
//
// The prompt is one argv element, built by the same builder as every other
// mode.
//
// And the exit status is not read as task success. A read-only sandbox failure
// exits 0, so a run that ended is all this can honestly report: an exit code is
// returned to the caller as "the turn ended", never as "it worked". A process
// that could not be started at all is a different thing and is reported as an
// error, because then no turn ran.
//
// What differs from the detached path is deliberate on both counts. The context
// is not passed to the command, for the reason startDetachedWorker gives: it is
// cancelled when the dispatch returns, and here the dispatch does not return
// until the worker is done anyway. And there is no Setsid: the worker shares
// the caller's process group, so Ctrl-C reaches it. That is what a developer
// expects of a command running in front of them, and it is the exact property
// Setsid takes away from the detached one.
func runForegroundWorker(spec agentplan.LaunchSpec, bin string, args []string, instanceDir string, env []string, stdout, stderr io.Writer) error {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("dispatch: opening %s for the worker's stdin: %w", os.DevNull, err)
	}
	defer devNull.Close()

	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	cmd := exec.Command(bin, args...)
	cmd.Dir = instanceDir
	cmd.Env = env
	cmd.Stdin = devNull
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// The turn ran and ended non-zero. That is not a failed launch and
			// it is not a failed task either -- this agent's exit code says
			// neither -- and treating it as a launch failure would roll the
			// instance back out from under work that may well have happened.
			// The developer watched the run; whatever it said is on their
			// terminal already.
			return nil
		}
		return fmt.Errorf("dispatch: launching %s: %w", spec.Binary, err)
	}
	return nil
}

// startDetachedWorker starts a worker that runs its whole turn in the
// foreground, and lets go of it.
//
// Three things here are load-bearing rather than tidy, and each was measured.
//
// The context is not passed to the command. A context cancelled when the
// dispatch returns -- which is every dispatch -- would kill the worker the
// instant the launch finished. The backgrounded path can use it precisely
// because the process it starts is not the worker.
//
// Stdin is /dev/null. An agent that reads stdin in addition to its prompt
// blocks forever on an inherited or open one: measured at twenty seconds with
// no output, no session record, and no request made, which is a hang with
// nothing on disk to diagnose it by.
//
// The worker gets its own session (Setsid), so a signal sent to whatever
// process group the dispatch was started in does not reach it. That is what
// makes it survive the terminal it was launched from.
//
// Its output goes to files inside the instance rather than to a pipe nobody
// drains or a terminal that will not be there. A worker that could not do its
// work leaves a diagnosable trail either way, and the stream stays separated:
// merging stderr into a structured stdout would corrupt it, and a healthy run
// writes to both.
func startDetachedWorker(spec agentplan.LaunchSpec, bin string, args []string, instanceDir string, env []string) error {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("dispatch: opening %s for the worker's stdin: %w", os.DevNull, err)
	}
	defer devNull.Close()

	stdout, err := openWorkerLog(instanceDir, spec.Binary, "out")
	if err != nil {
		return err
	}
	defer stdout.Close()
	stderr, err := openWorkerLog(instanceDir, spec.Binary, "err")
	if err != nil {
		return err
	}
	defer stderr.Close()

	cmd := exec.Command(bin, args...)
	cmd.Dir = instanceDir
	cmd.Env = env
	cmd.Stdin = devNull
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("dispatch: launching %s: %w", spec.Binary, err)
	}
	// Release rather than Wait: the worker outlives this process by design, and
	// nothing here is waiting to learn anything from its exit status -- which
	// for at least one agent does not report whether the work succeeded anyway.
	return cmd.Process.Release()
}

// maxWorkerLogTailLines bounds how much of a failed worker's own output is
// echoed. Enough to carry a startup error and the line before it; short enough
// that a worker which failed after producing real output does not bury the
// error that follows.
const maxWorkerLogTailLines = 12

// readWorkerLogTail returns the tail of a detached worker's own output, for the
// one moment it is about to be unavailable: a capture failure rolls the
// instance back, and the log lives inside the instance.
//
// It prefers the error stream, because a worker that died before writing a
// session record died at startup and said so there, and falls back to the
// standard stream for a worker that failed later with something structured to
// say. An agent whose launch is not detached keeps no such log and gets an
// empty string, which the caller renders as nothing rather than as an absence.
func readWorkerLogTail(instanceDir, binary string) string {
	for _, stream := range []string{"err", "out"} {
		path := filepath.Join(instanceDir, ".niwa", fmt.Sprintf("dispatch-%s.%s", binary, stream))
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) == 1 && lines[0] == "" {
			continue
		}
		if len(lines) > maxWorkerLogTailLines {
			lines = lines[len(lines)-maxWorkerLogTailLines:]
		}
		return "  " + strings.Join(lines, "\n  ")
	}
	return ""
}

// openWorkerLog opens one of a detached worker's output files inside the
// instance, truncating any earlier one. The name carries the binary rather than
// a fixed string so two agents' logs could never be confused for each other,
// and so this function names no agent.
func openWorkerLog(instanceDir, binary, stream string) (*os.File, error) {
	dir := filepath.Join(instanceDir, ".niwa")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("dispatch: preparing the worker's log directory: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("dispatch-%s.%s", binary, stream))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("dispatch: opening the worker's %s log: %w", stream, err)
	}
	return f, nil
}

// buildLaunchArgs builds the discrete argv (excluding the binary) for a launch.
// Order: the agent's own leading arguments, then the arguments that ride the
// detached path only, then the grant for the working directory if it declares
// one, then the pass-through flags (already split into discrete elements by the
// caller), then an optional bare "--" for an agent whose parser would otherwise
// read a prompt beginning with a dash as a flag, then the prompt as the final
// single element.
//
// The mode reaches in for one reason: an argument that exists so niwa can parse
// a log nobody is watching has no business on a run the developer is watching.
// Everything else is identical across the modes -- the policy flags, the grant,
// the separator, the prompt -- because everything else is about what the agent
// needs to do the work rather than about who reads the output.
//
// The grant comes before the pass-through so that a developer who asks for a
// posture explicitly gets the last word on it.
//
// Returning each value as its own slice element -- and never concatenating into
// a command line -- is what prevents a crafted prompt or pass-through value from
// smuggling in an extra flag (D8, security note 1). It is a pure helper so the
// argv contract is unit-testable without exec, and it reads the agent's spec
// rather than naming one, so the same test drives it for any agent.
func buildLaunchArgs(spec agentplan.LaunchSpec, mode agentplan.LaunchMode, workdir, prompt string, passthrough []string) []string {
	args := make([]string, 0, len(spec.LeadingArgs)+len(spec.DetachedArgs)+len(spec.WorkdirGrantArgs)+len(passthrough)+2)
	args = append(args, spec.LeadingArgs...)
	if mode == agentplan.LaunchDetached {
		args = append(args, spec.DetachedArgs...)
	}
	args = append(args, formatWorkdirGrant(spec, workdir)...)
	args = append(args, passthrough...)
	if spec.PromptSeparator {
		args = append(args, "--")
	}
	args = append(args, prompt)
	return args
}

// formatWorkdirGrant renders the arguments that grant this invocation the
// posture niwa intends for a directory it owns, substituting the working
// directory into the single verb the declaration leaves for it. An agent that
// declares no grant gets nothing, and an empty working directory gets nothing
// either -- a grant naming no directory would either fail to parse or grant
// something nobody asked for.
func formatWorkdirGrant(spec agentplan.LaunchSpec, workdir string) []string {
	if len(spec.WorkdirGrantArgs) == 0 || workdir == "" {
		return nil
	}
	out := slices.Clone(spec.WorkdirGrantArgs)
	last := len(out) - 1
	out[last] = fmt.Sprintf(out[last], workdir)
	return out
}
