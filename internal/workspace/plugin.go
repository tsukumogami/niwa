package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const repoRefPrefix = "repo:"

// ResolveMarketplaceSource resolves a marketplace source string to a value
// suitable for `claude plugin marketplace add`. GitHub refs (org/repo) pass
// through unchanged. repo: refs are resolved to absolute paths using the
// repoIndex (repo name -> on-disk path).
func ResolveMarketplaceSource(source string, repoIndex map[string]string) (string, error) {
	if !strings.HasPrefix(source, repoRefPrefix) {
		return source, nil
	}

	ref := strings.TrimPrefix(source, repoRefPrefix)
	slashIdx := strings.IndexByte(ref, '/')
	if slashIdx < 0 {
		return "", fmt.Errorf("invalid repo ref %q: expected \"repo:<name>/<path>\"", source)
	}

	repoName := ref[:slashIdx]
	filePath := ref[slashIdx+1:]

	repoDir, ok := repoIndex[repoName]
	if !ok {
		return "", fmt.Errorf("marketplace %q: repo %q is not managed by this workspace", source, repoName)
	}

	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return "", fmt.Errorf("marketplace %q: repo %q has not been cloned", source, repoName)
	}

	absPath := filepath.Join(repoDir, filePath)

	if err := checkContainment(absPath, repoDir); err != nil {
		return "", fmt.Errorf("marketplace %q: path escapes repo directory", source)
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return "", fmt.Errorf("marketplace %q: file not found at %s", source, absPath)
	}

	return absPath, nil
}

// FindClaude checks if the claude CLI is on PATH.
func FindClaude() (string, bool) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", false
	}
	return path, true
}

// RegisterMarketplaces registers marketplace sources and updates their caches.
// Returns warnings for any failures (non-fatal).
func RegisterMarketplaces(sources []string, repoIndex map[string]string) (warnings []string, fatalErr error) {
	if len(sources) == 0 {
		return nil, nil
	}

	claudePath, found := FindClaude()
	if !found {
		return []string{"claude CLI not found, skipping plugin installation. Install Claude Code to enable plugin management."}, nil
	}

	for _, source := range sources {
		resolved, err := ResolveMarketplaceSource(source, repoIndex)
		if err != nil {
			return nil, err // config errors are fatal
		}

		cmd := exec.Command(claudePath, "plugin", "marketplace", "add", resolved, "--scope", "user")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			warnings = append(warnings, fmt.Sprintf("marketplace registration failed for %s: %v", source, err))
		}
	}

	// Update all marketplace caches.
	cmd := exec.Command(claudePath, "plugin", "marketplace", "update")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		warnings = append(warnings, fmt.Sprintf("marketplace update failed: %v", err))
	}

	return warnings, nil
}

// InstallPlugins installs plugins at project scope for a repo.
// Returns warnings for any failures (non-fatal).
func InstallPlugins(plugins []string, repoDir string) []string {
	if len(plugins) == 0 {
		return nil
	}

	claudePath, found := FindClaude()
	if !found {
		return nil // already warned at marketplace registration
	}

	var warnings []string
	for _, plugin := range plugins {
		cmd := exec.Command(claudePath, "plugin", "install", plugin, "--scope", "local")
		cmd.Dir = repoDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			warnings = append(warnings, fmt.Sprintf("plugin install %s failed for %s: %v", plugin, filepath.Base(repoDir), err))
		}
	}

	return warnings
}
