package cli

import (
	"fmt"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// resolveEffectiveWorkspaceName loads the workspace-root init state and
// returns the effective workspace name via workspace.EffectiveConfigName,
// honoring any ConfigNameOverride recorded by `niwa init <name>`.
//
// This is the canonical resolution path the cli layer uses before
// looking up registry entries or invoking Applier.Create. apply.go,
// create.go, and reset.go all funnel through here so the
// LoadState + EffectiveConfigName pair stays in one place; future
// commands that consume cfg.Workspace.Name should call this helper
// rather than reading cfg directly.
//
// Errors are limited to ValidateInitName failures on a tampered
// override (defense in depth per Security §4); a missing or
// unreadable workspace-root state file is treated as the no-override
// case and falls through to cfg.Workspace.Name.
func resolveEffectiveWorkspaceName(workspaceRoot string, cfg *config.WorkspaceConfig) (string, error) {
	wsRootState, _ := workspace.LoadState(workspaceRoot)
	name, err := workspace.EffectiveConfigName(wsRootState, cfg)
	if err != nil {
		return "", fmt.Errorf("resolving effective workspace name: %w", err)
	}
	return name, nil
}
