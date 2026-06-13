package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
	"github.com/tsukumogami/niwa/internal/worktree"
)

func init() {
	sessionCmd.AddCommand(sessionCreateCmd)
	sessionCmd.AddCommand(sessionApplyCmd)
	sessionCmd.AddCommand(sessionDestroyCmd)
}

var sessionApplyCmd = &cobra.Command{
	Use:   "apply <session-id>",
	Short: "Re-sync an existing worktree's CLAUDE content",
	Long: `Re-sync an existing worktree's CLAUDE content.

The worktree analog of ` + "`niwa apply`" + `: resolves an existing worktree from
its session lifecycle state and re-installs the owning repo's CLAUDE content
(plus the worktree rules import and the purpose/branch layer) idempotently.
Unlike create, it does not scaffold a new worktree; the session must already
exist and be active.`,
	// Same reasoning as sessionCreateCmd/sessionDestroyCmd: RunE validates the
	// arg count itself and returns an *sessionattach.ExitCodeError with Code=2.
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeSessionIDs,
	RunE:              runSessionApply,
}

var sessionCreateCmd = &cobra.Command{
	Use:   "create <repo> <purpose>",
	Short: "Create a new git worktree for a repo",
	Long: `Create a new git worktree for a repo.

Scaffolds a git worktree under .niwa/worktrees/<repo>-<session-id>/ and
writes the worktree lifecycle state.

On success the shell wrapper navigates to the new worktree directory.`,
	// We don't use cobra.ExactArgs because its default error exits 1 with a
	// generic "accepts 2 arg(s), received 0" message. RunE validates arg count
	// itself and returns an *sessionattach.ExitCodeError with Code=2 plus a
	// usage string naming <repo> and <purpose>.
	Args:              cobra.MaximumNArgs(2),
	ValidArgsFunction: completeSessionCreateArgs,
	RunE:              runSessionCreate,
}

var sessionDestroyCmd = &cobra.Command{
	Use:   "destroy <session-id>",
	Short: "Destroy a worktree and its working directory",
	Long: `Destroy a worktree: mark its lifecycle state ended, remove the working
directory, and delete the worktree branch (only if already merged; use
--force to delete regardless).

Refuses to destroy a worktree that holds uncommitted changes unless --force
is passed (the worktree analog of the instance-level uncommitted-work guard).`,
	// Same reasoning as sessionCreateCmd: RunE handles missing-arg with a
	// usage string and exit code 2 via *sessionattach.ExitCodeError.
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeSessionIDs,
	RunE:              runSessionDestroy,
}

// completeSessionCreateArgs returns repo completions for the first
// positional argument and suppresses filename completion for the second
// (<purpose> is freeform text).
//
// The switch shape is intentional: each case represents a positional slot,
// so adding a future positional means adding a case rather than rewriting
// the dispatcher. The single-arg completers in this file use a plain
// `if len(args) > 0` guard instead because they only have one slot.
func completeSessionCreateArgs(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return completeRepoNames(cmd, args, toComplete)
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

var sessionDestroyForce bool

func init() {
	sessionDestroyCmd.Flags().BoolVar(&sessionDestroyForce, "force", false, "Destroy even with uncommitted changes, and delete the branch regardless of merge status")
}

func runSessionCreate(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return &sessionattach.ExitCodeError{
			Code: 2,
			Msg: "niwa: usage: niwa worktree create <repo> <purpose>. " +
				"Run `niwa worktree create --help` for details.",
		}
	}
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return err
	}
	repo := args[0]
	purpose := args[1]

	sessionID, worktreePath, branch, err := worktree.CreateSession(context.Background(), worktree.CreateSessionParams{
		InstanceRoot: instanceRoot,
		Repo:         repo,
		Purpose:      purpose,
		GitInvoker:   worktree.StdGitInvoker{},
	})
	if err != nil {
		if errors.Is(err, worktree.ErrSessionUnknownRole) {
			return fmt.Errorf("niwa: error: %v", err)
		}
		return fmt.Errorf("niwa: error: creating session: %w", err)
	}

	// Install the owning repo's CLAUDE content (and the worktree-context
	// rules import + purpose/branch layer) into the new worktree, so a
	// worktree ends up with the same class of accessories a repo checkout
	// gets from `niwa apply`. The worktree already exists at this point; an
	// install failure is surfaced but does not unwind the worktree (it can
	// be re-synced later).
	written, err := applyContentToWorktree(instanceRoot, worktreePath, repo, purpose, branch)
	if err != nil {
		return fmt.Errorf("niwa: error: installing content into worktree %s (the worktree exists; re-sync it later): %w", sessionID, err)
	}

	// Issue 10: success summary on stdout so callers can pipe it.
	// Landing-path delivery uses NIWA_RESPONSE_FILE separately; the
	// shell wrapper's stdout-cd target is unaffected.
	fmt.Fprintf(cmd.OutOrStdout(), "session: created %s at %s\n", sessionID, worktreePath)
	printWorktreeContentFiles(cmd, written)

	if err := validateLandingPath(worktreePath); err != nil {
		return err
	}
	if err := writeLandingPath(worktreePath); err != nil {
		return err
	}
	hintShellInit(cmd)
	return nil
}

