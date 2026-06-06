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

	"github.com/tsukumogami/niwa/internal/worktree"
)

// Options configures Run for the niwa session attach command.
type Options struct {
	InstanceRoot string
	SessionID    string
	ClaudeBin    string // empty = look up via PATH
	HomeDir      string // empty = derived from os.UserHomeDir
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer

	// SuperviseFn allows tests to inject a stub for the claude exec.
	SuperviseFn func(context.Context, SuperviseOptions) (int, error)
}

// AttachRun executes the niwa session attach <id> command per the design
// doc's happy-path sequence diagram. Returns the propagated Claude exit code
// (or a niwa-side exit code per the Exit Code Mapping) wrapped in an
// *ExitCodeError. Returns nil only when nothing went wrong AND claude exited
// with code 0.
func AttachRun(ctx context.Context, opts Options) error {
	stderr := stderrOrDefault(opts.Stderr)

	// Step 0: resolve home dir for transcript path computation.
	if opts.HomeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return &ExitCodeError{Code: 1, Msg: fmt.Sprintf("niwa: error: cannot resolve home directory: %v", err)}
		}
		opts.HomeDir = home
	}

	// Step 1: read lifecycle state, validate status == active.
	sessionsDir := filepath.Join(opts.InstanceRoot, ".niwa", "sessions")
	state, err := worktree.ReadSessionLifecycleState(sessionsDir, opts.SessionID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &ExitCodeError{Code: 1, Msg: fmt.Sprintf("niwa: error: session %s not found", opts.SessionID)}
		}
		// EACCES on the state file means cross-UID access (PRD R26 wrapper).
		if errors.Is(err, fs.ErrPermission) {
			return uidMismatchError(sessionsDir, opts.SessionID)
		}
		return &ExitCodeError{Code: 1, Msg: fmt.Sprintf("niwa: error: reading session state %s: %v", opts.SessionID, err)}
	}
	if state.Status != worktree.SessionStatusActive {
		hint := ""
		if state.Status == worktree.SessionStatusEnded {
			hint = " (For ended sessions, the worktree was removed on destroy; create a new session instead.)"
		}
		return &ExitCodeError{
			Code: 1,
			Msg: fmt.Sprintf(
				"niwa: error: session %s has status %s; attach requires status active.%s",
				opts.SessionID, state.Status, hint,
			),
		}
	}

	// Step 2: acquire flock, with stale-sentinel recovery retry.
	lockPath := worktree.AttachLockPath(state.WorktreePath)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return &ExitCodeError{Code: 1, Msg: fmt.Sprintf("niwa: error: ensuring .niwa dir: %v", err)}
	}
	lockFile, lockErr := acquireAttachLock(lockPath)
	if lockErr == errLockHeld {
		// Maybe stale -- read sentinel and retry once if reapable.
		_, avail, _ := worktree.ReadAttachState(state.WorktreePath, true /* reap */)
		if avail == worktree.AttachStale {
			lockFile, lockErr = acquireAttachLock(lockPath)
		}
		if lockErr == errLockHeld {
			st, _, _ := worktree.ReadAttachState(state.WorktreePath, false)
			if st != nil {
				return &ExitCodeError{
					Code: 3,
					Msg: fmt.Sprintf(
						"niwa: error: session %s is already attached (pid=%d, started=%s). "+
							"Run `niwa session detach %s --force` to break the lock if the holder is gone.",
						opts.SessionID, st.OwnerPID, st.StartedAt, opts.SessionID,
					),
				}
			}
			return &ExitCodeError{Code: 3, Msg: fmt.Sprintf("niwa: error: session %s attach lock is held", opts.SessionID)}
		}
	}
	if lockErr != nil {
		return &ExitCodeError{Code: 1, Msg: fmt.Sprintf("niwa: error: acquiring attach lock: %v", lockErr)}
	}
	// Lock acquired. Ensure release on every return path.
	defer func() {
		_ = lockFile.Close() // releases the flock as a side-effect of fd close
	}()

	// Step 3 (removed): there is no longer a mesh task store to consult for a
	// running worker, so attach no longer waits for or force-kills one. The
	// attach lock acquired in Step 2 (flock + attach.state, with PID-liveness
	// via the worktree package) is the sole and sufficient safety signal that
	// no other live process is using this worktree.

	// Step 4: preflight transcript validation.
	workerCWD := filepath.Join(state.WorktreePath, state.Repo)
	if err := Preflight(state, PreflightOptions{HomeDir: opts.HomeDir, WorkerCWD: workerCWD}); err != nil {
		var pe *PreflightError
		if errors.As(err, &pe) {
			return &ExitCodeError{Code: 1, Msg: pe.Error()}
		}
		return &ExitCodeError{Code: 1, Msg: err.Error()}
	}

	// Cleanup defer registered BEFORE WriteAttachState so any error between
	// here and the supervise call still removes the attach sentinel and
	// surfaces the worktree warnings on detach.
	defer func() {
		_ = worktree.RemoveAttachState(state.WorktreePath)
		Warnings(state.WorktreePath, state.EffectiveBranchName(), stderr)
		fmt.Fprintf(stderr, "session: detached %s\n", opts.SessionID)
	}()

	// Step 5: write attach sentinel.
	myPID := os.Getpid()
	myStart, _ := worktree.PIDStartTime(myPID)
	startedAt := time.Now().UTC().Format(time.RFC3339)
	if err := worktree.WriteAttachState(state.WorktreePath, worktree.AttachState{
		V:              1,
		OwnerPID:       myPID,
		OwnerStartTime: myStart,
		StartedAt:      startedAt,
		LockPath:       ".niwa/attach.lock",
	}); err != nil {
		return &ExitCodeError{Code: 1, Msg: fmt.Sprintf("niwa: error: writing attach state: %v", err)}
	}

	fmt.Fprintf(stderr, "session: attached %s at %s\n", opts.SessionID, state.WorktreePath)

	// Step 7-8: spawn claude --resume <conv_id> and wait.
	supervise := opts.SuperviseFn
	if supervise == nil {
		supervise = Supervise
	}
	exitCode, supErr := supervise(ctx, SuperviseOptions{
		ClaudeBin: opts.ClaudeBin,
		ConvID:    state.ClaudeConversationID,
		WorkerCWD: workerCWD,
		Stdin:     opts.Stdin,
		Stdout:    opts.Stdout,
		Stderr:    opts.Stderr,
	})
	if supErr != nil {
		return &ExitCodeError{Code: 1, Msg: supErr.Error()}
	}
	if exitCode != 0 {
		return &ExitCodeError{Code: exitCode, Msg: ""} // empty msg => caller doesn't print extra
	}
	return nil
}

