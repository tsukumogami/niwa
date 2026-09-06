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

// EffectiveWorktreeSetup returns whether the named repo's own scripts/setup/
// should run against a worktree of that repo. Resolution order mirrors
// EffectiveReadEnvExample above -- repo override, then workspace setting --
// with one deliberate difference:
//
//	When both are nil the answer is FALSE.
//
// That inversion is the whole safety property of the feature. Every setup
// script that exists predates worktrees running one, and a script that walks
// up from its working directory lands on the instance root from a clone and on
// the instance's internal directory from a worktree -- a path that exists and
// is writable, so it writes to the wrong place and exits 0. Defaulting on
// would break those scripts silently; defaulting off means a repo opts in
// after someone has read what a script may assume.
//
// It is exported for the same reason its sibling is: internal/workspace calls
// it rather than duplicating the cascade.
func EffectiveWorktreeSetup(ws *WorkspaceConfig, repoName string) bool {
	if ws == nil {
		return false
	}

	if override, ok := ws.Repos[repoName]; ok && override.WorktreeSetup != nil {
		return *override.WorktreeSetup
	}

	if ws.Workspace.WorktreeSetup != nil {
		return *ws.Workspace.WorktreeSetup
	}

	return false
}
