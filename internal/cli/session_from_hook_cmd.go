package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/workspace"
	"github.com/tsukumogami/niwa/internal/worktree"
)

func init() {
	sessionCmd.AddCommand(sessionFromHookCmd)
}

// sessionFromHookCmd is the thin entry Claude Code invokes DIRECTLY as the
// WorktreeCreate/WorktreeRemove hook command (`niwa worktree from-hook`, with
// NO shim script) written into each repo's settings and into the instance
// root's and workspace root's settings. A session launched at a root has no
// repo for its working directory, so the repo can come from a `<repo>/`
// prefix in the worktree name, a one-repo instance, a niwa worktree holding
// cwd, NIWA_WORKTREE_REPO or, for a bare name, the instance's first repo, as
// well as from cwd; see workspace.ResolveHookTarget. It reads the Claude hook JSON on
// stdin and dispatches on hook_event_name to the same create/destroy core the
// human `niwa worktree create`/`destroy` commands use.
//
// The hook I/O contract is the only thing this command owns:
//   - WorktreeCreate: print ONLY the absolute worktree path to stdout, exit 0
//     on success; exit non-zero on any error (Claude then fails creation,
//     which is correct: a partial/wrong worktree is worse than none).
//   - WorktreeRemove: non-blocking, ALWAYS exit 0 (errors are logged to
//     stderr but never fail teardown).
//   - SubagentStop: exit 0 with nothing on stdout once the payload parses,
//     and never exit 2, the code that would send the subagent back to work.
//     It removes the finished Agent-tool subagent's worktree when it is
//     unchanged. An unreadable payload exits 1 like any other event, which
//     Claude Code treats as a non-blocking hook error.
var sessionFromHookCmd = &cobra.Command{
	Use:    "from-hook",
	Short:  "Internal: dispatch a Claude Code worktree hook (reads JSON on stdin)",
	Hidden: true,
	Long: `Internal entry point invoked directly by Claude Code's
WorktreeCreate, WorktreeRemove and SubagentStop hooks. On SubagentStop it
removes the worktree it made for that Agent-tool subagent when the subagent
left it unchanged.

Reads the Claude hook JSON payload on stdin and dispatches on
hook_event_name. Not intended for direct human use; niwa apply writes the
hook command into each repo's settings.local.json and into the instance
root's and workspace root's settings.json. From an instance or workspace
root, name the worktree <repo>/<slug> to pick the repo. A bare name from
there goes to the repo NIWA_WORKTREE_REPO names, or else to the instance's
first repo by group and name.`,
	Args:          cobra.NoArgs,
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE:          runSessionFromHook,
}

// hookPayload is the subset of the Claude Code hook JSON that from-hook reads.
// The fields are a superset across the WorktreeCreate and WorktreeRemove
// events; absent fields decode to their zero value.
//
// WorktreeCreate stdin (from the spike): {session_id, transcript_path, cwd,
// hook_event_name, name}. `cwd` is the repo root; `name` is Claude's worktree
// name (untrusted).
//
// WorktreeRemove stdin schema was NOT exercised by the spike (design flagged
// this as a plan-time risk). We accept `worktree_path` as the primary carrier
// of the worktree directory and fall back to `cwd`. See runFromHookRemove.
//
// SubagentStop stdin (observed with Claude Code 2.1.305): `cwd` is the
// subagent's working directory, which for an Agent-tool subagent with
// worktree isolation is the worktree the WorktreeCreate hook made, and
// `agent_id` is the id Claude Code also used to name that worktree
// (`agent-<agent_id>`). A subagent without isolation reports its parent's
// directory, which is why the id, not the directory, picks the worktree.
type hookPayload struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	Name           string `json:"name"`
	WorktreePath   string `json:"worktree_path"`
	AgentID        string `json:"agent_id"`
}

