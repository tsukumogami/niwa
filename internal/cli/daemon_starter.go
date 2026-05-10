package cli

import (
	"errors"

	"github.com/tsukumogami/niwa/internal/mcp"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// makeDaemonStarter wraps workspace.EnsureDaemonRunning to translate the
// workspace-package timeout sentinel into the mcp-package equivalent. The mcp
// package can't import workspace (workspace already imports mcp), so this
// translation lives in the cli wiring layer where both are already imported.
//
// Used by mcp-serve and the session lifecycle CLI commands when wiring the
// MCP server's daemon-start hook. handleCreateSession in internal/mcp uses
// errors.Is(err, mcp.ErrDaemonSpawnTimeout) to detect the timeout class and
// roll back the worktree, branch, and session-state file.
func makeDaemonStarter() func(instanceRoot string, extraEnv []string) error {
	return func(instanceRoot string, extraEnv []string) error {
		if err := workspace.EnsureDaemonRunning(instanceRoot, extraEnv); err != nil {
			if errors.Is(err, workspace.ErrDaemonSpawnTimeout) {
				return mcp.ErrDaemonSpawnTimeout
			}
			return err
		}
		return nil
	}
}
