package workspace

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

// failingGitHubClient fails the test if the GitHub API is touched. The
// explicit-repos discovery path must not call ListRepos, so this guards
// that contract while we exercise classification offline.
type failingGitHubClient struct{ t *testing.T }

func (f *failingGitHubClient) ListRepos(context.Context, string) ([]github.Repo, error) {
	f.t.Helper()
	f.t.Fatal("ListRepos called: an explicit-repos [[sources]] block must not hit the GitHub API")
	return nil, nil
}

// TestScaffoldFromSource_ClassifiesOwnRepo is the regression test for the
// bootstrap classification gap. The scaffold writes an explicit-repos source
// (no live visibility) alongside its group block; discoverRepos builds those
// repos with an empty Visibility, so the scaffold MUST bind the repo to its
// group by name. Otherwise the repo matches no group, apply produces zero
// repos and no role, and bootstrap dies at session-create with
// "unknown role: role <repo> not found".
//
// This drives the real apply chain (scaffold -> config.Load -> discoverRepos
// -> Classify) end to end — the seam that the stubbed bootstrap tests skip.
func TestScaffoldFromSource_ClassifiesOwnRepo(t *testing.T) {
	for _, tc := range []struct {
		name      string
		private   bool
		wantGroup string
	}{
		{"public", false, "public"},
		{"private", true, "private"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			opts := ScaffoldOptions{
				Name:           "ws",
				Org:            "owner",
				Repo:           "bootstrap-repo",
				Private:        tc.private,
				IncludeGitkeep: true,
			}
			if err := ScaffoldFromSource(dir, opts); err != nil {
				t.Fatalf("ScaffoldFromSource: %v", err)
			}

			result, err := config.Load(filepath.Join(dir, StateDir, WorkspaceConfigFile))
			if err != nil {
				t.Fatalf("loading scaffolded config: %v", err)
			}
			cfg := result.Config

			// Mirror apply: discover each source's repos, then classify.
			applier := NewApplier(&failingGitHubClient{t: t})
			var discovered []github.Repo
			for _, src := range cfg.Sources {
				repos, derr := applier.discoverRepos(context.Background(), src)
				if derr != nil {
					t.Fatalf("discoverRepos: %v", derr)
				}
				discovered = append(discovered, repos...)
			}

			classified, warnings, cerr := Classify(discovered, cfg.Groups)
			if cerr != nil {
				t.Fatalf("Classify: %v", cerr)
			}
			if len(warnings) != 0 {
				t.Errorf("scaffolded repo failed to match its group: %v", warnings)
			}
			if len(classified) != 1 {
				t.Fatalf("expected 1 classified repo, got %d: %+v", len(classified), classified)
			}
			if got := classified[0].Repo.Name; got != "bootstrap-repo" {
				t.Errorf("classified repo name = %q, want bootstrap-repo", got)
			}
			if got := classified[0].Group; got != tc.wantGroup {
				t.Errorf("classified into group %q, want %q", got, tc.wantGroup)
			}
		})
	}
}