const (
	hookEventWorktreeCreate = "WorktreeCreate"
	hookEventWorktreeRemove = "WorktreeRemove"
	hookEventSubagentStop   = "SubagentStop"
)

func runSessionFromHook(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return fmt.Errorf("niwa: error: reading hook payload from stdin: %w", err)
	}

	var payload hookPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("niwa: error: parsing hook payload JSON: %w", err)
	}

	switch payload.HookEventName {
	case hookEventWorktreeCreate:
		return runFromHookCreate(cmd, payload)
	case hookEventWorktreeRemove:
		return runFromHookRemove(cmd, payload)
	case hookEventSubagentStop:
		return runFromHookSubagentStop(cmd, payload)
	default:
		// An unrecognized event is a configuration error on the create side
		// (we cannot know whether it should block), so fail loudly rather than
		// silently succeeding.
		return fmt.Errorf("niwa: error: unknown hook_event_name %q", payload.HookEventName)
	}
}

// runFromHookCreate handles a WorktreeCreate hook. It resolves the repo from
// the untrusted stdin cwd via the canonicalizing resolver (the SINGLE
// enforcement point for "reject out-of-workspace cwd"), derives a purpose from
// the untrusted name (control characters stripped), then runs the SAME
// two-step flow `niwa worktree create` uses: CreateSession (worktree + branch +
// state) THEN applyContentToWorktree (secrets + CLAUDE context; R10's
// warn-and-continue surfaced on stderr).
//
// On success it prints ONLY the absolute worktree path to stdout and exits 0.
// On any error it returns a non-zero exit so Claude fails creation (a partial
// worktree is worse than none).
//
// Security: `name` and `cwd` are passed as argv (never interpolated into a
// shell). The resolver canonicalizes both cwd and each candidate repo path
// before a prefix match, so `..`- or symlink-bearing cwd cannot escape the
// workspace. `name` is only persisted as the session purpose and never enters
// a git ref (branches are prefix + random-hex), so control-char stripping
// covers the residual stored/displayed-metadata concern.
func runFromHookCreate(cmd *cobra.Command, payload hookPayload) error {
	// Resolve the instance from the launch directory and the repo from a
	// `<repo>/` prefix in the name, the untrusted cwd, a one-repo instance,
	// NIWA_WORKTREE_REPO, or the instance's first repo for a bare name.
	// The instance is anchored on CLAUDE_PROJECT_DIR, which the harness fixes,
	// so a model-chosen cwd can't point the hook at a planted instance; the
	// canonicalizing cwd match stays the gate for picking a repo from cwd. On
	// no match we return an error (non-zero exit) whose text lists the repos,
	// so Claude's creation fails with guidance rather than producing a wrong
	// worktree.
	target, err := workspace.ResolveHookTarget(workspace.HookAnchor(), payload.Cwd, payload.Name)
	if err != nil {
		return fmt.Errorf("niwa: error: %w", err)
	}
	instanceRoot, repo := target.InstanceRoot, target.Repo
	if target.Defaulted {
		// stderr only: stdout carries nothing but the worktree path.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"niwa: notice: worktree %q names no repo, so it was created in %s, this instance's first repo by group and name. "+
				"To choose another repo, name the worktree <repo>/<slug> or set %s=<repo> in the session's environment.\n",
			payload.Name, repo, workspace.WorktreeRepoEnv)
	}

	purpose := purposeFromHookName(target.Slug)

	sessionID, worktreePath, branch, err := worktree.CreateSession(context.Background(), worktree.CreateSessionParams{
		InstanceRoot: instanceRoot,
		Repo:         repo,
		Purpose:      purpose,
		GitInvoker:   worktree.StdGitInvoker{},
		// Marks the session as the hook's, which is what lets the
		// SubagentStop hook and the reap sweep remove it later.
		CreatedBy:            worktree.CreatedByWorktreeHook,
		ClaudeTranscriptPath: hookTranscriptPath(payload.TranscriptPath),
	})
	if err != nil {
		return fmt.Errorf("niwa: error: creating session for hook worktree: %w", err)
	}

	// Materialize secrets + CLAUDE context into the worktree, exactly as
	// runSessionCreate does. Without this step the delegated worktree would be
	// the degraded checkout this feature exists to eliminate. A key the vault
	// cannot supply is marked and carried through rather than failing here; a
	// hard failure still fails creation.
	// The setup outcome comes back as data and is reported on stderr below.
	// It must NOT reach stdout: the hook contract is that stdout carries only
	// the absolute worktree path, which Claude Code reads as the session's
	// working directory. And it must not become an error either -- that would
	// route into reconcileFailedHookCreate and destroy a worktree whose only
	// fault is a failing setup script.
	var setup workspace.SetupResult
	if _, err := applyContentToWorktree(instanceRoot, worktreePath, repo, purpose, branch, &setup, cmd.ErrOrStderr()); err != nil {
		// Reconcile rather than strand (design Decision 8). CreateSession is
		// already atomic — every failure after `git worktree add` cleans up after
		// itself — but content install is not, so without this the tool call fails
		// loudly while the git worktree and an `active` session record both
		// survive, and `niwa worktree list` reports an active worktree no process
		// is in. That contradicts R6's system-of-record premise, and the rows
		// accumulate for as long as the underlying cause persists.
		//
		// This deliberately diverges from runSessionCreate, which retains and
		// tells. A human is standing at the terminal, the worktree is a real place
		// they asked for, and re-syncing it is a genuine repair. An agent never
		// enters the directory on this path and will not come back to it.
		return reconcileFailedHookCreate(cmd.ErrOrStderr(), instanceRoot, sessionID, err)
	}

	// A setup failure is reported and the worktree is kept. The agent lands in
	// a tree that exists and is navigable, with the reason on stderr, rather
	// than having its worktree deleted for a script's failure.
	reportWorktreeSetup(cmd.ErrOrStderr(), worktreePath, &setup)

	// Hook contract: stdout carries ONLY the absolute worktree path. Claude
	// uses this as the session working directory.
	fmt.Fprintln(cmd.OutOrStdout(), worktreePath)
	return nil
}

