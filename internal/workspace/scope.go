package workspace

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// ApplyMode describes which subtree a workspace apply should converge.
type ApplyMode int

const (
	// ApplySingle targets the single instance the user is currently inside,
	// plus that instance's worktrees. The worktrees refresh together with the
	// instance under the inherit model; --no-cascade has no effect at this
	// scope.
	ApplySingle ApplyMode = iota
	// ApplyAll targets the workspace-root managed config plus every instance
	// under the workspace root and each instance's worktrees. --no-cascade
	// caps this to the root-managed config only, skipping the instance cascade.
	ApplyAll
	// ApplyNamed targets a specific instance selected by name, plus its
	// worktrees (which refresh together with the instance under the inherit
	// model; --no-cascade has no effect at this scope).
	ApplyNamed
	// ApplyWorktree targets a single session worktree the user is currently
	// inside, never the parent instance or sibling worktrees.
	ApplyWorktree
)

// ApplyScope holds the resolved target for a workspace apply operation.
//
// The subtree model (PRD R8, R13, R14): apply converges the subtree rooted at
// the current scope and never climbs above it.
//   - ApplyAll (workspace root): root-managed config + vault, then every
//     instance and its worktrees.
//   - ApplySingle / ApplyNamed (instance): that instance and its worktrees.
//   - ApplyWorktree: that worktree alone.
//
// WorkspaceRoot is populated for ApplyAll so the caller can materialize the
// workspace-root managed config. Worktree is populated for ApplyWorktree.
type ApplyScope struct {
	Mode      ApplyMode
	Instances []string // absolute paths to instance roots
	Config    string   // path to workspace.toml

	// WorkspaceRoot is the workspace-root directory (parent of .niwa).
	// Populated for ApplyAll; used by apply to materialize root-managed
	// config before cascading into instances.
	WorkspaceRoot string

	// Worktree carries the single worktree target for ApplyWorktree. Empty
	// for every other mode.
	Worktree WorktreeTarget
}

// WorktreeTarget identifies a single session worktree for ApplyWorktree.
type WorktreeTarget struct {
	WorktreePath string // absolute path to the worktree root
	InstanceRoot string // the enclosing instance directory
}

// ResolveApplyScope determines which subtree to converge based on the current
// working directory and an optional instance name flag. It implements the
// subtree model (PRD R8, R13, R14): the scope is the subtree rooted at cwd and
// apply never climbs above it.
//
// Resolution order:
//  1. If instanceFlag is non-empty, find the workspace root via config.Discover,
//     enumerate instances, and match by InstanceName. Returns ApplyNamed.
//  2. If cwd is inside a worktree, return ApplyWorktree targeting that worktree
//     alone (never the parent instance or siblings).
//  3. If cwd is inside an instance, return ApplySingle targeting that instance
//     (its worktrees are cascaded by the apply caller).
//  4. If cwd is at/inside a workspace root, enumerate all instances and return
//     ApplyAll (the caller materializes root config and cascades).
//  5. Error if none of the above applies.
//
// Note: this is an intentional pre-1.0 change. Previously apply from anywhere
// inside an instance (including a worktree) converged the whole instance; now a
// worktree cwd converges only that worktree.
func ResolveApplyScope(cwd, instanceFlag string) (*ApplyScope, error) {
	if instanceFlag != "" {
		return resolveNamed(cwd, instanceFlag)
	}

	classification, err := ClassifyCwd(cwd)
	if err != nil {
		return nil, fmt.Errorf("classifying working directory: %w", err)
	}

	// config.Discover resolves the workspace.toml path for any in-workspace
	// cwd. It is only absent for an orphan instance (an instance directory
	// with no enclosing workspace config), which we tolerate below.
	configPath, configDir, configErr := config.Discover(cwd)

	switch classification.Class {
	case CwdInsideWorktree:
		cfg := ""
		if configErr == nil {
			cfg = configPath
		}
		return &ApplyScope{
			Mode:   ApplyWorktree,
			Config: cfg,
			Worktree: WorktreeTarget{
				WorktreePath: classification.WorktreeDir,
				InstanceRoot: classification.InstanceDir,
			},
		}, nil

	case CwdInsideInstance:
		cfg := ""
		if configErr == nil {
			cfg = configPath
		}
		return &ApplyScope{
			Mode:      ApplySingle,
			Instances: []string{classification.InstanceDir},
			Config:    cfg,
		}, nil

	case CwdAtWorkspaceRoot:
		// configErr is guaranteed nil here: CwdAtWorkspaceRoot is exactly the
		// case where config.Discover succeeds.
		workspaceRoot := filepath.Dir(configDir)
		instances, err := EnumerateInstances(workspaceRoot)
		if err != nil {
			return nil, fmt.Errorf("enumerating instances: %w", err)
		}
		return &ApplyScope{
			Mode:          ApplyAll,
			Instances:     instances,
			Config:        configPath,
			WorkspaceRoot: workspaceRoot,
		}, nil

	default:
		return nil, fmt.Errorf("not inside a workspace instance, worktree, or workspace root: %s", cwd)
	}
}

// resolveNamed finds a specific instance by name within the workspace.
func resolveNamed(cwd, instanceFlag string) (*ApplyScope, error) {
	configPath, configDir, err := config.Discover(cwd)
	if err != nil {
		return nil, fmt.Errorf("finding workspace root: %w", err)
	}

	workspaceRoot := filepath.Dir(configDir)
	instances, err := EnumerateInstances(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("enumerating instances: %w", err)
	}

	for _, dir := range instances {
		state, loadErr := LoadState(dir)
		if loadErr != nil {
			continue
		}
		if state.InstanceName == instanceFlag {
			return &ApplyScope{
				Mode:      ApplyNamed,
				Instances: []string{dir},
				Config:    configPath,
			}, nil
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
		return nil, fmt.Errorf("instance %q not found: no instances exist in workspace", instanceFlag)
	}
	return nil, fmt.Errorf("instance %q not found, available instances: %s", instanceFlag, strings.Join(available, ", "))
}
