package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/workspace"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// judgeGoneWorktree runs before a forced `niwa worktree destroy` of one
// session, on the --by-path route too. It acts only when the destroy is
// forced, the session record reads Active, its fields pass
// ValidateRecordFields and its worktree directory is gone: a failed Ended
// write after a successful removal leaves exactly that, and force alone
// would have deleted the session branch with whatever it held. Then the
// session branch, and the gone worktree's HEAD when git still lists one, is
// judged against the proof remotes through the removal pass's environment,
// after the repository's configuration and identity checks.
//
// gone is true when the destroy goes on through the gone-worktree path: the
// branch is fully held, or the session's repository isn't in the instance,
// so DestroySession deletes no branch. A branch that isn't fully held, or
// any read that fails, keeps: a line on stderr names the gone worktree and
// the reason, the record stays Active, no git worktree or branch call runs,
// and the error is exit 1. A worktree path that can't be read keeps the same
// way. In every other case gone is false and the destroy runs as before.
func judgeGoneWorktree(cmd *cobra.Command, instanceRoot, sessionID string) (gone bool, err error) {
	if !sessionDestroyForce {
		return false, nil
	}
	st, err := worktree.ReadSessionLifecycleState(filepath.Join(instanceRoot, ".niwa", "sessions"), sessionID)
	if err != nil || st.Status != worktree.SessionStatusActive {
		return false, nil
	}
	branch := st.EffectiveBranchName()
	if worktree.ValidateRecordFields(instanceRoot, st.WorktreePath, branch) != nil {
		return false, nil
	}
	stderr := cmd.ErrOrStderr()
	path := removal.ScrubEvidence(st.WorktreePath, 400)
	id := removal.ScrubEvidence(st.SessionID, 200)
	name := removal.ScrubEvidence(branch, 200)
	keep := func(reason string) error {
		fmt.Fprintf(stderr, "worktree %s for session %s is already gone; keeping branch %s: %s\n",
			path, id, name, removal.ScrubEvidence(reason, 400))
		return &sessionattach.ExitCodeError{Code: 1}
	}

	if _, lerr := os.Lstat(st.WorktreePath); lerr == nil {
		return false, nil
	} else if !errors.Is(lerr, fs.ErrNotExist) {
		fmt.Fprintf(stderr, "worktree %s for session %s can't be read; keeping branch %s: %s\n",
			path, id, name, removal.ScrubEvidence(lerr.Error(), 400))
		return false, &sessionattach.ExitCodeError{Code: 1}
	}

	repoDir, ferr := worktree.FindRepoInWorkspace(instanceRoot, st.Repo)
	if ferr != nil {
		// DestroySession finds no repository either, so it runs no git call
		// and deletes no branch; it only ends the record.
		fmt.Fprintf(stderr, "worktree %s for session %s is already gone, and its repository isn't in the instance; no branch is deleted\n",
			path, id)
		return true, nil
	}

	class, cerr := workspace.ClassifyCwd(instanceRoot)
	if cerr != nil {
		return false, keep("the workspace root can't be found: " + cerr.Error())
	}
	if class.WorkspaceRoot == "" {
		return false, keep("the workspace root can't be found for this instance")
	}
	cfg, cfgErr := loadWorkspaceConfig(class.WorkspaceRoot)
	if cfgErr != nil && !errors.Is(cfgErr, fs.ErrNotExist) {
		return false, keep("the workspace configuration can't be read: " + cfgErr.Error())
	}
	if cfgErr != nil {
		cfg = nil
	}

	env := newRemovalEnv(class.WorkspaceRoot, userHomeDir(), os.Getenv("PATH"))
	defer env.Close()
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, passDeadline)
	defer cancel()
	if why := env.BranchHeld(ctx, instanceRoot, repoDir, branch, st.WorktreePath, removalPolicy(cfg, env)); why != "" {
		return false, keep(why)
	}
	fmt.Fprintf(stderr, "worktree %s for session %s is already gone; its proof remotes hold branch %s, so it is deleted\n",
		path, id, name)
	return true, nil
}