// reconcileFailedHookCreate undoes a partially-created delegated worktree after
// the create path failed past `git worktree add`, and returns the error the
// hook exits with.
//
// It runs the SAME guarded teardown the WorktreeRemove path runs —
// DestroySession with force=false — so the feature has one reconciliation
// mechanism rather than two. In the normal case that is a full rollback: the
// record moves to a terminal state, the worktree is removed, and the session
// branch is deleted (it was created at HEAD with no commits, so the guarded
// delete succeeds).
//
// The dirty guard stays armed even though a failed create is almost never
// dirty. Every file niwa writes into a worktree is git-excluded, and the agent
// never enters the directory on this path, so the one way real work can be
// present is a workspace-authored worktree apply script that touched tracked
// files before a later step failed. That is exactly the case Decision 3 says to
// retain, so it is retained and logged rather than forced.
//
// The returned error always names the original cause; the reconciliation
// outcome is appended so the developer reading the failed tool call knows
// whether anything was left behind.
func reconcileFailedHookCreate(stderr io.Writer, instanceRoot, sessionID string, cause error) error {
	base := fmt.Errorf("niwa: error: installing content into worktree %s: %w", sessionID, cause)

	state, err := worktree.DestroySession(
		context.Background(), instanceRoot, sessionID, false /* force */, worktree.StdGitInvoker{},
	)
	if err != nil {
		if errors.Is(err, worktree.ErrWorktreeDirty) {
			fmt.Fprintf(stderr,
				"niwa: notice: worktree for session %s has uncommitted changes; "+
					"retaining it (not deleted). Reclaim it with `niwa worktree destroy %s --force` once reviewed.\n",
				sessionID, sessionID)
			return fmt.Errorf("%w (worktree retained: it has uncommitted changes)", base)
		}
		fmt.Fprintf(stderr,
			"niwa: warning: rolling back session %s after a failed create: %v\n", sessionID, err)
		return fmt.Errorf("%w (rollback incomplete: %v)", base, err)
	}

	// DestroySession reports a branch it declined to delete through
	// BranchWarning rather than an error, so carry it: otherwise the message
	// below asserts a clean rollback while a session branch survives.
	if state.BranchWarning != "" {
		return fmt.Errorf("%w (the partially created worktree was rolled back; %s)", base, state.BranchWarning)
	}

	return fmt.Errorf("%w (the partially created worktree was rolled back)", base)
}

