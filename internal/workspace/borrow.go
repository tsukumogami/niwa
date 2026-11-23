package workspace

import "github.com/tsukumogami/niwa/internal/config"

// borrowEnabled reports whether repo's new clone borrows objects from the
// host mirror. Every borrowing entry point asks this one function, so the
// release gate can't be skipped by a caller that reads the setting itself:
// the share_git_history cascade's answer, ANDed with borrowReleased, which is
// false in every release build.
func borrowEnabled(cfg *config.WorkspaceConfig, repo string) bool {
	return borrowReleased && cfg.ShareGitHistoryEnabled(repo)
}
