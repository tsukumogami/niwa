package removal

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// GitHubToken returns the token niwa destroy's pull request lookups use:
// GITHUB_TOKEN, then GH_TOKEN, and otherwise what gh auth token prints,
// trimmed. gh is the one found on the filtered PATH, so a gh only a
// directory inside the workspace root holds never runs. It is empty when no
// token is set and gh is missing or fails, and then the lookup goes
// unauthenticated or fails and the branch is listed as unverified.
func (e *Env) GitHubToken(ctx context.Context) string {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}
	if token := os.Getenv("GH_TOKEN"); token != "" {
		return token
	}
	ctx, cancel := context.WithTimeout(ctx, networkTimeout)
	defer cancel()
	cmd, err := e.ghCommand(ctx)
	if err != nil {
		return ""
	}
	out, err := run(cmd)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ghCommand builds the gh auth token call: by the absolute path resolved
// from the filtered PATH, with that PATH in its environment, run from the
// empty net directory under the pass's scratch directory, checked for owner
// and mode first, so gh never starts in an instance's repository. The rest of the
// environment is the person's, since gh reads their configuration and
// keychain. The process runs in its own group, which the context's end
// kills.
func (e *Env) ghCommand(ctx context.Context) (*exec.Cmd, error) {
	if e == nil || e.Gh == "" {
		return nil, errors.New("no gh on the filtered PATH")
	}
	dir, err := e.scratchDir(scratchNet)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, e.Gh, "auth", "token")
	cmd.Dir = dir
	cmd.Env = replaceEnv(os.Environ(), "PATH", e.PATH)
	groupKill(cmd)
	return cmd, nil
}
