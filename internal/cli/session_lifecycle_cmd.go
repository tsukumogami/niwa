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
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/resolve"
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
	Use:   "create [repo] [purpose]",
	Short: "Create a new git worktree for a repo",
	Long: `Create a new git worktree for a repo.

Scaffolds a git worktree under .niwa/worktrees/<repo>-<session-id>/ and
writes the worktree lifecycle state.

Both arguments are optional. When <repo> is omitted, it is inferred from the
process working directory (the repo whose checkout contains the cwd). When
<purpose> is omitted, a generic "session" purpose is used.

On success the shell wrapper navigates to the new worktree directory. Pass
--json to print a stable JSON object with the worktree path and session id
instead of the human-readable summary.`,
	// We don't use cobra.ExactArgs because its default error exits 1 with a
	// generic message. RunE validates arg count itself and returns an
	// *sessionattach.ExitCodeError with Code=2 on overflow. Both positionals
	// are optional: a bare `niwa worktree create` infers the repo from cwd.
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

Identify the worktree either by <session-id> or by --by-path <path>, which
resolves a worktree directory to its owning session before destroying it.

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

var (
	sessionDestroyForce  bool
	sessionDestroyByPath string
	sessionCreateJSON    bool
)

// defaultSessionPurpose is used when `niwa worktree create` is run without an
// explicit <purpose>. It mirrors the create-flow's existing freeform purpose
// field; a generic value keeps a bare invocation working without prompting.
const defaultSessionPurpose = "session"

func init() {
	sessionDestroyCmd.Flags().BoolVar(&sessionDestroyForce, "force", false, "Destroy even with uncommitted changes, and delete the branch regardless of merge status")
	sessionDestroyCmd.Flags().StringVar(&sessionDestroyByPath, "by-path", "", "Resolve the worktree at this path to its session and destroy it (instead of passing a session id)")
	sessionCreateCmd.Flags().BoolVar(&sessionCreateJSON, "json", false, "Print a JSON object with the worktree path and session id instead of the human-readable summary")
}

// sessionCreateJSONOutput is the stable wire shape emitted by
// `niwa worktree create --json`. It is deliberately minimal and additive: the
// absolute worktree path and the niwa session id are the two fields the hook
// integration (and any scripting caller) depends on. Additional fields may be
// appended later without breaking consumers that read these two keys.
type sessionCreateJSONOutput struct {
	SessionID    string `json:"session_id"`
	WorktreePath string `json:"worktree_path"`
	Repo         string `json:"repo"`
	Purpose      string `json:"purpose"`
	Branch       string `json:"branch"`
}

