package watch

import "strings"

// WorkspaceScope decides whether a PR's repository belongs to the developer's
// niwa workspace. It supports two membership forms so it maps cleanly onto
// niwa's workspace config: an explicit "owner/repo" set (from sources that list
// their repos, and from per-repo overrides) and a whole-owner set (from sources
// that auto-discover every repo under an org). The matching logic is pure so it
// is table-testable independent of config parsing.
type WorkspaceScope struct {
	exactRepos map[string]bool // keyed by RepoKey(owner, repo)
	wholeOrgs  map[string]bool // keyed by lowercased owner
}

// NewWorkspaceScope builds a scope from an explicit repo list ("owner/repo"
// slugs) and a list of whole-org owners.
func NewWorkspaceScope(exactRepos, wholeOrgs []string) *WorkspaceScope {
	s := &WorkspaceScope{
		exactRepos: map[string]bool{},
		wholeOrgs:  map[string]bool{},
	}
	for _, r := range exactRepos {
		s.exactRepos[strings.ToLower(r)] = true
	}
	for _, o := range wholeOrgs {
		s.wholeOrgs[strings.ToLower(o)] = true
	}
	return s
}

// Contains reports whether owner/repo is in the workspace.
func (s *WorkspaceScope) Contains(owner, repo string) bool {
	if s == nil {
		return false
	}
	if s.exactRepos[RepoKey(owner, repo)] {
		return true
	}
	return s.wholeOrgs[strings.ToLower(owner)]
}

// RepoSet returns the exact-repo membership map for use with Select. A
// whole-org scope cannot be enumerated without a network call, so PRs under a
// whole-org source are matched via Contains at poll time; RepoSet covers the
// explicit-repo case Select consumes directly.
func (s *WorkspaceScope) RepoSet() map[string]bool {
	out := map[string]bool{}
	for k := range s.exactRepos {
		out[k] = true
	}
	return out
}
