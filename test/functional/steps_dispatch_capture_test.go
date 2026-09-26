package functional

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// iRunWithStdinHeldOpen runs a command with standard input attached to a pipe
// that is never written to and never closed.
//
// This is the only shape that can fail for the right reason. The ordinary run
// helper leaves standard input at /dev/null, so a command that violates the
// never-block guarantee by reading standard input receives an immediate
// end-of-input and exits anyway -- the scenario passes against a broken
// implementation. A pipe held open distinguishes "did not read" from "read and
// got end-of-input": only the former terminates.
func iRunWithStdinHeldOpen(ctx context.Context, command string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}

	args := strings.Fields(command)
	if len(args) > 0 && args[0] == "niwa" {
		args[0] = s.binPath
	}

	runCtx, cancel := context.WithTimeout(ctx, ptyStepTimeout)
	defer cancel()

	pr, pw, err := os.Pipe()
	if err != nil {
		return ctx, fmt.Errorf("creating held-open stdin pipe: %w", err)
	}
	// The write end stays open for the command's whole lifetime, so the read end
	// never sees end-of-input.
	defer pw.Close()
	defer pr.Close()

	cmd := exec.CommandContext(runCtx, args[0], args[1:]...)
	cmd.Dir = s.workspaceRoot
	cmd.Env = s.buildEnv()
	cmd.Stdin = pr
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	if runCtx.Err() == context.DeadlineExceeded {
		return ctx, fmt.Errorf(
			"%q did not terminate within %s with standard input held open; it is reading input that will never arrive",
			command, ptyStepTimeout)
	}

	s.stdout = stdout.String()
	s.stderr = stderr.String()
	s.shellPwd = ""
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			s.exitCode = exitErr.ExitCode()
			_ = elapsed
			return ctx, nil
		}
		return ctx, fmt.Errorf("command execution failed: %w; stderr: %s", runErr, s.stderr)
	}
	s.exitCode = 0
	return ctx, nil
}