// runFromHookRemove handles a WorktreeRemove hook. WorktreeRemove is
// non-blocking, and Claude's session_id is NOT niwa's session id, so the
// worktree is mapped to a niwa session BY WORKTREE PATH
// (resolveSessionIDByPath scans ListSessionLifecycleStates for the matching
// WorktreePath).
//
// WorktreeRemove stdin schema assumption: the spike did not exercise this
// event, so we accept the worktree directory from `worktree_path` and fall
// back to `cwd`. If a future Claude release names the field differently, only
// this mapping needs to change.
//
// Reconciliation follows design Decision 3 (defense-in-depth):
//  1. Release the agent's OWN attach lock (the exiting agent is its own holder,
//     so bypassing only the attach-lock guard is safe).
//  2. Attempt DestroySession(force=false) — the dirty guard stays armed.
//  3. If the destroy is rejected ONLY because the worktree is dirty
//     (ErrWorktreeDirty), do NOT force-delete: log-and-retain, leaving the
//     worktree for the developer. NEVER force past the dirty guard.
//
// This path ALWAYS returns nil (exit 0): WorktreeRemove is non-blocking, so a
// failed reconciliation is logged to stderr but never fails teardown.
func runFromHookRemove(cmd *cobra.Command, payload hookPayload) error {
	stderr := cmd.ErrOrStderr()

	// Prefer worktree_path; fall back to cwd. See the schema-assumption note
	// above.
	wantPath := payload.WorktreePath
	if wantPath == "" {
		wantPath = payload.Cwd
	}
	if wantPath == "" {
		fmt.Fprintln(stderr, "niwa: warning: WorktreeRemove hook carried neither worktree_path nor cwd; nothing to reconcile")
		return nil
	}

	// The instance comes from the launch directory and the worktree path, so
	// a remove sent from the workspace root (where the process's own working
	// directory is in no instance) still finds it. When that fails, fall back
	// to NIWA_INSTANCE_ROOT or the process's working directory, as before.
	// The fallback deliberately does not enforce the anchor refusal that create
	// does ("outside the instance this session was launched in"). That is safe
	// for remove in a way it wouldn't be for create: remove only tears down a
	// worktree that matches a session record in the instance it found, and
	// keeps any worktree with uncommitted work. Don't copy this into create.
	instanceRoot, err := workspace.ResolveHookInstance(workspace.HookAnchor(), wantPath)
	if err != nil {
		var fallbackErr error
		instanceRoot, fallbackErr = resolveInstanceRoot()
		if fallbackErr != nil {
			fmt.Fprintf(stderr, "niwa: warning: WorktreeRemove could not resolve instance root: %v (fallback: %v)\n", err, fallbackErr)
			return nil
		}
	}
	if state, stateErr := workspace.LoadState(instanceRoot); stateErr == nil && state.NoWorktreeDelegation {
		// The workspace root's hooks are shared by every instance; an
		// opted-out instance's worktrees aren't niwa's to reconcile here.
		return nil
	}

	sessionID, err := resolveSessionIDByPath(instanceRoot, wantPath)
	if err != nil {
		// Unknown path: the worktree was never a niwa session (or already
		// reconciled). Non-blocking — log and exit 0.
		fmt.Fprintf(stderr, "niwa: warning: WorktreeRemove found no niwa session for worktree %q: %v\n", wantPath, err)
		return nil
	}

	teardownHookSession(stderr, instanceRoot, sessionID, hookEventWorktreeRemove)
	return nil
}

