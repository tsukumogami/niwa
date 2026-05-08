package workspace

import (
	"fmt"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/config"
)

// CwdClass discriminates where the user is running niwa from, used by
// commands (notably destroy) that behave differently inside an instance,
// at the workspace root, and outside any niwa workspace.
type CwdClass int

const (
	// CwdInsideInstance: cwd is an instance directory or any subdirectory
	// of one. Both WorkspaceRoot and InstanceDir are populated.
	CwdInsideInstance CwdClass = iota

	// CwdAtWorkspaceRoot: cwd is the workspace root (or the workspace
	// dir if cwd is a sibling of an instance directory). WorkspaceRoot
	// is populated; InstanceDir is empty.
	CwdAtWorkspaceRoot

	// CwdOutside: cwd is neither inside a workspace nor at one. Both
	// path fields are empty.
	CwdOutside
)

// String returns a human-readable representation of the class. Used in
// tests and error messages.
func (c CwdClass) String() string {
	switch c {
	case CwdInsideInstance:
		return "inside-instance"
	case CwdAtWorkspaceRoot:
		return "at-workspace-root"
	case CwdOutside:
		return "outside"
	default:
		return fmt.Sprintf("unknown(%d)", int(c))
	}
}

// CwdClassification is the result of ClassifyCwd. WorkspaceRoot and
// InstanceDir are absolute paths; both are empty when Class is CwdOutside.
//
// (The struct is named CwdClassification rather than Classify to avoid
// collision with the existing workspace.Classify function that groups
// repos.)
type CwdClassification struct {
	Class         CwdClass
	WorkspaceRoot string // populated for CwdInsideInstance and CwdAtWorkspaceRoot
	InstanceDir   string // populated for CwdInsideInstance only
}

// ClassifyCwd discriminates a cwd into one of three classes:
//   - CwdInsideInstance: cwd is inside an instance (DiscoverInstance succeeds)
//   - CwdAtWorkspaceRoot: cwd is at or inside a workspace root but NOT an
//     instance (config.Discover succeeds, DiscoverInstance fails)
//   - CwdOutside: neither (both helpers fail)
//
// It does not error on missing-niwa-workspace conditions — those produce
// CwdOutside with empty paths. It does error on filesystem-resolution
// failures (e.g., bad permissions) so callers can distinguish "cwd is
// fine, just outside niwa" from "I couldn't even read cwd."
//
// Used by `niwa destroy` to dispatch into mode-specific runners.
func ClassifyCwd(cwd string) (CwdClassification, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return CwdClassification{}, fmt.Errorf("resolving cwd: %w", err)
	}

	// Inside an instance? Most specific case — check first.
	if instanceDir, err := DiscoverInstance(abs); err == nil {
		// We expect to also find the workspace root by walking further up
		// (instances live under workspace roots). If config.Discover
		// fails here, it's an unusual layout (orphan instance) — treat
		// it as inside-instance with no workspace root, so the caller
		// can decide whether that's an error.
		_, configDir, configErr := config.Discover(abs)
		var workspaceRoot string
		if configErr == nil {
			workspaceRoot = filepath.Dir(configDir)
		}
		return CwdClassification{
			Class:         CwdInsideInstance,
			WorkspaceRoot: workspaceRoot,
			InstanceDir:   instanceDir,
		}, nil
	}

	// At or inside a workspace root? config.Discover succeeds when
	// .niwa/workspace.toml exists at or above cwd.
	if _, configDir, err := config.Discover(abs); err == nil {
		return CwdClassification{
			Class:         CwdAtWorkspaceRoot,
			WorkspaceRoot: filepath.Dir(configDir),
		}, nil
	}

	// Outside both.
	return CwdClassification{Class: CwdOutside}, nil
}
