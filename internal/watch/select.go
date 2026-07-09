package watch

import (
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/github"
)

// DefaultPerRunBound is the first-run safety floor on how many new review
// agents a single `watch --once` pass stages. It prevents a workspace with
// many pending review requests from staging a burst of instances at once.
// Richer, configurable concurrency control is deferred.
const DefaultPerRunBound = 3

// RepoKey is the workspace-membership key for a repository: "owner/repo",
// lowercased so matching is robust to case differences between the GitHub
// search payload and the workspace config.
func RepoKey(owner, repo string) string {
	return strings.ToLower(owner) + "/" + strings.ToLower(repo)
}

// Select turns the raw poll results into the bounded, ordered set of PRs to
// dispatch this run. It is a pure function so the deterministic selection
// contract is table-testable:
//
//   - keep only PRs whose repository is in the workspace (workspaceRepos, keyed
//     by RepoKey) -- a directly-requested PR outside the workspace is dropped;
//   - drop PRs already recorded in the handled-set (handled, keyed by
//     HandledKey);
//   - order the remainder oldest-PR-first by CreatedAt, with a stable tie-break
//     on repo then number so a repeat run over unchanged state selects the same
//     set;
//   - take at most bound.
func Select(prs []github.PRRef, workspaceRepos, handled map[string]bool, bound int) []github.PRRef {
	if bound <= 0 {
		bound = DefaultPerRunBound
	}
	var kept []github.PRRef
	for _, pr := range prs {
		if !workspaceRepos[RepoKey(pr.Owner, pr.Repo)] {
			continue
		}
		if handled[HandledKey(pr.Owner, pr.Repo, pr.Number)] {
			continue
		}
		kept = append(kept, pr)
	}

	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt < b.CreatedAt // oldest first (RFC3339 sorts lexically)
		}
		ak, bk := RepoKey(a.Owner, a.Repo), RepoKey(b.Owner, b.Repo)
		if ak != bk {
			return ak < bk
		}
		return a.Number < b.Number
	})

	if len(kept) > bound {
		kept = kept[:bound]
	}
	return kept
}
