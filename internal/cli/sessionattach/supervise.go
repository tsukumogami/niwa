package sessionattach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// SuperviseOptions configures Supervise. Stdio fields default to os.Stdin /
// os.Stdout / os.Stderr when nil so production callers don't have to wire
// them up.
type SuperviseOptions struct {
	ClaudeBin string // when "", looked up via exec.LookPath("claude")
	ConvID    string
	WorkerCWD string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
}

// Supervise spawns claude --resume <ConvID> as a child process with
// cmd.Dir = WorkerCWD and stdio inherited from the supplied (or os.*)
// streams. Blocks until the child exits, then returns the propagated exit
// code, capped at 125 (reserves 126/127/128+ for shell semantics per PRD R7
// and the Exit Code Mapping).
//
// Signals received while the child is running are forwarded to the child's
// process group so Ctrl-C reaches Claude Code rather than being eaten by
// niwa's signal handler.
func Supervise(ctx context.Context, opts SuperviseOptions) (int, error) {
	bin := opts.ClaudeBin
	if bin == "" {
		var err error
		bin, err = exec.LookPath("claude")
		if err != nil {
			return 1, fmt.Errorf("niwa: error: claude binary not found in PATH (set $PATH or pass --claude-bin)")
		}
	}
	cmd := exec.CommandContext(ctx, bin, "--resume", opts.ConvID)
	cmd.Dir = opts.WorkerCWD
	cmd.Stdin = stdinOrDefault(opts.Stdin)
	cmd.Stdout = stdoutOrDefault(opts.Stdout)
	cmd.Stderr = stderrOrDefault(opts.Stderr)
	// Place the child in its own process group so we can target the group on
	// signal forwarding; matches how niwa spawns workers (Setsid=true makes
	// PID == PGID).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return 1, fmt.Errorf("niwa: error: starting claude: %w", err)
	}

	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigCh)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	for {
		select {
		case sig := <-sigCh:
			// Forward to the child's process group. Ignore errors -- the child
			// may already be exiting.
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, sig.(syscall.Signal))
			}
		case err := <-done:
			return exitCodeFromWaitErr(err), nil
		}
	}
}

func stdinOrDefault(r io.Reader) io.Reader {
	if r != nil {
		return r
	}
	return os.Stdin
}

func stdoutOrDefault(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return os.Stdout
}

func stderrOrDefault(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return os.Stderr
}

// exitCodeFromWaitErr returns the propagated exit code from cmd.Wait()'s
// error, capped at 125. nil error returns 0.
func exitCodeFromWaitErr(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code := ee.ExitCode()
		if code < 0 {
			return 1 // signaled; collapse to generic-failure
		}
		if code > 125 {
			return 125
		}
		return code
	}
	return 1
}