func runSessionCreate(cmd *cobra.Command, args []string) error {
	if len(args) > 2 {
		return &sessionattach.ExitCodeError{
			Code: 2,
			Msg: "niwa: usage: niwa worktree create [repo] [purpose]. " +
				"Run `niwa worktree create --help` for details.",
		}
	}
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return err
	}

	// <repo> is optional: when omitted, infer it from the process working
	// directory (the repo whose checkout contains cwd). The resolver is the
	// security boundary — a cwd outside every workspace repo is rejected, so a
	// bare create cannot fabricate an arbitrary repo name.
	var repo string
	if len(args) >= 1 {
		repo = args[0]
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("niwa: error: getting working directory: %w", err)
		}
		repo, err = workspace.ResolveRepoNameFromCwd(instanceRoot, cwd)
		if err != nil {
			return &sessionattach.ExitCodeError{
				Code: 2,
				Msg: fmt.Sprintf("niwa: error: could not infer repo from working directory: %v. "+
					"Pass the repo explicitly: niwa worktree create <repo> [purpose].", err),
			}
		}
	}

	// <purpose> is optional: a bare create uses a generic purpose so the flow
	// (which carries purpose into the worktree-context layer) still has a value.
	purpose := defaultSessionPurpose
	if len(args) >= 2 {
		purpose = args[1]
	}

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

	// --json mode emits a single stable object and suppresses the human
	// summary / content-file lines. The landing-path side effects below still
	// run so the shell wrapper's cd target works identically in both modes.
	if sessionCreateJSON {
		out := sessionCreateJSONOutput{
			SessionID:    sessionID,
			WorktreePath: worktreePath,
			Repo:         repo,
			Purpose:      purpose,
			Branch:       branch,
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return fmt.Errorf("niwa: error: marshaling create output: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
	} else {
		// Issue 10: success summary on stdout so callers can pipe it.
		// Landing-path delivery uses NIWA_RESPONSE_FILE separately; the
		// shell wrapper's stdout-cd target is unaffected.
		fmt.Fprintf(cmd.OutOrStdout(), "session: created %s at %s\n", sessionID, worktreePath)
		printWorktreeContentFiles(cmd, written)
	}

	if err := validateLandingPath(worktreePath); err != nil {
		return err
	}
	if err := writeLandingPath(worktreePath); err != nil {
		return err
	}
	if !sessionCreateJSON {
		hintShellInit(cmd)
	}
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
	ctx := context.Background()

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

	// Resolve and merge the workspace overlay the same way `niwa apply` does, so
	// a worktree of an overlay-augmented repo gets the overlay-merged CLAUDE
	// content a repo checkout would. config.Load does NOT run the overlay merge,
	// so the loaded cfg has no OverlaySource set; without this step an
	// overlay-augmented repo would silently miss its overlay content. The
	// overlay dir is recorded in InstanceState.OverlayURL by apply/create.
	// When no overlay is configured (empty OverlayURL or NoOverlay), opts.OverlayDir
	// stays empty and the no-overlay path runs exactly as before.
	//
	// The structural overlay merge runs BEFORE the resolve+merge helper below
	// so the helper sees the overlay-merged base (mirroring the instance
	// pipeline's Step 0.6 → Step 6 ordering); the helper's personal-overlay
	// merge then layers on top of that base.
	if cfgWithOverlay, overlayDir, oErr := mergeWorktreeOverlay(cfg, instanceRoot); oErr != nil {
		return nil, oErr
	} else if overlayDir != "" {
		cfg = cfgWithOverlay
		opts.OverlayDir = overlayDir
	}

	// Run the shared resolve+merge helper so the worktree apply path receives
	// the same effective WorkspaceConfig the instance apply path does. Without
	// this step, env.secrets vault:// URIs stay literal in the worktree's
	// .local.env and personal-overlay env keys go missing — which broke
	// `niwa worktree create` for any workspace using a personal global
	// override or a vault-backed env entry. See issue #162.
	//
	// AllowMissingSecrets: true is deliberate. A worktree apply is a localized
	// re-materialization; the instance create / apply already enforced strict
	// secret resolution at bootstrap. A transient vault outage during a
	// worktree apply should warn-and-continue rather than hard-fail the
	// worktree.
	globalOverride := loadGlobalConfigOverride()
	teamBundle, err := resolve.BuildBundle(ctx, vault.DefaultRegistry, cfg.Vault, "workspace config")
	if err != nil {
		return nil, fmt.Errorf("building team vault bundle: %w", err)
	}
	defer teamBundle.CloseAll()
	var overlayRegistry *config.VaultRegistry
	if globalOverride != nil {
		overlayRegistry = globalOverride.Global.Vault
	}
	personalBundle, err := resolve.BuildBundle(ctx, vault.DefaultRegistry, overlayRegistry, "global overlay")
	if err != nil {
		return nil, fmt.Errorf("building personal-overlay vault bundle: %w", err)
	}
	defer personalBundle.CloseAll()

	gDir, _ := config.GlobalConfigDir()
	effectiveCfg, globalEnvExamplePolicy, globalEnvOutput, err := workspace.ResolveAndMergeEffectiveConfig(
		ctx, cfg, globalOverride, teamBundle, personalBundle,
		workspace.EffectiveConfigOptions{
			AllowMissingSecrets: true,
			GlobalConfigDir:     gDir,
			Stderr:              os.Stderr,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("resolving effective workspace config: %w", err)
	}
	cfg = effectiveCfg
	opts.GlobalEnvExamplePolicy = globalEnvExamplePolicy
	opts.GlobalEnvOutput = globalEnvOutput

	return workspace.ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, group, repo, purpose, branch, opts)
}

// loadGlobalConfigOverride reads the already-synced personal global config
// override (niwa.toml under the registered global config dir) and returns the
// parsed value. It does NOT sync the snapshot (apply/create already did) and
// never fails the worktree apply: any unavailability (no global config
// registered, no niwa.toml, or a parse error) returns nil, which the
// resolve+merge helper treats as "no overlay registered" (team-only resolve,
// no merge).
func loadGlobalConfigOverride() *config.GlobalConfigOverride {
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
	return parsed
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
// It mirrors the apply path's overlay-vault resolution (apply.go ~887-920):
// it builds the overlay's own vault bundle and resolves the overlay's env and
// per-repo secrets against it before merging, then carries overlay.Vault into
// the merged config (via MergeWorkspaceOverlay) so the merged config keeps the
// provider. Resolution runs with AllowMissing so an unresolvable provider
// degrades gracefully (skip/warn) instead of failing the content install --
// the worktree create path must not be stricter than `niwa apply`.
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

	// Resolve the overlay's vault references against its own provider bundle
	// before merging, the same way the apply path does. A nil Vault yields an
	// empty bundle, which passes overlay env without vault:// refs through
	// unchanged. AllowMissing keeps an unresolvable provider non-fatal.
	ctx := context.Background()
	overlayVaultBundle, bundleErr := resolve.BuildBundle(ctx, nil, overlay.Vault, "workspace-overlay.toml")
	if bundleErr != nil {
		return nil, "", fmt.Errorf("building overlay vault bundle: %w", bundleErr)
	}
	defer overlayVaultBundle.CloseAll()

	tmpCfg := &config.WorkspaceConfig{
		Env:   overlay.Env,
		Repos: overlay.Repos,
	}
	resolvedTmp, resolveErr := resolve.ResolveWorkspace(ctx, tmpCfg, resolve.ResolveOptions{
		AllowMissing: true,
		TeamBundle:   overlayVaultBundle,
	})
	if resolveErr != nil {
		return nil, "", fmt.Errorf("resolving overlay vault references: %w", resolveErr)
	}
	overlay.Env = resolvedTmp.Env
	overlay.Repos = resolvedTmp.Repos

	merged, err := workspace.MergeWorkspaceOverlay(cfg, overlay, overlayDir)
	if err != nil {
		return nil, "", fmt.Errorf("merging workspace overlay: %w", err)
	}
	return merged, overlayDir, nil
}

// resolveSessionIDByPath scans the instance's session lifecycle states for the
// one whose WorktreePath matches wantPath, returning its session id. It is the
// reverse lookup `niwa worktree destroy --by-path` (and the from-hook remove
// path) need: a worktree directory -> its owning niwa session.
//
// Both the requested path and each recorded WorktreePath are canonicalized
// (EvalSymlinks + Clean) before comparison, so a path passed with `..`
// components, a trailing slash, or through a symlink still matches the session
// that owns the same on-disk directory. A recorded path that no longer exists
// on disk falls back to a lexical Clean so an already-removed worktree's
// session can still be resolved (e.g. when destroy is retried).
//
// Terminal sessions (ended/abandoned) are skipped: their worktree directory is
// gone, and matching one would attempt to destroy an already-destroyed session.
// An unmatched path is an explicit, actionable error (exit code 1).
func resolveSessionIDByPath(instanceRoot, wantPath string) (string, error) {
	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	states, err := worktree.ListSessionLifecycleStates(sessionsDir)
	if err != nil {
		return "", fmt.Errorf("niwa: error: listing sessions: %w", err)
	}

	wantCanon := canonicalizeWorktreePath(wantPath)
	for _, st := range states {
		if st.WorktreePath == "" {
			continue
		}
		if st.Status == worktree.SessionStatusEnded || st.Status == worktree.SessionStatusAbandoned {
			continue
		}
		if canonicalizeWorktreePath(st.WorktreePath) == wantCanon {
			return st.SessionID, nil
		}
	}

	return "", &sessionattach.ExitCodeError{
		Code: 1,
		Msg: fmt.Sprintf("niwa: error: no active worktree found at path %q. "+
			"Run `niwa worktree list` to see worktrees and their paths.", wantPath),
	}
}

// canonicalizeWorktreePath resolves symlinks and cleans path for comparison.
// When the path does not exist on disk (EvalSymlinks fails), it falls back to
// the absolute, lexically-cleaned form so an already-removed worktree still
// compares equal to the same lexical path recorded in its session state.
func canonicalizeWorktreePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func runSessionDestroy(cmd *cobra.Command, args []string) error {
	// Exactly one identifier is required: either a positional <session-id> OR
	// --by-path <path>, but not both and not neither.
	if sessionDestroyByPath != "" && len(args) >= 1 {
		return &sessionattach.ExitCodeError{
			Code: 2,
			Msg: "niwa: usage: niwa worktree destroy takes either <session-id> or --by-path <path>, not both. " +
				"Run `niwa worktree list` to discover existing worktrees.",
		}
	}
	if sessionDestroyByPath == "" && len(args) != 1 {
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

	var sessionID string
	if sessionDestroyByPath != "" {
		sessionID, err = resolveSessionIDByPath(instanceRoot, sessionDestroyByPath)
		if err != nil {
			return err
		}
	} else {
		sessionID = args[0]
	}

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