// teardownHookSession is the guarded teardown both WorktreeRemove and
// SubagentStop run. It never forces past a guard and never fails: errors are
// logged to stderr.
//
//  1. Release the agent's own attach lock so the attach-lock guard in
//     DestroySession does not block this teardown. The exiting agent is the
//     lock holder, so bypassing only this guard is safe. force=true is NOT
//     passed, since it would also bypass the dirty guard.
//  2. Destroy without force. The dirty guard stays armed, and an unmerged
//     branch survives (`git branch -d`).
//  3. A dirty rejection means real uncommitted work: log and retain rather
//     than force-delete. The developer reclaims it with `niwa worktree
//     destroy --force`; the session record persists, so this is a surfaced
//     orphan, not a silent one.
func teardownHookSession(stderr io.Writer, instanceRoot, sessionID, event string) {
	state, readErr := worktree.ReadSessionLifecycleState(
		filepathJoinSessions(instanceRoot), sessionID)
	if readErr == nil && state.WorktreePath != "" {
		if rmErr := worktree.RemoveAttachState(state.WorktreePath); rmErr != nil {
			fmt.Fprintf(stderr, "niwa: warning: releasing attach lock for session %s: %v\n", sessionID, rmErr)
		}
	}

	_, err := worktree.DestroySession(context.Background(), instanceRoot, sessionID, false /* force */, worktree.StdGitInvoker{})
	if err == nil {
		return
	}
	if errors.Is(err, worktree.ErrWorktreeDirty) {
		fmt.Fprintf(stderr,
			"niwa: notice: worktree for session %s has uncommitted changes; "+
				"retaining it (not deleted). Reclaim it with `niwa worktree destroy %s --force` once reviewed.\n",
			sessionID, sessionID)
		return
	}
	fmt.Fprintf(stderr, "niwa: warning: reconciling session %s on %s: %v\n", sessionID, event, err)
}

// runFromHookSubagentStop removes the worktree an Agent-tool subagent ran in,
// once it finishes, when the subagent left it unchanged. Claude Code doesn't
// send WorktreeRemove for a worktree a WorktreeCreate hook made when that
// worktree is a git checkout, so without this every isolated subagent leaves
// its niwa worktree behind.
//
// It acts on one session only: the active session this hook created whose
// purpose is `agent-<agent_id>` (the name Claude Code gave the worktree) and
// whose worktree contains the payload's cwd. Matching on the id rather than
// on cwd alone matters, because a subagent without isolation reports its
// parent's directory, which can itself be a hook-made worktree the parent
// session is still working in. The worktree must also be unchanged
// (worktree.WorktreeUnchanged); a changed one is kept and said so on stderr.
//
// SubagentStop must never send the subagent back to work, so this returns nil
// on every path: exit 2 is the code that would.
func runFromHookSubagentStop(cmd *cobra.Command, payload hookPayload) error {
	stderr := cmd.ErrOrStderr()
	if payload.AgentID == "" || payload.Cwd == "" {
		return nil
	}
	instanceRoot, err := workspace.ResolveHookInstance(workspace.HookAnchor(), payload.Cwd)
	if err != nil {
		// Not in a niwa instance: no worktree of ours to look at.
		return nil
	}
	if state, stateErr := workspace.LoadState(instanceRoot); stateErr == nil && state.NoWorktreeDelegation {
		return nil
	}

	state, ok := findAgentWorktreeSession(instanceRoot, payload.Cwd, "agent-"+payload.AgentID)
	if !ok {
		return nil
	}
	unchanged, reason, err := worktree.WorktreeUnchanged(context.Background(), state, worktree.StdGitInvoker{})
	if err != nil {
		fmt.Fprintf(stderr, "niwa: warning: keeping worktree %s: could not check it for changes: %v\n", state.WorktreePath, err)
		return nil
	}
	if !unchanged {
		fmt.Fprintf(stderr, "niwa: notice: keeping worktree %s because %s. Remove it with `niwa worktree destroy %s` when it's no longer needed.\n",
			state.WorktreePath, reason, state.SessionID)
		return nil
	}
	teardownHookSession(stderr, instanceRoot, state.SessionID, hookEventSubagentStop)
	return nil
}

