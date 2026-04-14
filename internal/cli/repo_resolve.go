package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// findRepoDir searches for a repo by name within an instance directory.
//
// It uses workspace.EnumerateRepos to validate the name is discoverable
// (same sanitization + skip rules), then scans group subdirectories to
// resolve the full path and detect cross-group ambiguity. First-match
// semantics and the "ambiguous" error shape are preserved for existing
// callers.
func findRepoDir(instanceRoot, repoName string) (string, error) {
	if strings.Contains(repoName, "/") || strings.Contains(repoName, "..") {
		return "", fmt.Errorf("invalid repo name: %q", repoName)
	}

	names, err := workspace.EnumerateRepos(instanceRoot)
	if err != nil {
		return "", fmt.Errorf("reading instance directory: %w", err)
	}
	present := false
	for _, n := range names {
		if n == repoName {
			present = true
			break
		}
	}
	if !present {
		return "", fmt.Errorf("not found")
	}

	// Name exists under some group; scan to collect every group path that
	// contains it so we can detect ambiguity. EnumerateRepos already
	// filtered control dirs and bad names, so we replay the same skip set
	// for consistency.
	entries, err := os.ReadDir(instanceRoot)
	if err != nil {
		return "", fmt.Errorf("reading instance directory: %w", err)
	}

	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() {
			continue
		}
		if name == workspace.StateDir || name == ".claude" {
			continue
		}
		if len(name) > 0 && name[0] == '.' {
			continue
		}
		if !workspace.ValidName(name) {
			continue
		}
		candidate := filepath.Join(instanceRoot, name, repoName)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			matches = append(matches, candidate)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("not found")
	case 1:
		rel, err := filepath.Rel(instanceRoot, matches[0])
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("path traversal detected")
		}
		return matches[0], nil
	default:
		var groups []string
		for _, m := range matches {
			rel, _ := filepath.Rel(instanceRoot, m)
			groups = append(groups, filepath.Dir(rel))
		}
		return "", fmt.Errorf("ambiguous: found in groups %s", strings.Join(groups, ", "))
	}
}
