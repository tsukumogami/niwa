package workspace

import (
	"fmt"

	"github.com/tsukumogami/niwa/internal/config"
)

// EffectiveConfigName resolves the workspace name that downstream
// commands should surface. When `niwa init <name>` was invoked with an
// explicit positional name, that name is persisted in
// state.ConfigNameOverride at the workspace root. Apply.Create and
// Apply.Apply call this helper so the same name flows into the
// instance-state file's ConfigName field, and from there into
// `niwa status`, `niwa apply` output, and any other reader of
// instance state.
//
// When state is non-nil and ConfigNameOverride is non-empty, the
// override is re-validated via ValidateInitName before being returned.
// This is defense in depth against persistence-boundary tampering: the
// state file lives at <workspaceRoot>/.niwa/instance.json with 0o644
// permissions, and a process with write access to that path could
// rewrite the override to a value that fails the original init-time
// validation. Re-validating at every apply forces the value to clear
// the same regex+blacklist gate.
//
// When the override is empty (or state is nil), the cloned config's
// [workspace] name is returned without re-validation; that field has
// already been validated at config.Load time.
//
// IMPORTANT for callers: passing state == nil silently disables override
// resolution and falls back to cfg.Workspace.Name. Any caller that wants
// the override to take effect MUST load the workspace-root state first
// via LoadState(workspaceRoot) and pass the result. The cli package's
// init / apply / create / reset commands all follow this pattern; new
// commands that consume cfg.Workspace.Name MUST do the same or the
// override silently disappears.
func EffectiveConfigName(state *InstanceState, cfg *config.WorkspaceConfig) (string, error) {
	if state != nil && state.ConfigNameOverride != "" {
		if err := ValidateInitName(state.ConfigNameOverride); err != nil {
			return "", fmt.Errorf("invalid ConfigNameOverride in instance state: %w", err)
		}
		return state.ConfigNameOverride, nil
	}
	if cfg == nil {
		return "", fmt.Errorf("EffectiveConfigName: nil config and no override")
	}
	return cfg.Workspace.Name, nil
}
