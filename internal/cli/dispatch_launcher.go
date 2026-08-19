package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

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

	args := buildLaunchArgs(spec, prompt, passthrough)
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
		return fmt.Errorf("dispatch: launching %s: %w", spec.Binary, err)
	}
	return nil
}

// buildLaunchArgs builds the discrete argv (excluding the binary) for a
// background launch. Order: the agent's own leading arguments first, then the
// pass-through flags (already split into discrete elements by the caller), then
// an optional bare "--" for an agent whose parser would otherwise read a prompt
// beginning with a dash as a flag, then the prompt as the final single element.
//
// Returning each value as its own slice element -- and never concatenating into
// a command line -- is what prevents a crafted prompt or pass-through value from
// smuggling in an extra flag (D8, security note 1). It is a pure helper so the
// argv contract is unit-testable without exec, and it reads the agent's spec
// rather than naming one, so the same test drives it for any agent.
func buildLaunchArgs(spec agentplan.LaunchSpec, prompt string, passthrough []string) []string {
	args := make([]string, 0, len(spec.LeadingArgs)+len(passthrough)+2)
	args = append(args, spec.LeadingArgs...)
	args = append(args, passthrough...)
	if spec.PromptSeparator {
		args = append(args, "--")
	}
	args = append(args, prompt)
	return args
}
