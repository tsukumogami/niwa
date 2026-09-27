package cli

import (
	"context"
	"fmt"
	"sync"

	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// githubForge answers the destroy scan's pull request lookups from the GitHub
// API. The token is resolved on the first lookup, so a destroy whose repos
// never need the forge doesn't run `gh auth token`.
type githubForge struct {
	once   sync.Once
	client *github.APIClient
}

// newDestroyForge returns the forge the destroy scan uses.
func newDestroyForge() workspace.Forge {
	return &githubForge{}
}

func (f *githubForge) HeadPR(ctx context.Context, owner, repo, branch string) (workspace.HeadPR, error) {
	f.once.Do(func() {
		f.client = github.NewAPIClient(resolveGitHubToken())
	})
	// A branch in a fork is proposed to the parent repository, so its pull
	// requests live there, and asking the fork finds none. "No PR" would then
	// read as "nothing will delete this branch", which is exactly the wrong
	// answer when the parent merged the PR and the branch is due for deletion.
	// Until fork PRs are looked up in the parent, a fork gets no answer, and
	// the scan treats the branch as unverified.
	fork, parent, err := f.client.RepoForkInfo(ctx, owner, repo)
	if err != nil {
		return workspace.HeadPR{}, err
	}
	if fork {
		return workspace.HeadPR{}, fmt.Errorf("%s/%s is a fork of %s, whose pull requests aren't checked", owner, repo, parent)
	}
	pulls, err := f.client.ListPullsByHead(ctx, owner, repo, branch)
	if err != nil {
		return workspace.HeadPR{}, err
	}
	return classifyHeadPulls(pulls), nil
}

// classifyHeadPulls reduces the PRs a head branch has had to one state. An
// open PR wins, since it keeps the branch alive whatever happened before;
// otherwise a merged PR means the branch is due for deletion; otherwise only
// closed-unmerged PRs are left, and nothing keeps the branch.
func classifyHeadPulls(pulls []github.PullByHead) workspace.HeadPR {
	var merged, closed *github.PullByHead
	for i := range pulls {
		p := &pulls[i]
		switch {
		case p.State == "open":
			return workspace.HeadPR{State: workspace.HeadPROpen, Number: p.Number}
		case p.MergedAt != "":
			if merged == nil || p.Number > merged.Number {
				merged = p
			}
		default:
			if closed == nil || p.Number > closed.Number {
				closed = p
			}
		}
	}
	switch {
	case merged != nil:
		return workspace.HeadPR{State: workspace.HeadPRMerged, Number: merged.Number}
	case closed != nil:
		return workspace.HeadPR{State: workspace.HeadPRClosed, Number: closed.Number}
	default:
		return workspace.HeadPR{State: workspace.HeadPRNone}
	}
}
