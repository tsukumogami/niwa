package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// ResolveInstanceTarget determines the absolute path of the target instance
// for a destroy operation.
//
// If nameArg is non-empty, the workspace root is discovered from cwd, all
// instances are enumerated, and the one whose InstanceName matches nameArg is
// returned. If nameArg is empty, the instance containing cwd is returned via
// DiscoverInstance.
func ResolveInstanceTarget(cwd, nameArg string) (string, error) {
	if nameArg != "" {
		return resolveInstanceByName(cwd, nameArg)
	}

	dir, err := DiscoverInstance(cwd)
	if err != nil {
		return "", fmt.Errorf("resolving current instance: %w", err)
	}
	return dir, nil
}

// resolveInstanceByName finds an instance by its InstanceName within the
// workspace discovered from cwd.
func resolveInstanceByName(cwd, name string) (string, error) {
	_, configDir, err := config.Discover(cwd)
	if err != nil {
		return "", fmt.Errorf("finding workspace root: %w", err)
	}

	workspaceRoot := filepath.Dir(configDir)
	instances, err := EnumerateInstances(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("enumerating instances: %w", err)
	}

	for _, dir := range instances {
		state, loadErr := LoadState(dir)
		if loadErr != nil {
			continue
		}
		if state.InstanceName == name {
			return dir, nil
		}
	}

	// Build list of available names for the error message.
	var available []string
	for _, dir := range instances {
		state, loadErr := LoadState(dir)
		if loadErr != nil {
			continue
		}
		available = append(available, state.InstanceName)
	}

	if len(available) == 0 {
		return "", fmt.Errorf("instance %q not found: no instances exist in workspace", name)
	}
	return "", fmt.Errorf("instance %q not found, available instances: %s", name, strings.Join(available, ", "))
}

// ValidateInstanceDir checks that dir is a valid instance directory suitable
// for destruction. It verifies that .niwa/instance.json exists (confirming it
// is an instance) and that .niwa/workspace.toml does NOT exist (confirming it
// is not a workspace root).
func ValidateInstanceDir(dir string) error {
	instancePath := filepath.Join(dir, StateDir, StateFile)
	if _, err := os.Stat(instancePath); err != nil {
		return fmt.Errorf("not an instance directory: %s does not exist", instancePath)
	}

	workspacePath := filepath.Join(dir, config.ConfigDir, config.ConfigFile)
	if _, err := os.Stat(workspacePath); err == nil {
		return fmt.Errorf("refusing to destroy workspace root: %s exists", workspacePath)
	}

	return nil
}

// CheckUncommittedChanges inspects each cloned repo within the instance for
// uncommitted git changes. It loads the instance state, iterates repos where
// Cloned is true, and runs git status --porcelain on each. Repos whose
// directories no longer exist on disk are silently skipped.
//
// Returns the names (map keys) of repos that have uncommitted changes.
func CheckUncommittedChanges(instanceDir string) ([]string, error) {
	state, err := LoadState(instanceDir)
	if err != nil {
		return nil, fmt.Errorf("loading instance state: %w", err)
	}

	var dirty []string
	for name, repo := range state.Repos {
		if !repo.Cloned {
			continue
		}

		repoDir := filepath.Join(instanceDir, name)
		if _, err := os.Stat(repoDir); os.IsNotExist(err) {
			continue
		}

		out, err := exec.Command("git", "-C", repoDir, "status", "--porcelain").Output()
		if err != nil {
			return nil, fmt.Errorf("checking git status for %s: %w", name, err)
		}

		if len(strings.TrimSpace(string(out))) > 0 {
			dirty = append(dirty, name)
		}
	}

	return dirty, nil
}

// DestroyInstance validates that dir is a proper instance directory and then
// removes it entirely.
func DestroyInstance(dir string) error {
	if err := ValidateInstanceDir(dir); err != nil {
		return err
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing instance directory: %w", err)
	}

	return nil
}
