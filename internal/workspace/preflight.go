package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Sentinel errors for init conflict detection.
var (
	// ErrWorkspaceExists indicates a workspace.toml already exists in the target directory.
	ErrWorkspaceExists = errors.New("workspace already exists")

	// ErrInsideInstance indicates the target directory is inside an existing niwa instance.
	ErrInsideInstance = errors.New("directory is inside an existing instance")

	// ErrNiwaDirectoryExists indicates a .niwa/ directory exists without a workspace.toml.
	ErrNiwaDirectoryExists = errors.New(".niwa directory exists without workspace config")
)

// InitConflictError wraps a sentinel error with contextual detail and a user-facing suggestion.
type InitConflictError struct {
	Err        error
	Detail     string
	Suggestion string
}

// Error returns a human-readable message combining the sentinel, detail, and suggestion.
func (e *InitConflictError) Error() string {
	return fmt.Sprintf("%s: %s. %s", e.Err, e.Detail, e.Suggestion)
}

// Unwrap returns the underlying sentinel error for use with errors.Is.
func (e *InitConflictError) Unwrap() error {
	return e.Err
}

// WorkspaceConfigFile is the filename for workspace configuration within StateDir.
const WorkspaceConfigFile = "workspace.toml"

// CheckInitConflicts checks whether the target directory is safe for niwa init.
// It returns an InitConflictError if a conflict is detected, or nil if init can proceed.
//
// Detection order:
//  1. .niwa/workspace.toml exists (existing workspace)
//  2. .niwa/ exists without workspace.toml (orphaned directory)
//  3. Directory is inside an existing niwa instance (nested instance)
func CheckInitConflicts(dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	niwaDir := filepath.Join(absDir, StateDir)
	workspaceConfig := filepath.Join(niwaDir, WorkspaceConfigFile)

	// Case 1: .niwa/workspace.toml exists -- this is already a workspace.
	if _, err := os.Stat(workspaceConfig); err == nil {
		return &InitConflictError{
			Err:        ErrWorkspaceExists,
			Detail:     fmt.Sprintf("found %s", filepath.Join(StateDir, WorkspaceConfigFile)),
			Suggestion: "Use niwa apply to update the existing workspace",
		}
	}

	// Case 3: .niwa/ exists without workspace.toml -- orphaned or partial state.
	if info, err := os.Stat(niwaDir); err == nil && info.IsDir() {
		return &InitConflictError{
			Err:        ErrNiwaDirectoryExists,
			Detail:     fmt.Sprintf("found %s directory without %s", StateDir, WorkspaceConfigFile),
			Suggestion: fmt.Sprintf("Remove the %s directory and retry", niwaDir),
		}
	}

	// Case 2: Inside an existing instance (walk up to find .niwa/instance.json).
	instanceDir, err := DiscoverInstance(absDir)
	if err == nil {
		return &InitConflictError{
			Err:        ErrInsideInstance,
			Detail:     fmt.Sprintf("this directory is inside the instance at %s", instanceDir),
			Suggestion: "Change to a directory outside the existing instance before running init",
		}
	}

	return nil
}
