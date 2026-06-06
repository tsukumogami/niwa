package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/source"
)

// GitInvoker is the test-injection seam for the bootstrap pipeline's git
// subprocess calls. Production wires stdGitInvoker{}, which delegates to
// exec.CommandContext. Tests pass a recording invoker that captures argv
// and supplies fake outputs without forking real git, so unit coverage
// can assert classifier ordering and argument shape without a working
// git binary on PATH.
//
// The interface deliberately exposes only CommandContext (returning the
// *exec.Cmd verbatim) so production code retains full control over
// stdin/stdout/stderr/env wiring. A higher-level "RunGitArgs" shape was
// considered and rejected because it would force the seam to absorb
// every stdio configuration the orchestrator wants to apply.
type GitInvoker interface {
	CommandContext(ctx context.Context, args ...string) *exec.Cmd
}

// stdGitInvoker is the production implementation of GitInvoker. It
// invokes `git` resolved via PATH. Callers configure stdin/stdout/env
// on the returned *exec.Cmd.
type stdGitInvoker struct{}

// CommandContext returns exec.CommandContext(ctx, "git", args...).
func (stdGitInvoker) CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "git", args...)
}

// StdGitInvoker returns the production GitInvoker. The function form
// (rather than an exported var) keeps the zero value of stdGitInvoker
// internal so external callers can't mutate it.
func StdGitInvoker() GitInvoker { return stdGitInvoker{} }

// CreateSessionFunc is the callback shape RunBootstrap invokes to create
// the bootstrap session. The signature mirrors worktree.CreateSession but
// uses basic types so workspace stays ignorant of the worktree package and
// the seam stays available as a test injection point. The cli layer adapts
// the two by passing a thin closure that wraps worktree.CreateSession.
//
// The branchName return is the persisted branch the orchestrator must
// pass to the session-destroy helper if the post-create rollback fires.
type CreateSessionFunc func(ctx context.Context, instanceRoot, repo, purpose, branchPrefix string, gitInvoker GitInvoker) (sessionID, worktreePath, branchName string, err error)

// DestroySessionFunc is the callback shape RunBootstrap invokes to tear
// down a successfully-created session when the post-create commit step
// fails. Mirrors `niwa session destroy --force <sid>` semantics:
// removes the worktree, deletes the branch, removes the session state
// JSON. Best-effort; errors are logged but do not block return.
type DestroySessionFunc func(ctx context.Context, instanceRoot, sessionID string, gitInvoker GitInvoker) error

// ApplierCreateFunc is the callback shape RunBootstrap invokes to run
// the create-step pipeline. Wraps Applier.Create with the standard
// configDir convention (configDir == <workspaceRoot>/.niwa).
type ApplierCreateFunc func(ctx context.Context, workspaceRoot, instanceName string) (instancePath string, err error)

// BootstrapParams collects the inputs to RunBootstrap.
//
// Field rationale:
//
//   - WorkspaceRoot — absolute path to the workspace being scaffolded.
//   - WorkspaceName — name written into [workspace] name = "...".
//   - InstanceName  — directory name for the create step's output.
//     For a brand-new workspace this is conventionally WorkspaceName
//     (computeInstanceName's "first instance" rule).
//   - Src           — parsed --from slug.
//   - GitInvoker    — production stdGitInvoker{} or a test recorder.
//   - Reporter      — TTY-aware writer for the success block, R17 note,
//     and R18 commit summary.
//   - ScaffoldOpts  — passed to the post-create scaffold call inside
//     the bootstrap repo. MUST equal the ScaffoldOptions runInit used
//     for the pre-create write so on-disk bytes match (Appendix A
//     contract).
//   - ApplierCreate — seam for the create-step pipeline.
//   - CreateSession — seam for the session-create step.
//   - DestroySession — seam for the post-session-create rollback.
type BootstrapParams struct {
	WorkspaceRoot  string
	WorkspaceName  string
	InstanceName   string
	Src            source.Source
	GitInvoker     GitInvoker
	Reporter       *Reporter
	ScaffoldOpts   ScaffoldOptions
	ApplierCreate  ApplierCreateFunc
	CreateSession  CreateSessionFunc
	DestroySession DestroySessionFunc
}

// BootstrapResult is the success-path output of RunBootstrap.
type BootstrapResult struct {
	InstancePath string
	SessionID    string
	WorktreePath string
	BranchName   string
}

// commitEnvBlocklistKeys are the GIT_* keys filtered out of os.Environ
// before the bootstrap commit invocation. Removing these is the R18
// argv-layer invariant: niwa must not propagate caller-provided author
// identity into the commit. Filtering is explicit so a parent process
// that exports GIT_AUTHOR_NAME cannot inject identity by inheritance.
var commitEnvBlocklistKeys = []string{
	"GIT_AUTHOR_NAME",
	"GIT_AUTHOR_EMAIL",
	"GIT_AUTHOR_DATE",
	"GIT_COMMITTER_NAME",
	"GIT_COMMITTER_EMAIL",
	"GIT_COMMITTER_DATE",
}

