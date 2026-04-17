package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CloneOrSyncOverlay clones the overlay repo to dir when dir does not exist or
// contains no valid git repository (returning firstTime=true). When a valid
// clone already exists it pulls with --ff-only (returning firstTime=false).
// url may be an org/repo shorthand, a full HTTPS URL, or an SSH URL.
func CloneOrSyncOverlay(url, dir string) (firstTime bool, err error) {
	cloneURL, resolveErr := ResolveCloneURL(url, "ssh")
	if resolveErr != nil {
		return true, fmt.Errorf("resolving overlay URL %q: %w", url, resolveErr)
	}
	if !isValidGitDir(dir) {
		// Clone fresh. Suppress git output: callers treat first-time clone
		// failure as a silent skip (overlay repo may not exist), so printing
		// git's "Repository not found" error would alarm users needlessly.
		if mkErr := os.MkdirAll(filepath.Dir(dir), 0o755); mkErr != nil {
			return true, fmt.Errorf("creating overlay parent directory: %w", mkErr)
		}
		cmd := exec.Command("git", "clone", cloneURL, dir)
		if runErr := cmd.Run(); runErr != nil {
			return true, fmt.Errorf("cloning overlay %s: %w", url, runErr)
		}
		return true, nil
	}

	// Pull existing clone.
	cmd := exec.Command("git", "-C", dir, "pull", "--ff-only")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if runErr := cmd.Run(); runErr != nil {
		return false, fmt.Errorf("syncing overlay: %w", runErr)
	}
	return false, nil
}

// isValidGitDir returns true when dir contains a .git entry.
func isValidGitDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// HeadSHA returns the current HEAD commit SHA of the git repository at dir.
func HeadSHA(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("reading HEAD SHA: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
