package mirror

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// disposableGitKey is the context key WithDisposableGit sets.
type disposableGitKey struct{}

// WithDisposableGit returns ctx marked so that every git process a mirror
// call starts under it stops completely when ctx is done, its remote helper
// included: under the mark each git process leads a session of its own, and
// cancelling ctx sends SIGTERM to that whole process group, then SIGKILL a
// second later. Without it, cancelling kills git alone, and an https fetch's
// git-remote-https outlives it until its own connect timeout. It's the
// mirror's side of workspace.WithDisposableGit, which sets both marks, so a
// caller that cancels its context on SIGTERM, such as niwa dispatch, leaves
// no mirror git process behind either. Every mirror call already runs with
// GIT_TERMINAL_PROMPT=0, so losing the terminal changes no prompt.
func WithDisposableGit(ctx context.Context) context.Context {
	return context.WithValue(ctx, disposableGitKey{}, true)
}

// disposableGit reports whether ctx carries WithDisposableGit's mark.
func disposableGit(ctx context.Context) bool {
	marked, _ := ctx.Value(disposableGitKey{}).(bool)
	return marked
}

// gitKillDelay is how long a cancelled git process group has to end on
// SIGTERM before it is sent SIGKILL; gitWaitDelay bounds the wait for its
// output pipes after that, and is longer, so the group is killed first.
const (
	gitKillDelay = time.Second
	gitWaitDelay = 3 * time.Second
)

// makeDisposable sets cmd up as WithDisposableGit describes when ctx carries
// the mark, and leaves it as exec.CommandContext made it otherwise.
func makeDisposable(ctx context.Context, cmd *exec.Cmd) {
	if !disposableGit(ctx) {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		// Setsid makes git's pid its process group id. TERM first, so git
		// removes its lock files; KILL follows for anything that ignored it.
		pgid := cmd.Process.Pid
		err := syscall.Kill(-pgid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		time.AfterFunc(gitKillDelay, func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
		return err
	}
	cmd.WaitDelay = gitWaitDelay
}
