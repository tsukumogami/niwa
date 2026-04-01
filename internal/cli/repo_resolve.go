package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// findRepoDir searches for a repo by name within an instance directory.
// It scans all group subdirectories (immediate children that are not .niwa
// or .claude) looking for a matching repo directory.
func findRepoDir(instanceRoot, repoName string) (string, error) {
	if strings.Contains(repoName, "/") || strings.Contains(repoName, "..") {
		return "", fmt.Errorf("invalid repo name: %q", repoName)
	}

	entries, err := os.ReadDir(instanceRoot)
	if err != nil {
		return "", fmt.Errorf("reading instance directory: %w", err)
	}

	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".niwa" || entry.Name() == ".claude" {
			continue
		}
		candidate := filepath.Join(instanceRoot, entry.Name(), repoName)
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
