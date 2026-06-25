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
// An empty prompt is rejected before any exec (R43).
func realDispatchLaunch(ctx context.Context, instanceDir, prompt string, passthrough []string) error {
	if prompt == "" {
		return fmt.Errorf("dispatch: empty prompt")
	}

	bin, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("dispatch: claude binary not found in PATH")
	}

	args := buildClaudeBgArgs(prompt, passthrough)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = instanceDir
	// Inherit the parent environment so the worker sees the same context the
	// supervisor does (mirrors the sessionattach supervisor).
	cmd.Env = os.Environ()

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
