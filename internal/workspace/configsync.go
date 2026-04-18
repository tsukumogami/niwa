package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SyncConfigDir pulls the latest config from origin if the config directory
// is a git repo with a remote. Returns nil if the directory is not a git repo
// or has no remote. Returns an error if the working tree is dirty (unless
// allowDirty is true).
func SyncConfigDir(configDir string, allowDirty bool) error {
	// Check if it's a git repo.
	gitDir := filepath.Join(configDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return nil // not a git repo, nothing to sync
	}

	// Check for origin remote.
	cmd := exec.Command("git", "-C", configDir, "remote", "get-url", "origin")
	if err := cmd.Run(); err != nil {
		return nil // no origin remote, nothing to sync
	}

	// Check for dirty working tree.
	if !allowDirty {
		cmd = exec.Command("git", "-C", configDir, "status", "--porcelain", "--untracked-files=no")
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("checking config directory status: %w", err)
		}
		if strings.TrimSpace(string(out)) != "" {
			return fmt.Errorf("config directory has uncommitted changes\n  %s\n  Use --allow-dirty to apply with local modifications", configDir)
		}
	}

	// Pull latest from origin.
	cmd = exec.Command("git", "-C", configDir, "pull", "--ff-only", "origin")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pulling config from origin: %w", err)
	}

	return nil
}
