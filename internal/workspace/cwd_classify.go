package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

	// CwdAtWorkspaceRoot: cwd is the workspace root, or a directory inside
	// it that is neither an instance nor a worktree (then BelowRoot is set).
	// WorkspaceRoot is populated; InstanceDir is empty.
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

	// BelowRoot is set for CwdAtWorkspaceRoot when cwd is a directory inside
	// the workspace root rather than the root itself: a directory that is not
	// an instance or a worktree, such as a half-provisioned instance left by
	// an interrupted create. The root's own .niwa/ subtree counts as the root
	// (see isBelowRoot). Read-only commands treat it as the root. Commands that act on every
	// instance must refuse it; see RefuseBelowRoot.
	BelowRoot bool
}

// RefuseBelowRoot is the scope rule for commands that can act on every
// instance in a workspace (apply, destroy). Such a command runs from exactly
// three kinds of directory: an instance, a worktree, or the workspace root
// itself (including the root's own .niwa/). From any other directory under the root it returns an error naming
// that directory, and it never falls back to the root's scope: a script that
// loops over directories under the root and lands in one that isn't an
// instance would otherwise act on the whole workspace. It returns nil for
// every other classification, including CwdOutside, which callers report in
// their own words.
func (c CwdClassification) RefuseBelowRoot(cwd, command string) error {
	if c.Class != CwdAtWorkspaceRoot || !c.BelowRoot {
		return nil
	}
	msg := fmt.Sprintf("%s: %s is inside workspace %s but is not an instance, a worktree, or the workspace root; "+
		"refusing to act from here, because from this directory the command would take the whole workspace's scope. "+
		"Run it inside an instance, or at %s itself",
		command, cwd, c.WorkspaceRoot, c.WorkspaceRoot)
	if top := topLevelEntry(c.WorkspaceRoot, cwd); !strings.HasPrefix(top, ".") {
		msg += fmt.Sprintf(". If %s is an instance whose creation was interrupted, it has no %s and niwa does not manage it; remove it by hand",
			filepath.Join(c.WorkspaceRoot, top), filepath.Join(StateDir, StateFile))
	}
	return errors.New(msg)
}

// isBelowRoot reports whether abs is a directory under root that does not
// count as the root. The root's own config dir (<root>/.niwa and anything in
// it) counts as the root: it holds the root's configuration, and editing it
// there and then applying is ordinary use. So does every directory in a
// single-instance layout, where the root is itself the instance and its repos
// sit directly under it; those directories are inside the instance, the same
// judgement the worktree commands make (IsSingleInstanceLayout). Everything
// else under the root that is not an instance or a worktree does not.
func isBelowRoot(root, abs string) bool {
	top := topLevelEntry(root, abs)
	if top == "" || top == StateDir {
		return false
	}
	return !IsSingleInstanceLayout(root)
}

// topLevelEntry returns the first path element of path below root, or "" when
// path is not strictly below root.
func topLevelEntry(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
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
		root := filepath.Dir(configDir)
		return CwdClassification{
			Class:         CwdAtWorkspaceRoot,
			WorkspaceRoot: root,
			BelowRoot:     isBelowRoot(root, abs),
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
