package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// CloneOptions controls clone behavior.
type CloneOptions struct {
	Ref   string // tag, branch, or commit SHA to checkout
	Depth int    // clone depth (0 = full, >0 = shallow)
}

// Cloner handles git clone operations.
type Cloner struct{}

// Clone clones a repo into the target directory if it doesn't already exist.
// It returns true if a clone was performed, false if the directory already existed.
// r receives all git output; pass a non-nil *Reporter.
func (c *Cloner) Clone(ctx context.Context, url, targetDir string, r *Reporter) (bool, error) {
	return c.CloneWith(ctx, url, targetDir, CloneOptions{}, r)
}

// CloneWithBranch clones a repo into the target directory, optionally checking
// out a specific branch. If branch is empty, the default branch is used.
// It returns true if a clone was performed, false if the directory already existed.
// r receives all git output; pass a non-nil *Reporter.
func (c *Cloner) CloneWithBranch(ctx context.Context, url, targetDir, branch string, r *Reporter) (bool, error) {
	return c.CloneWith(ctx, url, targetDir, CloneOptions{Ref: branch}, r)
}

// CloneWith clones a repo into the target directory with the given options.
// If Ref looks like a commit SHA (hex string of 7-40 characters), it clones
// the default branch then checks out the commit. Otherwise, Ref is passed
// as --branch (works for both branches and tags).
// It returns true if a clone was performed, false if the directory already existed.
// r receives all git output; pass a non-nil *Reporter.
func (c *Cloner) CloneWith(ctx context.Context, url, targetDir string, opts CloneOptions, r *Reporter) (bool, error) {
	if _, err := os.Stat(filepath.Join(targetDir, ".git")); err == nil {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return false, fmt.Errorf("creating parent directory: %w", err)
	}

	sha := isCommitSHA(opts.Ref)

	args := []string{"clone"}
	if opts.Depth > 0 {
		args = append(args, "--depth", fmt.Sprintf("%d", opts.Depth))
	}
	if opts.Ref != "" && !sha {
		args = append(args, "--branch", opts.Ref)
	}
	args = append(args, url, targetDir)

	cmd := exec.CommandContext(ctx, "git", args...)
	if err := runGitWithReporter(r, cmd); err != nil {
		return false, fmt.Errorf("cloning %s: %w", url, err)
	}

	if sha {
		checkout := exec.CommandContext(ctx, "git", "-C", targetDir, "checkout", opts.Ref)
		if err := runGitWithReporter(r, checkout); err != nil {
			return false, fmt.Errorf("checking out %s: %w", opts.Ref, err)
		}
	}

	return true, nil
}

// hexPattern matches commit SHAs: 7-40 lowercase hex characters.
var hexPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// isCommitSHA returns true if ref looks like a commit SHA (7-40 hex chars).
func isCommitSHA(ref string) bool {
	return hexPattern.MatchString(ref)
}

// ResolveCloneURL converts an "org/repo" shorthand to a full clone URL
// based on the given protocol. Supported protocols are "https" and "ssh".
// If the input already looks like a full URL (contains "://" or starts
// with "git@"), it is returned unchanged.
func ResolveCloneURL(orgRepo, protocol string) (string, error) {
	// Already a full URL or absolute filesystem path -- pass through.
	if strings.Contains(orgRepo, "://") || strings.HasPrefix(orgRepo, "git@") ||
		(strings.HasPrefix(orgRepo, "/") && strings.Count(orgRepo, "/") > 1) {
		return orgRepo, nil
	}

	parts := strings.SplitN(orgRepo, "/", 3)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("invalid org/repo format: %q", orgRepo)
	}

	org, repo := parts[0], parts[1]

	switch strings.ToLower(protocol) {
	case "ssh":
		return fmt.Sprintf("git@github.com:%s/%s.git", org, repo), nil
	case "https", "":
		return fmt.Sprintf("https://github.com/%s/%s.git", org, repo), nil
	default:
		return "", fmt.Errorf("unsupported clone protocol: %q", protocol)
	}
}
