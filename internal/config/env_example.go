package config

// EffectiveReadEnvExample returns whether the .env.example pre-pass should
// run for the named repo. Resolution order:
//
//  1. If the repo has an explicit per-repo override, that value wins.
//  2. Otherwise the workspace-level setting applies.
//  3. When both are nil the feature is enabled (opt-out default).
//
// It is exported so that internal/workspace can call it without duplicating
// the resolution logic.
func EffectiveReadEnvExample(ws *WorkspaceConfig, repoName string) bool {
	if ws == nil {
		return true
	}

	// Check per-repo override first.
	if override, ok := ws.Repos[repoName]; ok && override.ReadEnvExample != nil {
		return *override.ReadEnvExample
	}

	// Fall back to workspace-level setting.
	if ws.Workspace.ReadEnvExample != nil {
		return *ws.Workspace.ReadEnvExample
	}

	// Both nil: feature is on by default.
	return true
}
