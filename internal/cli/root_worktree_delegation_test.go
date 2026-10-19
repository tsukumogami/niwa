package cli

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestRootWorktreeDelegation checks the workspace root's decision: the hook or
// the deny for the probe result by default, nothing when the workspace turns
// worktree_delegation off.
func TestRootWorktreeDelegation(t *testing.T) {
	off := false

	wd, err := rootWorktreeDelegation(&config.WorkspaceConfig{}, workspace.HarnessSupport{WorktreeHooks: true})
	if err != nil || wd == nil || !wd.Supported || wd.NiwaPath == "" {
		t.Errorf("default + supported: got %+v, %v; want a supported delegation with a niwa path", wd, err)
	}

	wd, err = rootWorktreeDelegation(&config.WorkspaceConfig{}, workspace.HarnessSupport{WorktreeHooks: false})
	if err != nil || wd == nil || wd.Supported {
		t.Errorf("default + unsupported: got %+v, %v; want the deny branch", wd, err)
	}

	wd, err = rootWorktreeDelegation(&config.WorkspaceConfig{Claude: config.ClaudeConfig{WorktreeDelegation: &off}}, workspace.HarnessSupport{WorktreeHooks: true})
	if err != nil || wd != nil {
		t.Errorf("worktree_delegation = false: got %+v, %v; want nil", wd, err)
	}

	if wd, err = rootWorktreeDelegation(nil, workspace.HarnessSupport{WorktreeHooks: true}); err != nil || wd != nil {
		t.Errorf("nil config: got %+v, %v; want nil", wd, err)
	}
}
