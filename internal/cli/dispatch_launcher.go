package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// dispatchLaunch is the package-level launcher seam. Production wires it to
// realDispatchLaunch; tests substitute a fake to assert the constructed argv
// and cmd.Dir without spawning a real claude. It launches a background worker
// rooted in instanceDir.
var dispatchLaunch = realDispatchLaunch

// realDispatchLaunch runs `claude --bg <prompt>` with cmd.Dir set to
// instanceDir, forwarding passthrough as already-split discrete argv elements.
// It generalizes the exec pattern in internal/cli/sessionattach/supervise.go:
// the worker is daemon-backed, so this does not capture stdout (identity is
// recovered by jobs-dir cwd correlation in dispatch_capture.go). The prompt is
// passed as a single argv element -- never shell-interpolated -- so quotes,
// newlines, and metacharacters in it cannot inject a command (D8).
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
func realDispatchLaunch(ctx context.Context, instanceDir, prefix, body string, passthrough, env []string) error {
	if body == "" {
		return fmt.Errorf("dispatch: empty prompt")
	}
	prompt := prefix + body
	// Backstop, not the user-facing check: runDispatch step (1) is where an
	// oversized prompt is supposed to be rejected, early and with advice. This
	// guard sits on the string that actually reaches execve, so if something is
	// ever prepended without being counted in dispatchPromptReserve the failure
	// names the limit instead of surfacing as an opaque E2BIG.
	if len(prompt) > maxArgStringBytes {
		return fmt.Errorf("dispatch: prompt is %d bytes, over the %d-byte single-argument exec limit", len(prompt), maxArgStringBytes)
	}

	bin, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("dispatch: claude binary not found in PATH")
	}

	args := buildClaudeBgArgs(prompt, passthrough)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = instanceDir
	if env == nil {
		// Inherit the parent environment so the worker sees the same context the
		// supervisor does (mirrors the sessionattach supervisor).
		cmd.Env = os.Environ()
	} else {
		cmd.Env = env
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dispatch: launching claude --bg: %w", err)
	}
	return nil
}

// buildClaudeBgArgs builds the discrete argv (excluding the binary) for a
// background launch. Order: --bg first, then the pass-through flags (already
// split into discrete elements by the caller), then the prompt as the final
// single element. Returning each value as its own slice element -- and never
// concatenating into a command line -- is what prevents a crafted prompt or
// pass-through value from smuggling in an extra claude flag (D8, security
// note 1). It is a pure helper so the argv contract is unit-testable without
// exec.
func buildClaudeBgArgs(prompt string, passthrough []string) []string {
	args := make([]string, 0, 2+len(passthrough))
	args = append(args, "--bg")
	args = append(args, passthrough...)
	args = append(args, prompt)
	return args
}
