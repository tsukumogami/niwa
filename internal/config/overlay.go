package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DeriveOverlayURL converts a workspace source URL into a convention overlay
// URL of the form "org/repo-overlay". Accepts HTTPS URLs
// (https://github.com/org/repo[.git]), SSH URLs (git@github.com:org/repo.git),
// and shorthand (org/repo). Returns ok=false when the input cannot be parsed.
func DeriveOverlayURL(sourceURL string) (conventionURL string, ok bool) {
	org, repo, parsed := parseOrgRepo(sourceURL)
	if !parsed {
		return "", false
	}
	return org + "/" + repo + "-overlay", true
}

// parseOrgRepo extracts (org, repo) from an HTTPS URL, SSH URL, or shorthand.
// Strips a trailing ".git" suffix from the repo name.
func parseOrgRepo(s string) (org, repo string, ok bool) {
	s = strings.TrimSpace(s)

	switch {
	case strings.HasPrefix(s, "https://"):
		// https://github.com/org/repo[.git]
		rest := strings.TrimPrefix(s, "https://")
		// drop host
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return "", "", false
		}
		rest = rest[slash+1:]
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return "", "", false
		}
		org = parts[0]
		repo = strings.TrimSuffix(parts[1], ".git")
		if repo == "" {
			return "", "", false
		}
		return org, repo, true

	case strings.HasPrefix(s, "git@"):
		// git@github.com:org/repo.git
		colon := strings.Index(s, ":")
		if colon < 0 {
			return "", "", false
		}
		rest := s[colon+1:]
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return "", "", false
		}
		org = parts[0]
		repo = strings.TrimSuffix(parts[1], ".git")
		if repo == "" {
			return "", "", false
		}
		return org, repo, true

	default:
		// shorthand org/repo
		parts := strings.SplitN(s, "/", 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return "", "", false
		}
		// reject anything that looks like an absolute path or has a scheme
		if strings.Contains(parts[0], ":") || strings.HasPrefix(s, "/") {
			return "", "", false
		}
		org = parts[0]
		repo = strings.TrimSuffix(parts[1], ".git")
		if repo == "" {
			return "", "", false
		}
		return org, repo, true
	}
}

// OverlayDir returns the local directory where the overlay repo is cloned.
// The path is $XDG_CONFIG_HOME/niwa/overlays/<org>-<repo>/ (falling back to
// $HOME/.config/niwa/overlays/<org>-<repo>/ when XDG_CONFIG_HOME is unset).
// overlayURL may be a shorthand (org/repo), HTTPS URL, or SSH URL.
func OverlayDir(overlayURL string) (string, error) {
	org, repo, ok := parseOrgRepo(overlayURL)
	if !ok {
		return "", fmt.Errorf("cannot parse overlay URL %q", overlayURL)
	}
	dirName := org + "-" + repo

	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determining home directory: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "niwa", "overlays", dirName), nil
}

// CloneOrSyncOverlay clones the overlay repo to dir when dir does not exist or
// contains no valid git repository (returning firstTime=true). When a valid
// clone already exists it pulls with --ff-only (returning firstTime=false).
func CloneOrSyncOverlay(url, dir string) (firstTime bool, err error) {
	if !isValidGitDir(dir) {
		// Clone fresh.
		if mkErr := os.MkdirAll(filepath.Dir(dir), 0o755); mkErr != nil {
			return true, fmt.Errorf("creating overlay parent directory: %w", mkErr)
		}
		cmd := exec.Command("git", "clone", url, dir)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
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
