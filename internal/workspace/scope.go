package workspace

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// ApplyMode describes which instances a workspace apply should target.
type ApplyMode int

const (
	// ApplySingle targets the single instance the user is currently inside.
	ApplySingle ApplyMode = iota
	// ApplyAll targets every instance under the workspace root.
	ApplyAll
	// ApplyNamed targets a specific instance selected by name.
	ApplyNamed
)

// ApplyScope holds the resolved target for a workspace apply operation.
type ApplyScope struct {
	Mode      ApplyMode
	Instances []string // absolute paths to instance roots
	Config    string   // path to workspace.toml
}

// ResolveApplyScope determines which instances to target based on the current
// working directory and an optional instance name flag.
//
// Resolution order:
//  1. If instanceFlag is non-empty, find the workspace root via config.Discover,
//     enumerate instances, and match by InstanceName. Returns ApplyNamed.
//  2. If cwd is inside an instance (DiscoverInstance succeeds), return ApplySingle
//     targeting that instance.
//  3. If cwd is inside a workspace (config.Discover succeeds), enumerate all
//     instances and return ApplyAll.
//  4. Error if none of the above applies.
func ResolveApplyScope(cwd, instanceFlag string) (*ApplyScope, error) {
	if instanceFlag != "" {
		return resolveNamed(cwd, instanceFlag)
	}

	// Try to find an instance first (more specific).
	instanceRoot, err := DiscoverInstance(cwd)
	if err == nil {
		configPath, _, configErr := config.Discover(cwd)
		if configErr != nil {
			// Inside an instance but can't find workspace config -- unusual
			// but we can still proceed with the instance path alone.
			configPath = ""
		}
		return &ApplyScope{
			Mode:      ApplySingle,
			Instances: []string{instanceRoot},
			Config:    configPath,
		}, nil
	}

	// Try to find a workspace root.
	configPath, configDir, err := config.Discover(cwd)
	if err != nil {
		return nil, fmt.Errorf("not inside a workspace instance or workspace root: %w", err)
	}

	workspaceRoot := filepath.Dir(configDir)
	instances, err := EnumerateInstances(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("enumerating instances: %w", err)
	}

	return &ApplyScope{
		Mode:      ApplyAll,
		Instances: instances,
		Config:    configPath,
	}, nil
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
