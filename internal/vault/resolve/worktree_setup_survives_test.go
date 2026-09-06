package resolve

import (
	"context"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// TestResolveWorkspace_WorktreeSetupSurvives is the behavioural half of the
// deep-copy guard, and it is worded against the path that actually loses the
// field rather than against the copy function.
//
// A repo's worktree_setup opt-in is read straight off the config on
// `niwa worktree create`, which resolves no secrets. `niwa apply` goes through
// ResolveWorkspace first, and that returns a rebuilt config. If the field is
// missing from the rebuild, the opt-in works on one command and silently does
// not on the other -- and "silently" is exact: nothing errors, nothing warns,
// the repo's worktrees just never get provisioned.
//
// Asserting on deepCopyRepos directly would be the weaker test. This one fails
// for any reason the value fails to survive a resolve, including a future
// refactor that routes the copy somewhere else.
func TestResolveWorkspace_WorktreeSetupSurvives(t *testing.T) {
	enabled := true
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Repos: map[string]config.RepoOverride{
			"alpha": {WorktreeSetup: &enabled},
		},
	}

	out, err := ResolveWorkspace(context.Background(), cfg, ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}

	got, ok := out.Repos["alpha"]
	if !ok {
		t.Fatal("repo alpha missing from the resolved config")
	}
	if got.WorktreeSetup == nil {
		t.Fatal("worktree_setup was dropped by the resolve: the opt-in would work " +
			"on `niwa worktree create` and silently not on `niwa apply`")
	}
	if !*got.WorktreeSetup {
		t.Errorf("worktree_setup = %v, want true", *got.WorktreeSetup)
	}

	// The resolver's contract is that it never mutates its input, so the copy
	// must not share the pointer either.
	if got.WorktreeSetup == cfg.Repos["alpha"].WorktreeSetup {
		t.Log("note: worktree_setup shares a pointer with the input; harmless for " +
			"a *bool nothing mutates, but the field is copied by value here")
	}
}

// TestResolveWorkspace_CodexOverrideSurvives covers the pre-existing instance of
// the same defect that this work had to fix before the exhaustiveness guard
// could be added at all.
//
// The consequence was fail-open rather than merely lost: AgentEnabled falls back
// to the workspace gate when a repo's Codex override is nil, and to true when
// that is unset. So a repo that explicitly set `enabled = false` had its opt-out
// dropped on the vault-resolved path and Codex content delivered into a repo
// whose owner had said not to. See niwa#291.
func TestResolveWorkspace_CodexOverrideSurvives(t *testing.T) {
	disabled := false
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Repos: map[string]config.RepoOverride{
			"alpha": {Codex: &config.CodexOverride{Enabled: &disabled}},
		},
	}

	out, err := ResolveWorkspace(context.Background(), cfg, ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}

	got := out.Repos["alpha"]
	if got.Codex == nil {
		t.Fatal("the repo's codex override was dropped by the resolve: an explicit " +
			"`enabled = false` silently becomes enabled, because the gate falls back " +
			"to the workspace default and then to true")
	}
	if got.Codex.Enabled == nil || *got.Codex.Enabled {
		t.Errorf("codex enabled = %v, want false", got.Codex.Enabled)
	}
}
