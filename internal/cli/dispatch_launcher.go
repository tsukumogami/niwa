package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// dispatchLaunch is the package-level launcher seam. Production wires it to
// realDispatchLaunch; tests substitute a fake to assert the constructed argv
// and cmd.Dir without spawning a real worker. It launches a background worker
// rooted in instanceDir.
var dispatchLaunch = realDispatchLaunch

// realDispatchLaunch runs the launched agent's worker binary with cmd.Dir set
// to instanceDir, forwarding passthrough as already-split discrete argv elements.
// It generalizes the exec pattern in internal/cli/sessionattach/supervise.go:
// the worker backgrounds itself, so this does not capture stdout (identity is
// recovered by correlating the agent's session records against instanceDir in
// dispatch_capture.go). The prompt is passed as a single argv element -- never
// shell-interpolated -- so quotes, newlines, and metacharacters in it cannot
// inject a command (D8).
//
// env selects the worker's environment: a nil env inherits the full parent
// environment (os.Environ()), the behavior every ordinary dispatch relies on;
// a non-nil env is used verbatim, which is how the contained watch path passes
// an allowlisted, credential-scrubbed environment. Passing an explicit env is
// the ONLY way the worker's environment differs from the supervisor's.
//
// The prompt arrives in two pieces. body is the developer's text and is the
// only part a spill may move to a file; prefix is niwa-authored text (today,
// the keep-alive arming instruction) that always rides the argv element. They
// are kept apart all the way down to here because the caller cannot know
// whether a spill will happen -- that depends on the composed length -- and
// once concatenated the distinction cannot be recovered without the exec layer
// knowing about the keep-alive feature.
//
// An empty prompt is rejected before any exec. The check binds to body, NOT to
// the composed string: prefix is a long constant whenever keep-alive is armed,
// so testing the pair would silently stop rejecting an empty task.
func realDispatchLaunch(ctx context.Context, spec agentplan.LaunchSpec, instanceDir, prefix, body string, passthrough, env []string) error {
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

	args := buildLaunchArgs(spec, instanceDir, prompt, passthrough)
	worker := os.Environ()
	if env != nil {
		// Inherit the parent environment so the worker sees the same context
		// the supervisor does (mirrors the sessionattach supervisor); a
		// non-nil env replaces it wholesale, which is how the contained watch
		// path passes an allowlisted, credential-scrubbed one.
		worker = env
	}

	switch spec.Mode {
	case agentplan.LaunchDetached:
		return startDetachedWorker(spec, bin, args, instanceDir, worker)
	default:
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
	}
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

// buildLaunchArgs builds the discrete argv (excluding the binary) for a
// background launch. Order: the agent's own leading arguments, then the grant
// for the working directory if it declares one, then the pass-through flags
// (already split into discrete elements by the caller), then an optional bare
// "--" for an agent whose parser would otherwise read a prompt beginning with a
// dash as a flag, then the prompt as the final single element.
//
// The grant comes before the pass-through so that a developer who asks for a
// posture explicitly gets the last word on it.
//
// Returning each value as its own slice element -- and never concatenating into
// a command line -- is what prevents a crafted prompt or pass-through value from
// smuggling in an extra flag (D8, security note 1). It is a pure helper so the
// argv contract is unit-testable without exec, and it reads the agent's spec
// rather than naming one, so the same test drives it for any agent.
func buildLaunchArgs(spec agentplan.LaunchSpec, workdir, prompt string, passthrough []string) []string {
	args := make([]string, 0, len(spec.LeadingArgs)+len(spec.WorkdirGrantArgs)+len(passthrough)+2)
	args = append(args, spec.LeadingArgs...)
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
