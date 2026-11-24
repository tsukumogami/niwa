package cli

import (
	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// The removal package's no-follow walk is a worktree.WorktreeRemover, which
// the worktree package declares without importing internal/removal.
var _ worktree.WorktreeRemover = removal.NoFollowRemover
