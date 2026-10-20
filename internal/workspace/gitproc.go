package workspace

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

// WithDisposableGit returns ctx marked so that the network git commands this
// package starts under it stop completely when ctx is done, not just git.
//
// Killing git alone is not enough. git runs an https fetch in a remote helper
// (git-remote-https) that it doesn't stop when it is killed, and the helper
// holds git's stderr open, so the Wait on git doesn't return until the helper's
// own connect timeout ends it, minutes later. Under the mark each git command
// leads a session of its own and cancelling ctx kills that whole process group.
//
// It is opt-in because the session takes git off the terminal. A Ctrl-C at the
// terminal no longer reaches git directly, so a caller that marks ctx has to
// cancel it on the signals it wants honoured (niwa dispatch cancels on SIGINT
// and SIGTERM). And git can't prompt there: with no controlling terminal, a
// credential or ssh prompt fails at once rather than waiting on a keyboard. A
// process group alone would leave git on the terminal's session but off its
// foreground group, where reading /dev/tty stops the process instead.
// GIT_TERMINAL_PROMPT=0 makes git say why the prompt failed.
func WithDisposableGit(ctx context.Context) context.Context {
	return context.WithValue(ctx, disposableGitKey{}, true)
}

// gitWaitDelay bounds how long a disposable git command's Wait waits for its
// output pipes once its process group has been killed.
const gitWaitDelay = 3 * time.Second

// gitCommand returns exec.CommandContext(ctx, "git", args...), set up as
// WithDisposableGit describes when ctx carries its mark. Without the mark the
// command is exactly what exec.CommandContext returns.
func gitCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	if disposable, _ := ctx.Value(disposableGitKey{}).(bool); !disposable {
		return cmd
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		// Setsid makes git's pid its process group id.
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = gitWaitDelay
	return cmd
}