var errLockHeld = errors.New("attach lock held")

// acquireAttachLock opens the lock file with mode 0600 and takes a non-
// blocking exclusive flock. Returns errLockHeld on contention so callers
// can reason about it specifically.
func acquireAttachLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLockHeld
		}
		return nil, err
	}
	return f, nil
}

// uidMismatchError formats the cross-UID error per PRD R26. It best-efforts
// to fetch the file owner UID for the diagnostic; a stat failure produces a
// plainer message.
func uidMismatchError(sessionsDir, sessionID string) *ExitCodeError {
	myUID := os.Geteuid()
	target := filepath.Join(sessionsDir, sessionID+".json")
	info, err := os.Stat(target)
	if err == nil {
		if sysstat, ok := info.Sys().(*syscall.Stat_t); ok {
			return &ExitCodeError{
				Code: 1,
				Msg: fmt.Sprintf(
					"niwa: error: cannot attach to session owned by another user (file owner uid=%d, your uid=%d)",
					sysstat.Uid, myUID,
				),
			}
		}
	}
	// Could not stat (permission denied on the directory itself, etc.).
	return &ExitCodeError{
		Code: 1,
		Msg:  fmt.Sprintf("niwa: error: cannot attach to session (permission denied; current uid=%d)", myUID),
	}
}
