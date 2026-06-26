package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/config"
)

// CwdClass discriminates where the user is running niwa from, used by
// commands (notably destroy) that behave differently inside an instance,
// at the workspace root, and outside any niwa workspace.
type CwdClass int

const (
	// CwdInsideInstance: cwd is an instance directory or any subdirectory
	// of one (but NOT inside one of that instance's worktrees). Both
	// WorkspaceRoot and InstanceDir are populated.
	CwdInsideInstance CwdClass = iota

	// CwdAtWorkspaceRoot: cwd is the workspace root (or the workspace
	// dir if cwd is a sibling of an instance directory). WorkspaceRoot
	// is populated; InstanceDir is empty.
	CwdAtWorkspaceRoot

	// CwdInsideWorktree: cwd is inside one of an instance's session
	// worktrees (a directory under <instanceRoot>/.niwa/worktrees/). This
	// is the most specific class: an instance subtree contains its worktree
	// subtrees, so worktree detection runs before the inside-instance
	// fallback. WorkspaceRoot, InstanceDir, and WorktreeDir are all
	// populated.
	CwdInsideWorktree

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
	case CwdInsideWorktree:
		return "inside-worktree"
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
	WorkspaceRoot string // populated for CwdInsideInstance, CwdAtWorkspaceRoot, CwdInsideWorktree
	InstanceDir   string // populated for CwdInsideInstance and CwdInsideWorktree
	WorktreeDir   string // populated for CwdInsideWorktree only (the worktree root)
}

// ClassifyCwd discriminates a cwd into one of four classes:
//   - CwdInsideWorktree: cwd is inside a session worktree
//     (<instanceRoot>/.niwa/worktrees/<name>/...). Most specific.
//   - CwdInsideInstance: cwd is inside an instance but not one of its
//     worktrees (DiscoverInstance succeeds)
//   - CwdAtWorkspaceRoot: cwd is at or inside a workspace root but NOT an
//     instance (config.Discover succeeds, DiscoverInstance fails)
//   - CwdOutside: neither (both helpers fail)
//
// It does not error on missing-niwa-workspace conditions — those produce
// CwdOutside with empty paths. It does error on filesystem-resolution
// failures (e.g., bad permissions) so callers can distinguish "cwd is
// fine, just outside niwa" from "I couldn't even read cwd."
//
// Used by `niwa destroy` to dispatch into mode-specific runners and by
// `niwa apply` to resolve its subtree scope.
func ClassifyCwd(cwd string) (CwdClassification, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return CwdClassification{}, fmt.Errorf("resolving cwd: %w", err)
	}

	// Inside a worktree? This is the most specific case and must be checked
	// before the inside-instance fallback: a worktree lives under an
	// instance's .niwa/worktrees/ subtree, so DiscoverInstance(abs) would
	// otherwise resolve up to the parent instance and misclassify a worktree
	// cwd as inside-instance.
	if worktreeDir, instanceDir, ok := discoverWorktree(abs); ok {
		_, configDir, configErr := config.Discover(abs)
		var workspaceRoot string
		if configErr == nil {
			workspaceRoot = filepath.Dir(configDir)
		}
		return CwdClassification{
			Class:         CwdInsideWorktree,
			WorkspaceRoot: workspaceRoot,
			InstanceDir:   instanceDir,
			WorktreeDir:   worktreeDir,
		}, nil
	}

	// Inside an instance? Next most specific case.
	//
	// A workspace root carries its own .niwa/instance.json (init persists
	// init-time state there for `niwa create` to read), so DiscoverInstance
	// resolves the root to itself. That must NOT classify as inside-instance:
	// treating the root as instance-0 is exactly the bug that made `niwa apply`
	// at the root clone repos directly under it. When the discovered directory
	// is a workspace root (carries .niwa/workspace.toml), fall through to the
	// workspace-root case below — the root is never an instance.
	if instanceDir, err := DiscoverInstance(abs); err == nil && !isWorkspaceRoot(instanceDir) {
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

// worktreesDirName is the directory under an instance's .niwa that holds
// session worktrees: <instanceRoot>/.niwa/worktrees/<repo>-<sid>/. It mirrors
// the layout CreateSession writes (internal/worktree/worktree.go).
const worktreesDirName = "worktrees"

// discoverWorktree reports whether abs is at or below a session worktree and,
// if so, returns the worktree root and its enclosing instance directory.
//
// A worktree root is the first directory whose immediate parent is
// "<instanceRoot>/.niwa/worktrees". This function walks up from abs looking
// for a path of the shape ".../<instance>/.niwa/worktrees/<name>" where
// <name> is at or above abs, then confirms <instance> is a real instance
// (carries .niwa/instance.json). The instance.json confirmation guards
// against a stray "worktrees" directory that is not an actual niwa worktree
// host.
func discoverWorktree(abs string) (worktreeDir, instanceDir string, ok bool) {
	dir := abs
	for {
		parent := filepath.Dir(dir)
		// Is `dir` a worktree root, i.e. parent == ".../.niwa/worktrees"?
		if filepath.Base(parent) == worktreesDirName {
			niwaDir := filepath.Dir(parent)
			if filepath.Base(niwaDir) == StateDir {
				instance := filepath.Dir(niwaDir)
				if _, err := os.Stat(statePath(instance)); err == nil {
					return dir, instance, true
				}
			}
		}
		if parent == dir {
			return "", "", false
		}
		dir = parent
	}
}
