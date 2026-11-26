package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/workspace"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// goneJudgement is what judgeGoneWorktree decided for a destroy that goes on.
type goneJudgement struct {
	gone, branchAbsent, repoAbsent bool
}

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
// gone is set when the destroy goes on through the gone-worktree path, in
// one of three cases. The branch is fully held, and is deleted. git
// positively answers that the branch doesn't exist (branchAbsent), and no
// branch delete runs. Or the session's repository is positively absent from
// the instance (repoAbsent), so DestroySession runs no git call and deletes
// nothing. A branch that isn't fully held, or any read that fails or gives
// anything short of a positive absence, keeps: a line on stderr names the
// gone worktree and the reason, the record stays Active, no git worktree or
// branch call runs, and the error is exit 1. A worktree path that can't be
// read keeps the same way. In every other case gone is false and the destroy
// runs as before.
func judgeGoneWorktree(cmd *cobra.Command, instanceRoot, sessionID string) (goneJudgement, error) {
	if !sessionDestroyForce {
		return goneJudgement{}, nil
	}
	st, err := worktree.ReadSessionLifecycleState(filepath.Join(instanceRoot, ".niwa", "sessions"), sessionID)
	if err != nil || st.Status != worktree.SessionStatusActive {
		return goneJudgement{}, nil
	}
	branch := st.EffectiveBranchName()
	if worktree.ValidateRecordFields(instanceRoot, st.WorktreePath, branch) != nil {
		return goneJudgement{}, nil
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
		return goneJudgement{}, nil
	} else if !errors.Is(lerr, fs.ErrNotExist) {
		fmt.Fprintf(stderr, "worktree %s for session %s can't be read; keeping branch %s: %s\n",
			path, id, name, removal.ScrubEvidence(lerr.Error(), 400))
		return goneJudgement{}, &sessionattach.ExitCodeError{Code: 1}
	}

	repoDir, ferr := worktree.FindRepoInWorkspace(instanceRoot, st.Repo)
	if ferr != nil {
		// Only a positive absence goes on: DestroySession finds no
		// repository either, so it runs no git call and deletes nothing; it
		// only ends the record. Anything it can't read keeps.
		if why := repoAbsentFromInstance(instanceRoot, st.Repo); why != "" {
			return goneJudgement{}, keep(why)
		}
		fmt.Fprintf(stderr, "worktree %s for session %s is already gone, and its repository isn't in the instance; no branch is deleted\n",
			path, id)
		return goneJudgement{gone: true, repoAbsent: true}, nil
	}

	class, cerr := workspace.ClassifyCwd(instanceRoot)
	if cerr != nil {
		return goneJudgement{}, keep("the workspace root can't be found: " + cerr.Error())
	}
	if class.WorkspaceRoot == "" {
		return goneJudgement{}, keep("the workspace root can't be found for this instance")
	}
	cfg, cfgErr := loadWorkspaceConfig(class.WorkspaceRoot)
	if cfgErr != nil && !errors.Is(cfgErr, fs.ErrNotExist) {
		return goneJudgement{}, keep("the workspace configuration can't be read: " + cfgErr.Error())
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
	absent, why := env.BranchHeld(ctx, instanceRoot, repoDir, branch, st.WorktreePath, removalPolicy(cfg, env))
	if why != "" {
		return goneJudgement{}, keep(why)
	}
	if absent {
		fmt.Fprintf(stderr, "worktree %s for session %s is already gone; branch already absent: %s, so there is nothing to delete\n",
			path, id, name)
		return goneJudgement{gone: true, branchAbsent: true}, nil
	}
	fmt.Fprintf(stderr, "worktree %s for session %s is already gone; its proof remotes hold branch %s, so it is deleted\n",
		path, id, name)
	return goneJudgement{gone: true}, nil
}

// repoAbsentFromInstance returns "" only when repo is positively absent from
// instanceRoot: the instance root lists, and every place DestroySession's
// search would find it, <instance>/<group>/<repo> under each top-level
// directory that isn't hidden, reports ENOENT to Lstat. Otherwise it says
// why the absence can't be shown: the root can't be listed, a candidate
// can't be read (a group directory that can't be searched among them), or
// something is there that the search doesn't take for the repository.
func repoAbsentFromInstance(instanceRoot, repo string) string {
	entries, err := os.ReadDir(instanceRoot)
	if err != nil {
		return "the instance root can't be read, so the session's repository can't be shown absent: " + err.Error()
	}
	for _, top := range entries {
		if !top.IsDir() || strings.HasPrefix(top.Name(), ".") {
			continue
		}
		candidate := filepath.Join(instanceRoot, top.Name(), repo)
		_, lerr := os.Lstat(candidate)
		switch {
		case lerr == nil:
			return candidate + " is there but isn't a repository the destroy can use"
		case !errors.Is(lerr, syscall.ENOENT):
			return candidate + " can't be read: " + lerr.Error()
		}
	}
	return ""
}
