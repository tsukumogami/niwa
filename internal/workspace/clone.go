package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Cloner handles git clone operations.
type Cloner struct{}

// Clone clones a repo into the target directory if it doesn't already exist.
// It returns true if a clone was performed, false if the directory already existed.
func (c *Cloner) Clone(ctx context.Context, url, targetDir string) (bool, error) {
	return c.CloneWithBranch(ctx, url, targetDir, "")
}

// CloneWithBranch clones a repo into the target directory, optionally checking
// out a specific branch. If branch is empty, the default branch is used.
// It returns true if a clone was performed, false if the directory already existed.
func (c *Cloner) CloneWithBranch(ctx context.Context, url, targetDir, branch string) (bool, error) {
	if _, err := os.Stat(filepath.Join(targetDir, ".git")); err == nil {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return false, fmt.Errorf("creating parent directory: %w", err)
	}

	args := []string{"clone"}
	if branch != "" {
		args = append(args, "-b", branch)
	}
	args = append(args, url, targetDir)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("cloning %s: %w", url, err)
	}

	return true, nil
}
