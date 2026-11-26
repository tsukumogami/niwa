package resolve

import (
	"context"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func TestDeepCopyRepos_CopiesShareGitHistoryByValue(t *testing.T) {
	for _, want := range []bool{true, false} {
		v := want
		original := map[string]config.RepoOverride{"app": {ShareGitHistory: &v}}

		out := deepCopyRepos(original)
		got := out["app"].ShareGitHistory
		if got == nil || *got != want {
			t.Fatalf("copy's share_git_history = %v, want %t", got, want)
		}

		*got = !want
		if *original["app"].ShareGitHistory != want {
			t.Fatalf("changing the copy's share_git_history changed the original to %t", *original["app"].ShareGitHistory)
		}
	}

	out := deepCopyRepos(map[string]config.RepoOverride{"app": {}})
	if out["app"].ShareGitHistory != nil {
		t.Fatal("an unset share_git_history came out of the copy set")
	}
}

// The resolve path is where a dropped field would bite: apply reads the
// resolved config, so a repo's off setting lost here would borrow anyway.
func TestResolveWorkspace_ShareGitHistorySurvives(t *testing.T) {
	off := false
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Repos:     map[string]config.RepoOverride{"app": {ShareGitHistory: &off}},
	}
	out, err := ResolveWorkspace(context.Background(), cfg, ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	if out.ShareGitHistoryEnabled("app") {
		t.Fatal("the resolved config lost the repo's share_git_history = false")
	}
}