// runSessionApply re-syncs an existing worktree's CLAUDE content. It resolves
// the worktree path / repo / purpose / branch from the session's lifecycle
// state (NO CreateSession), then runs the same shared content helper create
// uses against the existing worktree. It is idempotent by construction
// (workspace.ApplyToWorktree re-points the rules import and replaces the
// worktree-context section rather than appending duplicates).
func runSessionApply(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return &sessionattach.ExitCodeError{
			Code: 2,
			Msg: "niwa: usage: niwa worktree apply <session-id>. " +
				"Run `niwa worktree list` to discover existing worktrees.",
		}
	}
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return err
	}
	sessionID := args[0]

	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	state, err := worktree.ReadSessionLifecycleState(sessionsDir, sessionID)
	if err != nil {
		return fmt.Errorf("niwa: error: resolving session %s: %w", sessionID, err)
	}
	if state.Status == worktree.SessionStatusEnded || state.Status == worktree.SessionStatusAbandoned {
		return &sessionattach.ExitCodeError{
			Code: 1,
			Msg: fmt.Sprintf("niwa: error: session %s is %s; cannot re-sync a terminal worktree",
				sessionID, state.Status),
		}
	}
	if state.WorktreePath == "" {
		return &sessionattach.ExitCodeError{
			Code: 1,
			Msg:  fmt.Sprintf("niwa: error: session %s has no recorded worktree path", sessionID),
		}
	}

	written, err := applyContentToWorktree(instanceRoot, state.WorktreePath, state.Repo, state.Purpose, state.EffectiveBranchName())
	if err != nil {
		return fmt.Errorf("niwa: error: re-syncing content into worktree %s: %w", sessionID, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "session: applied %s at %s\n", sessionID, state.WorktreePath)
	printWorktreeContentFiles(cmd, written)
	return nil
}

// applyContentToWorktree loads the workspace config the way create/apply do
// (walk up from the instance root to find .niwa/workspace.toml), resolves the
// repo's group from the on-disk instance layout, and installs the repo's CLAUDE
// content into the worktree via workspace.ApplyToWorktree. It mirrors the
// init.go/RunBootstrap composition: the leaf internal/worktree stays a leaf
// (the content install lives in internal/workspace), and the CLI orchestrates
// the two.
func applyContentToWorktree(instanceRoot, worktreePath, repo, purpose, branch string) ([]string, error) {
	configPath, configDir, err := config.Discover(instanceRoot)
	if err != nil {
		return nil, fmt.Errorf("locating workspace config: %w", err)
	}
	result, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading workspace config: %w", err)
	}
	cfg := result.Config

	group, err := workspace.FindRepoGroup(instanceRoot, repo)
	if err != nil {
		return nil, err
	}

	opts := workspace.WorktreeApplyOptions{Stderr: os.Stderr}

	// Thread the resolved personal/global .env.example failure policy so the
	// worktree pre-pass applies the same policy as `niwa apply`. The global
	// override snapshot was already synced by apply/create; here we read the
	// already-present niwa.toml without re-syncing. Any unavailability (no
	// global config registered, no niwa.toml, or a parse error) leaves the
	// policy nil, which the resolver treats as "no global rung".
	opts.GlobalEnvExamplePolicy = resolveGlobalEnvExamplePolicy(cfg.Workspace.Name)

	// Thread the resolved personal/global secret-output target declaration so
	// the worktree materializer resolves the same targets as `niwa apply`. Same
	// no-fail posture as the policy above: any unavailability leaves it empty.
	opts.GlobalEnvOutput = resolveGlobalEnvOutput(cfg.Workspace.Name)

	// Resolve and merge the workspace overlay the same way `niwa apply` does, so
	// a worktree of an overlay-augmented repo gets the overlay-merged CLAUDE
	// content a repo checkout would. config.Load does NOT run the overlay merge,
	// so the loaded cfg has no OverlaySource set; without this step an
	// overlay-augmented repo would silently miss its overlay content. The
	// overlay dir is recorded in InstanceState.OverlayURL by apply/create.
	// When no overlay is configured (empty OverlayURL or NoOverlay), opts.OverlayDir
	// stays empty and the no-overlay path runs exactly as before.
	if cfgWithOverlay, overlayDir, oErr := mergeWorktreeOverlay(cfg, instanceRoot); oErr != nil {
		return nil, oErr
	} else if overlayDir != "" {
		cfg = cfgWithOverlay
		opts.OverlayDir = overlayDir
	}

	return workspace.ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, group, repo, purpose, branch, opts)
}