// findAgentWorktreeSession returns the active, hook-made session with the
// given purpose whose worktree contains cwd.
func findAgentWorktreeSession(instanceRoot, cwd, purpose string) (worktree.SessionLifecycleState, bool) {
	states, err := worktree.ListSessionLifecycleStates(filepathJoinSessions(instanceRoot))
	if err != nil {
		return worktree.SessionLifecycleState{}, false
	}
	canonCwd := canonicalizeWorktreePath(cwd)
	for _, st := range states {
		if st.Status != worktree.SessionStatusActive || st.CreatedBy != worktree.CreatedByWorktreeHook ||
			st.Purpose != purpose || st.WorktreePath == "" {
			continue
		}
		wt := canonicalizeWorktreePath(st.WorktreePath)
		if canonCwd == wt || strings.HasPrefix(canonCwd, wt+string(filepath.Separator)) {
			return st, true
		}
	}
	return worktree.SessionLifecycleState{}, false
}

// hookTranscriptPath keeps the payload's transcript path only when it is an
// absolute, clean path. The reap sweep only ever stats it, but a value that
// isn't a plain absolute path is not one Claude Code wrote.
func hookTranscriptPath(p string) string {
	if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return ""
	}
	return p
}

// purposeFromHookName derives a session purpose from Claude's untrusted
// worktree `name`. Control characters are STRIPPED (security: the name is
// untrusted and is persisted/displayed as session metadata; this prevents
// control-character injection into stored/displayed values). When the name is
// empty or strips to nothing, the generic default purpose is used so
// CreateSession's non-empty-purpose precondition still holds.
func purposeFromHookName(name string) string {
	sanitized := stripControlChars(name)
	sanitized = strings.TrimSpace(sanitized)
	if sanitized == "" {
		return defaultSessionPurpose
	}
	return sanitized
}

// stripControlChars removes ASCII and Unicode control characters from s,
// keeping printable runes (including normal whitespace such as spaces, which
// are not control characters). It is the security sanitizer for the untrusted
// hook `name`.
func stripControlChars(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		// Drop C0/C1 control characters and DEL. Tab/newline/CR are control
		// characters too and are intentionally dropped: a purpose is a single
		// freeform line of metadata.
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			continue
		}
		// Drop the line/paragraph separators and the bidi and zero-width
		// formatting characters. These are not control characters by
		// unicode.IsControl, so a Cc-only filter passes them through, and they
		// are exactly what reorders or hides part of a rendered line. It
		// matters because values from the session mapping store and from
		// worktree lifecycle records reach a terminal here, including the
		// kept-branch warning, which carries a record-supplied branch name into
		// a copy-pasteable `git branch -D` line.
		if r == '\u2028' || r == '\u2029' ||
			(r >= '\u200b' && r <= '\u200f') ||
			(r >= '\u202a' && r <= '\u202e') ||
			(r >= '\u2066' && r <= '\u2069') ||
			r == '\ufeff' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// filepathJoinSessions returns the per-instance sessions directory. It is a
// tiny helper mirroring the literal used across the lifecycle commands
// (filepath.Join(instanceRoot, ".niwa", "sessions")) so the from-hook remove
// path reads the same location ReadSessionLifecycleState expects.
func filepathJoinSessions(instanceRoot string) string {
	return filepath.Join(instanceRoot, ".niwa", "sessions")
}
