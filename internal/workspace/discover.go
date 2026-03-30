package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// DiscoverHooks scans configDir/hooks/ for hook scripts and returns a
// HooksConfig mapping event names to script paths.
//
// Two layout styles are supported:
//   - hooks/{event}.sh       -> maps to event name (extension stripped)
//   - hooks/{event}/*.sh     -> each file maps to that event
//
// Non-.sh files are ignored. A missing hooks/ directory returns an empty
// HooksConfig without error.
func DiscoverHooks(configDir string) (config.HooksConfig, error) {
	hooksDir := filepath.Join(configDir, "hooks")

	if err := validateWithinDir(configDir, hooksDir); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(hooksDir)
	if os.IsNotExist(err) {
		return config.HooksConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading hooks directory: %w", err)
	}

	hooks := config.HooksConfig{}

	for _, entry := range entries {
		entryPath := filepath.Join(hooksDir, entry.Name())

		if entry.IsDir() {
			// Subdirectory: each .sh file inside maps to the directory name as event.
			event := entry.Name()
			subEntries, err := os.ReadDir(entryPath)
			if err != nil {
				return nil, fmt.Errorf("reading hooks subdirectory %q: %w", event, err)
			}
			for _, sub := range subEntries {
				if sub.IsDir() || !strings.HasSuffix(sub.Name(), ".sh") {
					continue
				}
				scriptPath := filepath.Join(entryPath, sub.Name())
				if err := validateWithinDir(configDir, scriptPath); err != nil {
					return nil, err
				}
				hooks[event] = append(hooks[event], config.HookEntry{Scripts: []string{scriptPath}})
			}
		} else if strings.HasSuffix(entry.Name(), ".sh") {
			// Top-level .sh file: event name is filename without extension.
			event := strings.TrimSuffix(entry.Name(), ".sh")
			if err := validateWithinDir(configDir, entryPath); err != nil {
				return nil, err
			}
			hooks[event] = append(hooks[event], config.HookEntry{Scripts: []string{entryPath}})
		}
	}

	return hooks, nil
}

// DiscoverEnvFiles scans configDir/env/ for environment files.
//
// It looks for:
//   - env/workspace.env          -> returned as workspaceFile (empty string if absent)
//   - env/repos/{repoName}.env   -> returned in repoFiles map (name without .env -> path)
//
// Missing env/ or env/repos/ directories return empty results without error.
func DiscoverEnvFiles(configDir string) (workspaceFile string, repoFiles map[string]string, err error) {
	envDir := filepath.Join(configDir, "env")

	if err := validateWithinDir(configDir, envDir); err != nil {
		return "", nil, err
	}

	repoFiles = make(map[string]string)

	// Check for workspace.env.
	wsPath := filepath.Join(envDir, "workspace.env")
	if err := validateWithinDir(configDir, wsPath); err != nil {
		return "", nil, err
	}
	if _, err := os.Stat(wsPath); err == nil {
		workspaceFile = wsPath
	} else if !os.IsNotExist(err) {
		return "", nil, fmt.Errorf("checking workspace.env: %w", err)
	}

	// Scan env/repos/.
	reposDir := filepath.Join(envDir, "repos")
	if err := validateWithinDir(configDir, reposDir); err != nil {
		return "", nil, err
	}

	entries, err := os.ReadDir(reposDir)
	if os.IsNotExist(err) {
		return workspaceFile, repoFiles, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("reading env/repos directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".env") {
			continue
		}
		repoName := strings.TrimSuffix(entry.Name(), ".env")
		filePath := filepath.Join(reposDir, entry.Name())
		if err := validateWithinDir(configDir, filePath); err != nil {
			return "", nil, err
		}
		repoFiles[repoName] = filePath
	}

	return workspaceFile, repoFiles, nil
}

// validateWithinDir ensures that resolvedPath stays within baseDir after
// symlink resolution and cleaning.
func validateWithinDir(baseDir, targetPath string) error {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return fmt.Errorf("resolving base directory: %w", err)
	}

	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("resolving target path: %w", err)
	}

	// Clean both paths to normalize.
	cleanBase := filepath.Clean(absBase)
	cleanTarget := filepath.Clean(absTarget)

	// Target must be under base (or equal to base).
	if cleanTarget != cleanBase && !strings.HasPrefix(cleanTarget, cleanBase+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes config directory %q", targetPath, baseDir)
	}

	return nil
}