// sanitizeCommitEnv returns env with every GIT_AUTHOR_* / GIT_COMMITTER_*
// entry removed. The filter matches on the exact key prefix so unrelated
// GIT_* variables (e.g., GIT_DIR, GIT_TERMINAL_PROMPT) are preserved.
func sanitizeCommitEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		key := kv[:eq]
		blocked := false
		for _, b := range commitEnvBlocklistKeys {
			if key == b {
				blocked = true
				break
			}
		}
		if !blocked {
			out = append(out, kv)
		}
	}
	return out
}

// ErrBootstrapNonGitHub is the defense-in-depth error RunBootstrap
// returns when params.Src.IsGitHub() reports false. The cli layer's R9
// gate already covers this case earlier in the pipeline; the workspace
// layer enforces it again as a contract assertion so any caller that
// bypasses runInit (a future test, an embed) cannot reach a git
// invocation against a non-GitHub host.
var ErrBootstrapNonGitHub = errors.New("non-github host")

// RunBootstrap orchestrates the bootstrap pipeline:
//
//  1. Verify Src.IsGitHub() (defense-in-depth; no git invocation on fail).
//  2. Validate the slug shape via ResolveCloneURL (no network call).
//  3. Call ApplierCreate to install the create-step (channels infra,
//     bootstrap repo clone). Applier.Create owns its own teardown on
//     failure (it runs os.RemoveAll on the instance dir before
//     returning errors). Per PRD R7, failures AFTER ApplierCreate
//     succeeds MUST NOT touch the instance dir — session/commit
//     rollback is scoped to the session worktree + branch + state JSON.
//  4. Call CreateSession with BranchPrefix="niwa-bootstrap/".
//  5. ScaffoldFromSource the worktree (second scaffold write — same
//     opts as runInit's first write so bytes are identical).
//  6. git add .niwa/ + git commit (NO --author flag; GIT_AUTHOR_* /
//     GIT_COMMITTER_* env filtered).
//  7. On any failure between session-create and commit, DestroySession
//     runs to clean up worktree + branch + session state JSON.
//
// RunBootstrap does NOT do its own workspace-root scaffold write —
// runInit performs that BEFORE calling RunBootstrap, then disarms its
// workspaceCreated defer. The runInit-owned write is what
// Applier.Create reads to discover [channels.mesh].
func RunBootstrap(ctx context.Context, params BootstrapParams) (BootstrapResult, error) {
	// Step 1: defense-in-depth host check. The cli layer's R9 gate
	// already runs upstream of runInit, but the test contract requires
	// that calling RunBootstrap directly with a non-GitHub source
	// records ZERO git invocations.
	if !params.Src.IsGitHub() {
		return BootstrapResult{}, fmt.Errorf("%w: bootstrap supports only GitHub sources in v1; got host=%s",
			ErrBootstrapNonGitHub, params.Src.Host)
	}

	if params.GitInvoker == nil {
		return BootstrapResult{}, errors.New("RunBootstrap: GitInvoker is required")
	}
	if params.ApplierCreate == nil {
		return BootstrapResult{}, errors.New("RunBootstrap: ApplierCreate is required")
	}
	if params.CreateSession == nil {
		return BootstrapResult{}, errors.New("RunBootstrap: CreateSession is required")
	}
	if params.DestroySession == nil {
		return BootstrapResult{}, errors.New("RunBootstrap: DestroySession is required")
	}
	if params.InstanceName == "" {
		return BootstrapResult{}, errors.New("RunBootstrap: InstanceName is required")
	}

	// Step 2: resolve cloneURL (validated for downstream consumers).
	// Slug form leaves Src.Host empty; ResolveCloneURL accepts the
	// "owner/repo" canonical shape.
	cloneSlug := params.Src.Owner + "/" + params.Src.Repo
	if _, urlErr := ResolveCloneURL(cloneSlug, "https"); urlErr != nil {
		return BootstrapResult{}, fmt.Errorf("resolving clone URL: %w", urlErr)
	}

	// Step 3: ApplierCreate. The create-step pipeline (clone bootstrap
	// repo, install channels infra) may populate <ws>/<instanceName>
	// partway and then fail; the implicit contract for the create step
	// is "either Applier.Create returns nil OR the instance dir does
	// not survive". Applier.Create itself runs an internal
	// `os.RemoveAll(instanceRoot)` on every error return (see apply.go)
	// so a fresh failure does not leave a half-built instance dir.
	// Per PRD R7, session-step and commit-step failures must PRESERVE
	// the instance, so no defer is armed here: any failure AFTER this
	// point keeps the instance dir on disk.
	instancePath, createErr := params.ApplierCreate(ctx, params.WorkspaceRoot, params.InstanceName)
	if createErr != nil {
		return BootstrapResult{}, fmt.Errorf("bootstrap step=create: %w", createErr)
	}

	// Step 4: CreateSession via the injected seam. BranchPrefix is the
	// load-bearing R5 signal.
	sid, wtPath, branchName, sessErr := params.CreateSession(ctx, instancePath, params.Src.Repo, "bootstrap", "niwa-bootstrap/", params.GitInvoker)
	if sessErr != nil {
		return BootstrapResult{}, fmt.Errorf("bootstrap step=session-create: %w", sessErr)
	}

	// Step 5+: from here, any failure must clean up the session via
	// DestroySession. The instance dir is intentionally NOT torn down on
	// any error path below (PRD R7 session-step / commit-step contract).
	sessionCreated := true
	defer func() {
		if sessionCreated {
			_ = params.DestroySession(ctx, instancePath, sid, params.GitInvoker)
		}
	}()

	// Step 6: SECOND scaffold write inside the worktree. SAME options
	// as runInit's first write — byte-identical content per Appendix A.
	if scErr := ScaffoldFromSource(wtPath, params.ScaffoldOpts); scErr != nil {
		return BootstrapResult{}, fmt.Errorf("bootstrap step=session-create: writing worktree scaffold: %w", scErr)
	}

	// Step 7: git add + git commit. Both flow through GitInvoker.
	addCmd := params.GitInvoker.CommandContext(ctx, "-C", wtPath, "add", ".niwa/")
	if out, addErr := addCmd.CombinedOutput(); addErr != nil {
		return BootstrapResult{}, fmt.Errorf("bootstrap step=session-create: git add: %w\n%s", addErr, out)
	}
	commitCmd := params.GitInvoker.CommandContext(ctx, "-C", wtPath, "commit", "-m", "Initial niwa workspace config")
	// R18 argv-layer invariant: filter GIT_AUTHOR_* / GIT_COMMITTER_*
	// from os.Environ() so the parent process cannot inject author
	// identity through inheritance.
	commitCmd.Env = sanitizeCommitEnv(os.Environ())
	if out, commitErr := commitCmd.CombinedOutput(); commitErr != nil {
		return BootstrapResult{}, fmt.Errorf("bootstrap step=session-create: git commit: %w\n%s", commitErr, out)
	}

	// All steps succeeded — disarm the session defer.
	sessionCreated = false
	return BootstrapResult{
		InstancePath: instancePath,
		SessionID:    sid,
		WorktreePath: wtPath,
		BranchName:   branchName,
	}, nil
}