// resolveGlobalEnvExamplePolicy reads the already-synced personal global config
// override (niwa.toml under the registered global config dir) and returns the
// resolved .env.example failure policy for workspaceName. It does NOT sync the
// snapshot (apply/create already did) and never fails the worktree apply: any
// unavailability (no global config registered, no niwa.toml, or a parse error)
// returns nil, which EffectiveEnvExamplePolicy treats as "no global rung".
func resolveGlobalEnvExamplePolicy(workspaceName string) *config.EnvExamplePolicy {
	gDir, err := config.GlobalConfigDir()
	if err != nil || gDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(gDir, workspace.GlobalConfigOverrideFile))
	if err != nil {
		return nil
	}
	parsed, err := config.ParseGlobalConfigOverride(data)
	if err != nil {
		return nil
	}
	return workspace.ResolveGlobalOverride(parsed, workspaceName).EnvExamplePolicy
}

// resolveGlobalEnvOutput reads the already-synced personal global config
// override and returns the resolved secret-output target declaration for
// workspaceName. Like resolveGlobalEnvExamplePolicy it never fails the worktree
// apply: any unavailability returns nil, which EffectiveEnvOutput treats as "no
// global rung".
func resolveGlobalEnvOutput(workspaceName string) config.OutputTargets {
	gDir, err := config.GlobalConfigDir()
	if err != nil || gDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(gDir, workspace.GlobalConfigOverrideFile))
	if err != nil {
		return nil
	}
	parsed, err := config.ParseGlobalConfigOverride(data)
	if err != nil {
		return nil
	}
	return workspace.ResolveGlobalOverride(parsed, workspaceName).EnvOutput
}

// printWorktreeContentFiles surfaces the written-files list returned by
// applyContentToWorktree on stdout, so create and apply both report the
// content they installed/updated. A nil/empty list prints nothing.
func printWorktreeContentFiles(cmd *cobra.Command, written []string) {
	for _, f := range written {
		fmt.Fprintf(cmd.OutOrStdout(), "session: content %s\n", f)
	}
}

