package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CloneOrSyncOverlay clones the overlay repo to dir when dir does not contain
// a valid git repository (returning wasCloneAttempt=true). When a valid clone
// already exists it pulls with --ff-only (returning wasCloneAttempt=false).
//
// wasCloneAttempt signals which error-handling path the caller should use:
//   - true  → we attempted a fresh clone; callers treat failure as a silent skip
//     because the overlay repo may simply not exist.
//   - false → we attempted a pull on an existing clone; callers treat failure as
//     a hard error because the overlay was previously accessible.
//
// url may be an org/repo shorthand, a full HTTPS URL, or an SSH URL.
func CloneOrSyncOverlay(url, dir string) (wasCloneAttempt bool, err error) {
	cloneURL, resolveErr := ResolveCloneURL(url, "ssh")
	if resolveErr != nil {
		return true, fmt.Errorf("resolving overlay URL %q: %w", url, resolveErr)
	}
	if !isValidGitDir(dir) {
		// Clone fresh. Suppress git output: callers treat a clone failure as a
		// silent skip (the overlay repo may not exist), so printing git's
		// "Repository not found" error would alarm users needlessly.
		if mkErr := os.MkdirAll(filepath.Dir(dir), 0o755); mkErr != nil {
			return true, fmt.Errorf("creating overlay parent directory: %w", mkErr)
		}
		cmd := exec.Command("git", "clone", cloneURL, dir)
		if runErr := cmd.Run(); runErr != nil {
			return true, fmt.Errorf("cloning overlay %s: %w", url, runErr)
		}
		return true, nil
	}

	// Pull existing clone. Suppress output: R22 requires that standard apply
	// output not include the overlay's URL or repo name. The git fetch progress
	// line ("From github.com:org/repo-overlay") would leak the overlay name.
	// Sync errors surface via the returned error, not git's output.
	if runErr := exec.Command("git", "-C", dir, "pull", "--ff-only").Run(); runErr != nil {
		return false, fmt.Errorf("syncing overlay: %w", runErr)
	}
	return false, nil
}

// isValidGitDir returns true when dir contains a valid git repository: a .git
// entry exists AND git rev-parse HEAD exits 0. The HEAD check matches the
// "previously cloned" heuristic in R7/R19 — a .git directory from a partial
// or corrupt clone is not treated as a valid prior clone.
func isValidGitDir(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false
	}
	return exec.Command("git", "-C", dir, "rev-parse", "HEAD").Run() == nil
}

// HeadSHA returns the current HEAD commit SHA of the git repository at dir.
func HeadSHA(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("reading HEAD SHA: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