// DefaultDestroySession is the production DestroySessionFunc. It mirrors
// the `niwa session destroy --force <sid>` body: removes the worktree,
// deletes the session branch with force, and removes the session state
// JSON. All calls are best-effort; partial failure is logged via the
// error return but does not block.
func DefaultDestroySession(ctx context.Context, instanceRoot, sessionID string, gitInvoker GitInvoker) error {
	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	statePath := filepath.Join(sessionsDir, sessionID+".json")
	data, readErr := os.ReadFile(statePath)
	if readErr != nil {
		return fmt.Errorf("reading session state: %w", readErr)
	}
	// Minimal field extraction: we only need WorktreePath, Repo,
	// EffectiveBranchName equivalent. To avoid an mcp import here, we
	// read the JSON directly with the known schema.
	type stateShape struct {
		Repo         string `json:"repo"`
		BranchName   string `json:"branch_name,omitempty"`
		SessionID    string `json:"session_id"`
		WorktreePath string `json:"worktree_path"`
	}
	var st stateShape
	if jsonErr := json.Unmarshal(data, &st); jsonErr != nil {
		return fmt.Errorf("parsing session state: %w", jsonErr)
	}
	if st.BranchName == "" {
		st.BranchName = "session/" + st.SessionID
	}

	// Find the repo path so git worktree remove / branch -D can use it.
	repoPath, repoErr := findRepoInWorkspaceForDestroy(instanceRoot, st.Repo)
	if repoErr == nil && st.WorktreePath != "" {
		_ = gitInvoker.CommandContext(ctx, "-C", repoPath, "worktree", "remove", "--force", st.WorktreePath).Run()
		_ = gitInvoker.CommandContext(ctx, "-C", repoPath, "branch", "-D", st.BranchName).Run()
	}
	_ = os.Remove(statePath)
	return nil
}

// findRepoInWorkspaceForDestroy is a workspace-package mirror of the mcp
// helper of the same name (kept local to avoid the mcp import direction).
// Scans instanceRoot two levels deep for a directory named repoName that
// contains a .git entry.
func findRepoInWorkspaceForDestroy(instanceRoot, repoName string) (string, error) {
	topEntries, err := os.ReadDir(instanceRoot)
	if err != nil {
		return "", err
	}
	for _, top := range topEntries {
		if !top.IsDir() || strings.HasPrefix(top.Name(), ".") {
			continue
		}
		groupDir := filepath.Join(instanceRoot, top.Name())
		subEntries, subErr := os.ReadDir(groupDir)
		if subErr != nil {
			continue
		}
		for _, sub := range subEntries {
			if !sub.IsDir() || sub.Name() != repoName {
				continue
			}
			candidate := filepath.Join(groupDir, sub.Name())
			if _, statErr := os.Stat(filepath.Join(candidate, ".git")); statErr == nil {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("repo %q not found in workspace %s", repoName, instanceRoot)
}