// mergeWorktreeOverlay resolves the active workspace overlay (recorded in
// InstanceState.OverlayURL) and merges its config into cfg, mirroring the
// overlay merge `niwa apply` runs. It returns the merged config and the
// resolved overlay clone directory. When no overlay is active (no recorded
// URL, NoOverlay set, or the clone is absent), it returns the original cfg and
// an empty overlay dir so the caller takes the no-overlay path.
//
// This runs only the structural config merge (which populates OverlaySource on
// content entries); it deliberately does not re-run the overlay's vault
// resolution, which the apply pipeline needs for env/secrets but the worktree
// content install does not.
func mergeWorktreeOverlay(cfg *config.WorkspaceConfig, instanceRoot string) (*config.WorkspaceConfig, string, error) {
	state, err := workspace.LoadState(instanceRoot)
	if err != nil {
		// No readable state means no recorded overlay; fall back to the
		// no-overlay path rather than failing the create.
		return cfg, "", nil
	}
	if state.NoOverlay || state.OverlayURL == "" {
		return cfg, "", nil
	}

	overlayDir, err := config.OverlayDir(state.OverlayURL)
	if err != nil {
		return nil, "", fmt.Errorf("resolving overlay directory: %w", err)
	}
	overlayTOML := filepath.Join(overlayDir, "workspace-overlay.toml")
	overlay, err := config.ParseOverlay(overlayTOML)
	if err != nil {
		if os.IsNotExist(err) {
			// The overlay clone is missing locally (e.g. never synced on this
			// machine). Fall back to base content rather than hard-failing.
			return cfg, "", nil
		}
		return nil, "", fmt.Errorf("parsing workspace overlay: %w", err)
	}

	merged, err := workspace.MergeWorkspaceOverlay(cfg, overlay, overlayDir)
	if err != nil {
		return nil, "", fmt.Errorf("merging workspace overlay: %w", err)
	}
	return merged, overlayDir, nil
}

func runSessionDestroy(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return &sessionattach.ExitCodeError{
			Code: 2,
			Msg: "niwa: usage: niwa worktree destroy <session-id> [--force]. " +
				"Run `niwa worktree list` to discover existing worktrees.",
		}
	}
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return err
	}
	sessionID := args[0]

	state, err := worktree.DestroySession(context.Background(), instanceRoot, sessionID, sessionDestroyForce, worktree.StdGitInvoker{})
	if err != nil {
		// A live attach holds the worktree and --force was not passed: surface
		// the guard message verbatim (it carries the holder PID and recovery
		// command) rather than burying it under a generic destroy prefix.
		if errors.Is(err, worktree.ErrSessionAttached) {
			return &sessionattach.ExitCodeError{Code: 1, Msg: "niwa: error: " + err.Error()}
		}
		// The worktree holds uncommitted work and --force was not passed:
		// surface the actionable guard message verbatim (it names the worktree
		// path and the commit/stash/--force recovery options) rather than
		// burying it under a generic destroy prefix.
		if errors.Is(err, worktree.ErrWorktreeDirty) {
			return &sessionattach.ExitCodeError{Code: 1, Msg: "niwa: error: " + err.Error()}
		}
		return fmt.Errorf("niwa: error: destroying session %s: %w", sessionID, err)
	}
	if state.BranchWarning != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning:", state.BranchWarning)
	}

	// Issue 10: destroy success on stdout, matching create.
	fmt.Fprintf(cmd.OutOrStdout(), "session: destroyed %s\n", sessionID)
	return nil
}

// runSessionLifecycleList lists per-session lifecycle states, filtering by
// repo, status, and attach availability. Called by sessionListCmd when at
// least one filter flag is present.
//
// Each row's AVAILABILITY value is projected from the per-worktree
// attach.state sentinel via worktree.ReadAttachState (with reapStale=true so
// the listing pass naturally cleans up dead-holder sentinels).
//
// Sort order matches PRD R17: attached first (the operator's hot question
// "is anyone in there?"), then active before terminal status, then
// creation_time descending.
func runSessionLifecycleList(cmd *cobra.Command, repo, status string, onlyAttached, onlyAvailable bool) error {
	if onlyAttached && onlyAvailable {
		return fmt.Errorf("--attached and --available are mutually exclusive")
	}
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return err
	}

	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	all, err := worktree.ListSessionLifecycleStates(sessionsDir)
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}

	rows := make([]lifecycleRow, 0, len(all))
	for _, st := range all {
		if repo != "" && st.Repo != repo {
			continue
		}
		if status != "" && st.Status != status {
			continue
		}
		attachState, avail, _ := worktree.ReadAttachState(st.WorktreePath, true /* reap dead-holder sentinels */)
		if onlyAttached && avail != worktree.AttachAttached {
			continue
		}
		if onlyAvailable && avail != worktree.AttachAvailable {
			continue
		}
		// Project the live attach state onto the embedded
		// SessionLifecycleState so the CLI JSON wire shape carries the
		// `attach` key (the full AttachState struct, absent when no live
		// lock is held).
		if avail == worktree.AttachAttached && attachState != nil {
			st.Attach = attachState
		}
		rows = append(rows, lifecycleRow{
			state: st,
			avail: avail,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rowSortLess(rows[i], rows[j])
	})

	if sessionListJSON {
		// JSON mode: emit a fresh array (not null) when empty. The wire
		// shape carries the full AttachState struct via the embedded
		// SessionLifecycleState's `attach` key (absent when no live lock).
		// The `availability` key is a CLI-specific projection.
		out := cmd.OutOrStdout()
		jsonRows := make([]sessionListJSONRow, 0, len(rows))
		for _, r := range rows {
			jsonRows = append(jsonRows, sessionListJSONRow{
				SessionLifecycleState: r.state,
				Availability:          availabilityForTable(r.avail),
			})
		}
		if len(jsonRows) == 0 {
			fmt.Fprintln(out, "[]")
			return nil
		}
		data, err := json.MarshalIndent(jsonRows, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal sessions: %w", err)
		}
		_, _ = out.Write(data)
		fmt.Fprintln(out)
		return nil
	}

	writeSessionLifecycleTable(cmd.OutOrStdout(), rows)
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no sessions match the current filter)")
	}
	return nil
}

