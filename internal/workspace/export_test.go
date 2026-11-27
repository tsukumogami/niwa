package workspace

import (
	"context"
	"os"
	"os/exec"
)

// plainGitCommand builds a scan's git calls as git from PATH with the
// inherited environment, run with -C dir. Its status asks for untracked
// files explicitly, so a repository's status.showUntrackedFiles=no can't
// hide them. Only tests use it: a scan in niwa itself is always handed its
// builder through WithGitCommand.
func plainGitCommand(ctx context.Context, dir string, env []string, args ...string) (*exec.Cmd, error) {
	argv := append([]string{"-C", dir}, args...)
	if len(args) > 0 && args[0] == "status" {
		argv = append(argv, "--untracked-files=normal")
	}
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Env = append(os.Environ(), env...)
	return cmd, nil
}

// withPlainGit makes the scan build its git calls with plainGitCommand. It
// leaves liveDefaults unset, so a remote's default branch is judged at the
// local remote-tracking ref after a fetch into the repository, which these
// tests' local remotes and insteadOf rewrites rely on.
func withPlainGit() ScanOption {
	return func(c *scanConfig) { c.git = plainGitCommand }
}
