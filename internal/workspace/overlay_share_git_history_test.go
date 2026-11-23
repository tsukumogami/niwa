package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func sghPtr(v bool) *bool { return &v }

func sghState(p *bool) string {
	if p == nil {
		return "unset"
	}
	return fmt.Sprintf("%t", *p)
}

// An overlay repo the base config doesn't name arrives whole, so its
// share_git_history reaches the merged config, from the overlay's TOML as
// ParseOverlay reads it.
func TestMergeWorkspaceOverlay_OverlayOnlyRepoCarriesShareGitHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace-overlay.toml")
	body := "[repos.overlay-only]\nshare_git_history = false\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	overlay, err := config.ParseOverlay(path)
	if err != nil {
		t.Fatalf("ParseOverlay: %v", err)
	}

	merged, err := MergeWorkspaceOverlay(baseWS(), overlay, t.TempDir())
	if err != nil {
		t.Fatalf("MergeWorkspaceOverlay: %v", err)
	}
	got := merged.Repos["overlay-only"].ShareGitHistory
	if got == nil || *got {
		t.Fatalf("merged share_git_history = %s, want false from the overlay", sghState(got))
	}
	if merged.ShareGitHistoryEnabled("overlay-only") {
		t.Fatal("the merged config's cascade says borrow for a repo the overlay turned off")
	}
}

// A repo named in both follows the rule every other *bool repo field
// follows: whatever the merge gives worktree_setup and read_env_example for
// the same pair of values, it gives share_git_history.
func TestMergeWorkspaceOverlay_RepoInBothTreatsShareGitHistoryLikeTheOtherBoolFields(t *testing.T) {
	states := []*bool{nil, sghPtr(true), sghPtr(false)}
	for _, base := range states {
		for _, over := range states {
			name := fmt.Sprintf("base=%s/overlay=%s", sghState(base), sghState(over))
			t.Run(name, func(t *testing.T) {
				ws := baseWS()
				ws.Repos["repo-a"] = config.RepoOverride{
					Branch:          "main",
					ShareGitHistory: base,
					WorktreeSetup:   base,
					ReadEnvExample:  base,
				}
				overlay := &config.WorkspaceOverlay{Repos: map[string]config.RepoOverride{
					"repo-a": {ShareGitHistory: over, WorktreeSetup: over, ReadEnvExample: over},
				}}
				merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
				if err != nil {
					t.Fatalf("MergeWorkspaceOverlay: %v", err)
				}
				got := merged.Repos["repo-a"]
				for field, other := range map[string]*bool{
					"worktree_setup":   got.WorktreeSetup,
					"read_env_example": got.ReadEnvExample,
				} {
					if sghState(got.ShareGitHistory) != sghState(other) {
						t.Errorf("share_git_history merged to %s but %s merged to %s",
							sghState(got.ShareGitHistory), field, sghState(other))
					}
				}
			})
		}
	}
}