// rowSortLess implements PRD R17's composite sort:
//  1. attached first (the operator's hot question: "is anyone in there?")
//  2. status: active before terminal
//  3. creation_time descending (newest first)
func rowSortLess(a, b lifecycleRow) bool {
	// Key 1: attached < others.
	aAttached := a.avail == worktree.AttachAttached
	bAttached := b.avail == worktree.AttachAttached
	if aAttached != bAttached {
		return aAttached // true sorts first
	}
	// Key 2: active < ended < abandoned.
	if a.state.Status != b.state.Status {
		return statusRank(a.state.Status) < statusRank(b.state.Status)
	}
	// Key 3: creation_time descending (newer first).
	return a.state.CreationTime > b.state.CreationTime
}

func statusRank(s string) int {
	switch s {
	case worktree.SessionStatusActive:
		return 0
	case worktree.SessionStatusEnded:
		return 1
	case worktree.SessionStatusAbandoned:
		return 2
	default:
		return 3
	}
}

// availabilityForTable reduces an AttachAvailability to one of the three
// values rendered in the AVAILABILITY column.
func availabilityForTable(a worktree.AttachAvailability) string {
	switch a {
	case worktree.AttachAttached:
		return string(worktree.AttachAttached)
	case worktree.AttachStale:
		return string(worktree.AttachStale)
	default:
		return string(worktree.AttachAvailable)
	}
}

// lifecycleRow bundles a persisted SessionLifecycleState with the computed
// attach-availability projection (from .niwa/attach.state) needed at
// table-render time. The projection is not persisted; it is read on every
// list.
type lifecycleRow struct {
	state worktree.SessionLifecycleState
	avail worktree.AttachAvailability
}

// sessionListJSONRow is the wire shape returned by
// `niwa session list --json`. The `attach` key (via the embedded
// SessionLifecycleState.Attach pointer field) carries the full AttachState
// struct when a live lock is held, absent otherwise. The `availability` key
// is a CLI-side projection that lets callers distinguish `stale` (sentinel
// present but reaped) from `available` (no sentinel) without having to walk
// PIDs themselves.
type sessionListJSONRow struct {
	worktree.SessionLifecycleState
	Availability string `json:"availability"`
}

func writeSessionLifecycleTable(out interface{ Write([]byte) (int, error) }, rows []lifecycleRow) {
	fmt.Fprintf(out, "  %-12s %-12s %-10s %-12s %-20s %s\n",
		"SESSION_ID", "REPO", "STATUS", "AVAILABILITY", "CREATED", "PURPOSE")
	for _, r := range rows {
		s := r.state
		created := "-"
		if s.CreationTime != "" {
			if t, err := time.Parse(time.RFC3339, s.CreationTime); err == nil {
				created = formatRelativeTime(t)
			}
		}
		purpose := s.Purpose
		if len(purpose) > 40 {
			purpose = purpose[:37] + "..."
		}
		availability := availabilityForTable(r.avail)
		fmt.Fprintf(out, "  %-12s %-12s %-10s %-12s %-20s %s\n",
			s.SessionID, s.Repo, s.Status, availability, created, purpose)
	}
}
