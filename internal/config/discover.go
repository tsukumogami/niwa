package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// ConfigDir is the directory name where workspace config lives.
	ConfigDir = ".niwa"
	// ConfigFile is the workspace config filename.
	ConfigFile = "workspace.toml"
)

// Discover walks up from startDir looking for .niwa/workspace.toml.
// It returns the absolute path to the config file and the config directory.
func Discover(startDir string) (configPath string, configDir string, err error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", "", fmt.Errorf("resolving start directory: %w", err)
	}

	for {
		candidate := filepath.Join(dir, ConfigDir, ConfigFile)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, filepath.Join(dir, ConfigDir), nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", "", fmt.Errorf("no %s/%s found in any parent of %s", ConfigDir, ConfigFile, startDir)
}
