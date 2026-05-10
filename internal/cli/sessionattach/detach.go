package sessionattach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// DetachOptions configures Run for the niwa session detach command.
type DetachOptions struct {
	InstanceRoot string
	SessionID    string
	Force        bool
	Stdout       io.Writer
	Stderr       io.Writer
	// GraceSeconds overrides the default 5-second SIGTERM-to-SIGKILL grace
	// period; primarily for tests. When 0, defaults to NIWA_DESTROY_GRACE_SECONDS
	// or 5.
	GraceSeconds int
}

// DetachExitCode is returned by Run wrapped in an *ExitCodeError so the
// cobra layer can map it to os.Exit. Using a sentinel error type avoids
// abusing fmt.Errorf strings to convey intent.
type ExitCodeError struct {
	Code int
	Msg  string
}

func (e *ExitCodeError) Error() string { return e.Msg }

// DetachRun executes the niwa session detach <id> [--force] command.
//
//   - When no sentinel exists: returns nil (idempotent; nothing to break).
//   - When the holder PID is dead: removes the sentinel and returns nil.
//     This is the auto-recovery path; no --force needed.
//   - When the holder PID is alive and Force == false: returns *ExitCodeError
//     with Code=3 and the PRD R3 lock-contention message.
//   - When the holder PID is alive and Force == true: SIGTERMs the holder,
//     waits the grace period, SIGKILLs if needed, removes the sentinel,
//     emits the warning line per PRD R9, and returns *ExitCodeError with
//     Code=4 (signals "killed live holder" per Exit Code Mapping).
func DetachRun(ctx context.Context, opts DetachOptions) error {
	stderr := stderrOrDefault(opts.Stderr)

	worktreePath, err := worktreePathForSession(opts.InstanceRoot, opts.SessionID)
	if err != nil {
		return &ExitCodeError{Code: 1, Msg: err.Error()}
	}

	state, avail, err := mcp.ReadAttachState(worktreePath, false /* don't reap yet; we may need to act */)
	if err != nil {
		// Treat read errors as non-blocking: report but exit 0 (the lock is
		// not actively held by anyone we can identify).
		fmt.Fprintf(stderr, "warning: could not read attach state: %v\n", err)
		return nil
	}

	switch avail {
	case mcp.AttachAvailable:
		// Nothing to break.
		return nil
	case mcp.AttachStale:
		if err := mcp.RemoveAttachState(worktreePath); err != nil {
			return &ExitCodeError{Code: 1, Msg: fmt.Sprintf("niwa: error: removing stale attach state: %v", err)}
		}
		return nil
	case mcp.AttachAttached:
		if !opts.Force {
			return &ExitCodeError{
				Code: 3,
				Msg: fmt.Sprintf(
					"niwa: error: session %s is currently attached (pid=%d, started=%s). "+
						"Run `niwa session detach %s --force` to break the lock.",
					opts.SessionID, state.OwnerPID, state.StartedAt, opts.SessionID,
				),
			}
		}
		// Live holder + --force: SIGTERM, wait, SIGKILL, remove sentinel.
		fmt.Fprintf(stderr, "warning: detaching live attach holder pid=%d started=%s\n",
			state.OwnerPID, state.StartedAt)
		grace := graceDuration(opts.GraceSeconds)
		_ = syscall.Kill(state.OwnerPID, syscall.SIGTERM)
		if !waitForExit(state.OwnerPID, state.OwnerStartTime, grace) {
			_ = syscall.Kill(state.OwnerPID, syscall.SIGKILL)
		}
		if err := mcp.RemoveAttachState(worktreePath); err != nil {
			return &ExitCodeError{Code: 1, Msg: fmt.Sprintf("niwa: error: removing attach state: %v", err)}
		}
		return &ExitCodeError{Code: 4, Msg: ""} // Code 4 = killed live holder
	}
	return nil
}

// worktreePathForSession reads the lifecycle state file for sessionID under
// instanceRoot and returns the worktree path. Returns a niwa-shaped error
// when the session is not found or the file is corrupt.
func worktreePathForSession(instanceRoot, sessionID string) (string, error) {
	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	state, err := mcp.ReadSessionLifecycleState(sessionsDir, sessionID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("niwa: error: session %s not found", sessionID)
		}
		return "", fmt.Errorf("niwa: error: reading session state %s: %w", sessionID, err)
	}
	return state.WorktreePath, nil
}

// waitForExit polls IsPIDAlive until the process is dead or the grace period
// elapses. Returns true when the process exited within grace.
func waitForExit(pid int, startTime int64, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for time.Now().Before(deadline) {
		if !mcp.IsPIDAlive(pid, startTime) {
			return true
		}
		<-tick.C
	}
	return !mcp.IsPIDAlive(pid, startTime)
}

// graceDuration resolves the SIGTERM-to-SIGKILL grace period. Caller-supplied
// override > NIWA_DESTROY_GRACE_SECONDS env var > 5s default.
func graceDuration(override int) time.Duration {
	if override > 0 {
		return time.Duration(override) * time.Second
	}
	if env := os.Getenv("NIWA_DESTROY_GRACE_SECONDS"); env != "" {
		var n int
		if _, err := fmt.Sscanf(env, "%d", &n); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 5 * time.Second
}

// Suppress unused-context-arg lint warning (we accept ctx for parity with
// future tickets that may need cancellation).
var _ = context.Background
