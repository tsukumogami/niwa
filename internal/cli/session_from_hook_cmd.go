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
// per-repo WorktreeCreate/WorktreeRemove hook command (an absolute-path
// `niwa worktree from-hook`, with NO shim script). It reads the Claude hook
// JSON on stdin and dispatches on hook_event_name to the same create/destroy
// core the human `niwa worktree create`/`destroy` commands use.
//
// The hook I/O contract is the only thing this command owns:
//   - WorktreeCreate: print ONLY the absolute worktree path to stdout, exit 0
//     on success; exit non-zero on any error (Claude then fails creation,
//     which is correct: a partial/wrong worktree is worse than none).
//   - WorktreeRemove: non-blocking, ALWAYS exit 0 (errors are logged to
//     stderr but never fail teardown).
var sessionFromHookCmd = &cobra.Command{
	Use:    "from-hook",
	Short:  "Internal: dispatch a Claude Code worktree hook (reads JSON on stdin)",
	Hidden: true,
	Long: `Internal entry point invoked directly by Claude Code's per-repo
WorktreeCreate/WorktreeRemove hooks.

Reads the Claude hook JSON payload on stdin and dispatches on
hook_event_name. Not intended for direct human use; the hook command is an
absolute-path "niwa worktree from-hook" written into a repo's
settings.local.json by niwa apply.`,
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
type hookPayload struct {
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	Cwd           string `json:"cwd"`
	Name          string `json:"name"`
	WorktreePath  string `json:"worktree_path"`
}

const (
	hookEventWorktreeCreate = "WorktreeCreate"
	hookEventWorktreeRemove = "WorktreeRemove"
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
	// Resolve both the instance root and the owning repo from the untrusted
	// cwd. ResolveRepoFromCwd discovers the instance, then applies the
	// canonicalizing longest-prefix match; a cwd outside every workspace repo
	// is rejected here. On no match we return an error (non-zero exit), so
	// Claude's creation fails rather than producing a wrong worktree.
	instanceRoot, repo, err := workspace.ResolveRepoFromCwd(payload.Cwd)
	if err != nil {
		return fmt.Errorf("niwa: error: resolving repo from hook cwd %q: %w", payload.Cwd, err)
	}

	purpose := purposeFromHookName(payload.Name)

	sessionID, worktreePath, branch, err := worktree.CreateSession(context.Background(), worktree.CreateSessionParams{
		InstanceRoot: instanceRoot,
		Repo:         repo,
		Purpose:      purpose,
		GitInvoker:   worktree.StdGitInvoker{},
	})
	if err != nil {
		return fmt.Errorf("niwa: error: creating session for hook worktree: %w", err)
	}

	// Materialize secrets + CLAUDE context into the worktree, exactly as
	// runSessionCreate does. Without this step the delegated worktree would be
	// the degraded checkout this feature exists to eliminate. R10's missing-
	// secret warnings surface on stderr inside this helper (AllowMissingSecrets
	// warn-and-continue); a hard failure still fails creation.
	if _, err := applyContentToWorktree(instanceRoot, worktreePath, repo, purpose, branch); err != nil {
		return fmt.Errorf("niwa: error: installing content into worktree %s: %w", sessionID, err)
	}

	// Hook contract: stdout carries ONLY the absolute worktree path. Claude
	// uses this as the session working directory.
	fmt.Fprintln(cmd.OutOrStdout(), worktreePath)
	return nil
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

	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		fmt.Fprintf(stderr, "niwa: warning: WorktreeRemove could not resolve instance root: %v\n", err)
		return nil
	}

	sessionID, err := resolveSessionIDByPath(instanceRoot, wantPath)
	if err != nil {
		// Unknown path: the worktree was never a niwa session (or already
		// reconciled). Non-blocking — log and exit 0.
		fmt.Fprintf(stderr, "niwa: warning: WorktreeRemove found no niwa session for worktree %q: %v\n", wantPath, err)
		return nil
	}

	// Step 1: release the agent's own attach lock so the attach-lock guard in
	// DestroySession does not block this teardown. The exiting agent is the
	// lock holder, so bypassing only this guard is safe. We do NOT pass
	// force=true (which would also bypass the dirty guard).
	state, readErr := worktree.ReadSessionLifecycleState(
		filepathJoinSessions(instanceRoot), sessionID)
	if readErr == nil && state.WorktreePath != "" {
		if rmErr := worktree.RemoveAttachState(state.WorktreePath); rmErr != nil {
			fmt.Fprintf(stderr, "niwa: warning: releasing attach lock for session %s: %v\n", sessionID, rmErr)
		}
	}

	// Step 2: guarded (non-force) destroy. The dirty guard stays armed.
	_, err = worktree.DestroySession(context.Background(), instanceRoot, sessionID, false /* force */, worktree.StdGitInvoker{})
	if err != nil {
		// Step 3: a genuine dirty rejection means real uncommitted work.
		// Log-and-retain rather than force-deleting — never silently discard
		// the developer's work. They reclaim it with `niwa worktree destroy
		// --force`. The session record persists, so this is a SURFACED orphan,
		// not a silent one.
		if errors.Is(err, worktree.ErrWorktreeDirty) {
			fmt.Fprintf(stderr,
				"niwa: notice: worktree for session %s has uncommitted changes; "+
					"retaining it (not deleted). Reclaim it with `niwa worktree destroy %s --force` once reviewed.\n",
				sessionID, sessionID)
			return nil
		}
		// Any other destroy error is logged but still non-blocking.
		fmt.Fprintf(stderr, "niwa: warning: reconciling session %s on WorktreeRemove: %v\n", sessionID, err)
		return nil
	}

	return nil
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
