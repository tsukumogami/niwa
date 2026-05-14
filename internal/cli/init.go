package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	sourcepkg "github.com/tsukumogami/niwa/internal/source"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// looksLikeURL reports whether s is a full URL or SSH address that
// should bypass slug-grammar validation. Used by the init command to
// avoid running source.Parse on non-slug inputs (file://, https://,
// git@host:path) which the existing ResolveCloneURL handles directly.
func looksLikeURL(s string) bool {
	return strings.Contains(s, "://") || strings.HasPrefix(s, "git@") ||
		(strings.HasPrefix(s, "/") && strings.Count(s, "/") > 1)
}

// parseInitSource normalizes the --from input into a source.Source.
// Slug shapes (org/repo, host/owner/repo[:subpath][@ref]) feed
// source.Parse. URL shapes (https://, git@, file://) reuse the
// overlay parser since it already understands those forms.
func parseInitSource(input string) (sourcepkg.Source, error) {
	if !looksLikeURL(input) {
		return sourcepkg.Parse(input)
	}
	return workspace.ParseSourceURL(input)
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().StringVar(&initFrom, "from", "", "org/repo or URL to clone workspace config from")
	initCmd.Flags().BoolVar(&initSkipGlobal, "skip-global", false, "disable global config overlay for this instance")
	initCmd.Flags().StringVar(&initOverlay, "overlay", "", "overlay repo (org/repo or URL) to clone and associate with this workspace")
	initCmd.Flags().BoolVar(&initNoOverlay, "no-overlay", false, "disable overlay discovery and association for this workspace")
	initCmd.Flags().BoolVar(&initRebind, "rebind", false, "rebind a registered workspace name to this directory (use only when intentionally moving a workspace)")
	initCmd.ValidArgsFunction = completeWorkspaceNames
}

var (
	initFrom       string
	initSkipGlobal bool
	initOverlay    string
	initNoOverlay  bool
	initRebind     bool
)

var initCmd = &cobra.Command{
	Use:   "init [name]",
	Short: "Initialize a new workspace",
	Long: `Initialize a new niwa workspace.

Three modes:

  niwa init
    Scaffold a minimal .niwa/workspace.toml with commented examples in
    the current directory. The workspace name defaults to "workspace".
    No registry entry is created.

  niwa init <name>
    Creates <cwd>/<name>/ and initializes the workspace inside it. If
    the name is registered in the global registry with a source URL,
    clone from that source (same as --from); otherwise scaffold locally
    and register the workspace as local-only. The explicit <name>
    overrides whatever the cloned [workspace] name declares, and that
    explicit name is what every niwa command surfaces from this point
    on.

  niwa init <name> --from <org/repo>
    Creates <cwd>/<name>/ and shallow-clones the config repo into its
    .niwa/ subdirectory. The explicit <name> overrides the cloned
    [workspace] name; the on-disk workspace.toml is not modified.

When a positional <name> already exists in the global registry pointing
to a different directory, init refuses by default. Pass --rebind to
retarget the entry to the new directory (the previous directory at the
old root is left intact).

When the niwa shell wrapper is sourced, a successful "niwa init <name>"
also leaves your shell inside the new workspace directory — the same
mechanism "niwa go" and "niwa create" use. Without the wrapper, read
the path from the success message and cd manually.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

// initMode classifies the init invocation.
type initMode int

const (
	modeScaffold initMode = iota // no args: scaffold with default name
	modeNamed                    // name given, not registered
	modeClone                    // name given + source (from flag or registry)
)

// resolveInitMode determines the init mode from args and flags.
// It returns the mode, workspace name, and source URL (empty for scaffold/named).
func resolveInitMode(args []string, from string, globalCfg *config.GlobalConfig) (initMode, string, string) {
	if len(args) == 0 {
		if from != "" {
			// Clone without explicit name -- name will be derived from config after cloning.
			return modeClone, "", from
		}
		return modeScaffold, "", ""
	}

	name := args[0]

	if from != "" {
		return modeClone, name, from
	}

	// Check the registry for a source URL.
	entry := globalCfg.LookupWorkspace(name)
	if entry != nil && entry.SourceURL != "" {
		return modeClone, name, entry.SourceURL
	}

	return modeNamed, name, ""
}

func runInit(cmd *cobra.Command, args []string) error {
	// Mutual exclusion: --overlay and --no-overlay cannot be used together.
	if initOverlay != "" && initNoOverlay {
		return fmt.Errorf("--overlay and --no-overlay are mutually exclusive")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("loading global config: %w", err)
	}

	// Preflight order (per PRD R5/R6/R7/R8):
	//   1. Name validation (R7) — gated on the user supplying a positional
	//      arg, NOT on the resolved name being non-empty. `niwa init ""`
	//      MUST be rejected by R7 / AC-18; without this gate the empty
	//      string would silently fall through to modeScaffold.
	//   2. Target-exists pre-gate via os.Lstat with R6 sub-case routing
	//   3. CheckInitConflicts (niwa-state walk-up + nested instance)
	//   4. Registry-collision check (R8), gated by --rebind
	if len(args) >= 1 {
		if err := workspace.ValidateInitName(args[0]); err != nil {
			return err
		}
	}

	mode, name, source := resolveInitMode(args, initFrom, globalCfg)

	workspaceRoot := cwd
	if name != "" {
		workspaceRoot = filepath.Join(cwd, name)
	}

	if name != "" {
		if err := preflightTargetExists(workspaceRoot); err != nil {
			var conflict *workspace.InitConflictError
			if errors.As(err, &conflict) {
				return fmt.Errorf("%s\n  %s", conflict.Detail, conflict.Suggestion)
			}
			return err
		}
	}

	if err := workspace.CheckInitConflicts(workspaceRoot); err != nil {
		var conflict *workspace.InitConflictError
		if errors.As(err, &conflict) {
			return fmt.Errorf("%s\n  %s", conflict.Detail, conflict.Suggestion)
		}
		return err
	}

	var rebindFromRoot string
	if name != "" {
		absWorkspaceRoot, absErr := filepath.Abs(workspaceRoot)
		if absErr != nil {
			return fmt.Errorf("resolving workspace root: %w", absErr)
		}
		if entry := globalCfg.LookupWorkspace(name); entry != nil && entry.Root != absWorkspaceRoot {
			if !initRebind {
				conflict := &workspace.InitConflictError{
					Err:        workspace.ErrRegistryNameInUse,
					Detail:     fmt.Sprintf("workspace name %q is already registered (root: %s)", name, entry.Root),
					Suggestion: registryCollisionSuggestion(name),
				}
				return fmt.Errorf("%s\n  %s", conflict.Detail, conflict.Suggestion)
			}
			rebindFromRoot = entry.Root
		}
	}

	// Create the workspace directory (named modes only). os.Mkdir, NOT
	// MkdirAll — closes the symlink-TOCTOU window between the Lstat
	// pre-gate and creation per Security Considerations §3.
	if name != "" {
		if err := os.Mkdir(workspaceRoot, 0o755); err != nil {
			return fmt.Errorf("creating workspace directory: %w", err)
		}
	}

	switch mode {
	case modeScaffold:
		if err := workspace.Scaffold(workspaceRoot, ""); err != nil {
			return fmt.Errorf("scaffolding workspace: %w", err)
		}

	case modeNamed:
		if err := workspace.Scaffold(workspaceRoot, name); err != nil {
			return fmt.Errorf("scaffolding workspace: %w", err)
		}

	case modeClone:
		// Validate the slug shape early via the canonical parser
		// (PRD R3 strict parsing). Skip when source looks like a full
		// URL or SSH address — those go through parseInitSource below.
		if !looksLikeURL(source) {
			if _, parseErr := sourcepkg.Parse(source); parseErr != nil {
				return fmt.Errorf("parsing --from slug: %w", parseErr)
			}
		}

		src, err := parseInitSource(source)
		if err != nil {
			return fmt.Errorf("parsing --from: %w", err)
		}

		cloneURL, err := workspace.ResolveCloneURL(source, globalCfg.CloneProtocol())
		if err != nil {
			return fmt.Errorf("resolving clone URL: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Initializing from: %s\n", cloneURL)

		niwaDir := filepath.Join(workspaceRoot, workspace.StateDir)
		fetcher := github.NewAPIClient(resolveGitHubToken())
		reporter := workspace.NewReporter(os.Stderr)
		teamConfigRank, err := workspace.MaterializeFromSource(cmd.Context(), src, source, niwaDir, config.TeamConfigMarkerSet(), fetcher, reporter)
		if err != nil {
			return fmt.Errorf("materializing config repo: %w", err)
		}
		// PRD R10: emit the rank-2 deprecation notice for the team
		// config when the source resolves to the legacy whole-repo
		// layout. State is nil here because the workspace's
		// InstanceState is created later in scaffoldInstance — the
		// disclosed-notices guard fires on subsequent applies (apply
		// captures team-config rank and gates on workspace-root state).
		if teamConfigRank == 2 {
			workspace.EmitRank2Notice(nil, workspace.NoticeIDRank2TeamConfig, source, reporter)
		}
	}

	// Post-flight: verify workspace.toml exists and parses.
	configPath := filepath.Join(workspaceRoot, workspace.StateDir, workspace.WorkspaceConfigFile)
	result, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("post-flight verification failed: %w", err)
	}

	// Emit a post-clone vault bootstrap pointer when the cloned or
	// scaffolded workspace declares any [vault.*] provider. The
	// message only fires for clone mode by design: a fresh scaffold
	// has nothing but commented examples, so there is nothing to
	// bootstrap until the user uncomments a provider. The pointer
	// is written to stderr so it does not pollute the success
	// message that downstream shell redirects might capture.
	if mode == modeClone {
		emitVaultBootstrapPointer(cmd, result.Config)
	}

	// Register in global registry (skip for detached/no-args mode).
	if mode != modeScaffold {
		absRoot, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return fmt.Errorf("resolving workspace root: %w", err)
		}
		absConfigPath, err := filepath.Abs(configPath)
		if err != nil {
			return fmt.Errorf("resolving config path: %w", err)
		}

		registryName := name
		if registryName == "" {
			registryName = result.Config.Workspace.Name
		}

		entry := config.RegistryEntry{
			Root:   absRoot,
			Source: absConfigPath,
		}
		if source != "" {
			entry.SourceURL = source
		}

		globalCfg.SetRegistryEntry(registryName, entry)
		if err := config.SaveGlobalConfig(globalCfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not update registry: %v\n", err)
		}
	}

	// Build the instance state to persist. Always write when any state flag is
	// set, when a positional name is given (so the override propagates to
	// apply), or in clone mode; a missing state file is fine otherwise.
	state, stateErr := buildInitState(cmd, mode, source, name)
	if stateErr != nil {
		return stateErr
	}
	if state != nil {
		if saveErr := workspace.SaveState(workspaceRoot, state); saveErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not write instance state: %v\n", saveErr)
		}
	}

	// Rebind confirmation warning (after success). Prominent on stderr
	// per Security Considerations §6 — `--rebind` opens a registry-write
	// path, and an automated agent passing it programmatically still
	// leaves an audit trail.
	if rebindFromRoot != "" {
		absRoot, _ := filepath.Abs(workspaceRoot)
		if absRoot == "" {
			absRoot = workspaceRoot
		}
		errStream := cmd.ErrOrStderr()
		fmt.Fprintf(errStream, "WARNING: registry entry %q rebound from %s to %s\n", name, rebindFromRoot, absRoot)
		fmt.Fprintln(errStream, "")
	}

	// Override note (R4): per-invocation stderr note when the explicit
	// positional name differs from the cloned config's [workspace] name.
	// AC-8b: suppressed when the names match.
	if name != "" && result.Config.Workspace.Name != "" && result.Config.Workspace.Name != name {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: workspace name %q overrides %q from cloned config.\n", name, result.Config.Workspace.Name)
	}

	// R9 / AC-26: success message includes the resolved absolute path of
	// the workspace root. Symlinks in cwd ancestry are followed via
	// EvalSymlinks; on macOS this resolves /var/... to /private/var/...
	absForMsg, evalErr := filepath.EvalSymlinks(workspaceRoot)
	if evalErr != nil {
		absForMsg, _ = filepath.Abs(workspaceRoot)
		if absForMsg == "" {
			absForMsg = workspaceRoot
		}
	}

	printSuccess(cmd, mode, name, result.Config.Workspace.Name, absForMsg)

	// PRD R10a: when a positional name was given AND the shell wrapper
	// is sourced (NIWA_RESPONSE_FILE captured at process start), write
	// the resolved workspace root so the wrapper cd's the caller into
	// the new directory. No-args modes (modeScaffold without a name,
	// or `--from`-only) do NOT write — the user is already in the
	// workspace dir and a write would change wrapper behavior on the
	// unchanged code path. Failures earlier in runInit have already
	// returned, so the file stays empty when init didn't succeed.
	if name != "" {
		if err := writeLandingPath(absForMsg); err != nil {
			return err
		}
	}
	return nil
}

// preflightTargetExists implements the R5 + R6 caller-side existence
// check on the to-be-created `<cwd>/<name>` directory. R6 takes
// precedence over R5: when the existing path is itself a niwa workspace
// or contains an orphan .niwa/, surface the more specific
// ErrWorkspaceExists / ErrNiwaDirectoryExists error so the existing
// remediation hints stay correct. Otherwise the generic
// ErrTargetDirExists fires with a path-type qualifier from os.Lstat.
//
// Returns nil when the path does not exist (the happy path). Returns a
// non-nil error wrapping the appropriate sentinel via InitConflictError
// when the path exists.
func preflightTargetExists(targetDir string) error {
	info, statErr := os.Lstat(targetDir)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("checking target path: %w", statErr)
	}

	absTarget, absErr := filepath.Abs(targetDir)
	if absErr != nil {
		absTarget = targetDir
	}

	// AC-11: symlinks always surface ErrTargetDirExists with qualifier
	// "symlink", regardless of whether the link target resolves to a
	// niwa workspace, an orphan .niwa/, or anything else. R6 sub-case
	// routing therefore runs only for non-symlink paths.
	if info.Mode()&os.ModeSymlink == 0 {
		niwaDir := filepath.Join(targetDir, workspace.StateDir)
		workspaceCfg := filepath.Join(niwaDir, workspace.WorkspaceConfigFile)
		if _, err := os.Stat(workspaceCfg); err == nil {
			return &workspace.InitConflictError{
				Err:        workspace.ErrWorkspaceExists,
				Detail:     fmt.Sprintf("found %s", filepath.Join(workspace.StateDir, workspace.WorkspaceConfigFile)),
				Suggestion: "Use niwa apply to update the existing workspace",
			}
		}
		if dirInfo, err := os.Stat(niwaDir); err == nil && dirInfo.IsDir() {
			absNiwa := filepath.Join(absTarget, workspace.StateDir)
			return &workspace.InitConflictError{
				Err:        workspace.ErrNiwaDirectoryExists,
				Detail:     fmt.Sprintf("found %s directory without %s", workspace.StateDir, workspace.WorkspaceConfigFile),
				Suggestion: fmt.Sprintf("Remove the %s directory and retry", absNiwa),
			}
		}
	}

	return &workspace.InitConflictError{
		Err:        workspace.ErrTargetDirExists,
		Detail:     fmt.Sprintf("%s already exists (%s)", absTarget, pathTypeQualifier(info)),
		Suggestion: "Pick a different name or remove the path and retry.",
	}
}

// pathTypeQualifier returns the human-readable path type for an
// os.Lstat result: "file", "directory", or "symlink". Other modes
// (FIFO, socket, device) return "unknown" — out of scope per PRD R5.
// Symlinks are detected without being followed.
func pathTypeQualifier(info os.FileInfo) string {
	mode := info.Mode()
	switch {
	case mode&os.ModeSymlink != 0:
		return "symlink"
	case mode.IsDir():
		return "directory"
	case mode.IsRegular():
		return "file"
	default:
		return "unknown"
	}
}

// registryCollisionSuggestion builds the suggestion text for the
// ErrRegistryNameInUse error. Resolves the global config TOML path at
// call time via config.GlobalConfigPath() so the message reflects the
// user's actual XDG_CONFIG_HOME; falls back to the literal default
// when XDG resolution fails so the error itself never blocks on path
// resolution.
func registryCollisionSuggestion(name string) string {
	cfgPath, err := config.GlobalConfigPath()
	if err != nil || cfgPath == "" {
		cfgPath = "~/.config/niwa/config.toml"
	}
	return fmt.Sprintf("Pass --rebind to retarget the entry to this directory, or remove the [registry.%s] section from %s and retry.", name, cfgPath)
}

// emitVaultBootstrapPointer writes a stderr note when the parsed
// workspace config declares one or more [vault.*] providers, telling
// the user which backend-specific bootstrap command to run before
// `niwa apply`. The note is strictly informational — init has already
// completed by the time it prints — so it never fails the command.
//
// The message names the provider kind(s) declared. For infisical, it
// points at `infisical login`. For any other kind (sops, future
// backends) it prints a generic "<kind>-specific setup (see provider
// docs)" so users immediately know what class of tool to reach for
// even when niwa has no v1 knowledge of the backend.
func emitVaultBootstrapPointer(cmd *cobra.Command, cfg *config.WorkspaceConfig) {
	if cfg == nil || cfg.Vault == nil || cfg.Vault.IsEmpty() {
		return
	}
	kinds := vaultKindsDeclared(cfg.Vault)
	if len(kinds) == 0 {
		return
	}
	err := cmd.ErrOrStderr()
	for _, kind := range kinds {
		fmt.Fprintf(err, "note: this workspace declares a vault (kind: %s). Bootstrap with:\n", kind)
		fmt.Fprintf(err, "  %s\n", bootstrapCommandFor(kind))
	}
	fmt.Fprintln(err, "Then run `niwa apply`.")
}

// vaultKindsDeclared returns the sorted, deduped list of provider
// kinds declared in vr. The anonymous [vault.provider] shape contributes
// its single kind; named [vault.providers.<name>] tables each contribute
// their own. The resulting slice is sorted so the bootstrap note is
// order-stable for tests.
func vaultKindsDeclared(vr *config.VaultRegistry) []string {
	if vr == nil {
		return nil
	}
	seen := map[string]struct{}{}
	if vr.Provider != nil && vr.Provider.Kind != "" {
		seen[vr.Provider.Kind] = struct{}{}
	}
	for _, p := range vr.Providers {
		if p.Kind != "" {
			seen[p.Kind] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// bootstrapCommandFor returns the human-readable bootstrap step for
// a vault kind. The v1 implementation hard-codes the known backends;
// unknown kinds fall through to a generic message so the note stays
// useful even for future backends not yet wired here.
func bootstrapCommandFor(kind string) string {
	switch kind {
	case "infisical":
		return "`infisical login`"
	default:
		return fmt.Sprintf("%s-specific setup (see provider docs)", kind)
	}
}

// buildInitState constructs an InstanceState for the flags that require
// pre-apply state (--skip-global, --no-overlay, --overlay) and for the
// init-time name override that a positional `niwa init <name>` records.
// Returns (nil, nil) when no state needs to be written. Returns a
// non-nil error when an explicit --overlay clone fails (hard error by
// design).
func buildInitState(cmd *cobra.Command, mode initMode, source, name string) (*workspace.InstanceState, error) {
	ctx := cmd.Context()
	needsState := initSkipGlobal || initNoOverlay || initOverlay != "" || (mode == modeClone) || name != ""
	if !needsState {
		return nil, nil
	}

	state := &workspace.InstanceState{
		SchemaVersion:      workspace.SchemaVersion,
		SkipGlobal:         initSkipGlobal,
		ConfigNameOverride: name,
	}

	switch {
	case initNoOverlay:
		state.NoOverlay = true

	case initOverlay != "":
		// --overlay is explicit user intent: clone failure is a hard error.
		overlayDir, err := config.OverlayDir(initOverlay)
		if err != nil {
			return nil, fmt.Errorf("could not determine overlay directory: %w", err)
		}
		fetcher := github.NewAPIClient(resolveGitHubToken())
		reporter := workspace.NewReporter(os.Stderr)
		_, overlayRank, cloneErr := workspace.EnsureOverlaySnapshot(ctx, initOverlay, overlayDir, fetcher, reporter)
		if cloneErr != nil {
			return nil, fmt.Errorf("overlay clone failed: %w", cloneErr)
		}
		if overlayRank == 2 {
			workspace.EmitRank2Notice(nil, workspace.NoticeIDRank2Overlay, initOverlay, reporter)
		}
		sha, shaErr := workspace.HeadSHA(overlayDir)
		if shaErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not read overlay HEAD: %v\n", shaErr)
		}
		state.OverlayURL = initOverlay
		state.OverlayCommit = sha

	default:
		// Convention discovery in modeClone: derive URL from the workspace source.
		if mode == modeClone && source != "" {
			conventionURL, ok := config.DeriveOverlayURL(source)
			if ok {
				overlayDir, dirErr := config.OverlayDir(conventionURL)
				if dirErr == nil {
					fetcher := github.NewAPIClient(resolveGitHubToken())
					reporter := workspace.NewReporter(os.Stderr)
					_, overlayRank, cloneErr := workspace.EnsureOverlaySnapshot(ctx, conventionURL, overlayDir, fetcher, reporter)
					if cloneErr != nil {
						// Any clone failure is silently skipped — the overlay repo may
						// not exist or may be inaccessible. Refresh failures on existing
						// snapshots are also non-fatal at init time since no state has
						// been written yet.
						_ = cloneErr
					} else {
						if overlayRank == 2 {
							workspace.EmitRank2Notice(nil, workspace.NoticeIDRank2Overlay, conventionURL, reporter)
						}
						sha, shaErr := workspace.HeadSHA(overlayDir)
						if shaErr != nil {
							fmt.Fprintf(os.Stderr, "warning: could not read overlay HEAD: %v\n", shaErr)
						}
						state.OverlayURL = conventionURL
						state.OverlayCommit = sha
					}
				}
			}
		}
	}

	return state, nil
}

// printSuccess outputs a success message with next steps. The
// effective name surfaced in the message prefers the explicit
// positional name (when given) over the cloned config's
// [workspace] name, matching the override semantics that downstream
// niwa commands use. The resolved absolute path is included per
// PRD R9 / AC-26.
func printSuccess(cmd *cobra.Command, mode initMode, name, resolvedName, absPath string) {
	w := cmd.OutOrStdout()
	displayName := name
	if displayName == "" {
		displayName = resolvedName
	}

	switch mode {
	case modeScaffold:
		if displayName != "" {
			fmt.Fprintf(w, "Workspace %q initialized at %s.\n", displayName, absPath)
		} else {
			fmt.Fprintf(w, "Workspace initialized at %s.\n", absPath)
		}
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Next steps:")
		fmt.Fprintln(w, "  1. Edit .niwa/workspace.toml to configure sources and groups")
		fmt.Fprintln(w, "  2. Run niwa apply to set up the workspace")
	case modeNamed:
		fmt.Fprintf(w, "Workspace %q initialized at %s.\n", displayName, absPath)
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Next steps:")
		fmt.Fprintln(w, "  1. Edit .niwa/workspace.toml to configure sources and groups")
		fmt.Fprintln(w, "  2. Run niwa apply to set up the workspace")
	case modeClone:
		fmt.Fprintf(w, "Workspace %q initialized at %s from remote config.\n", displayName, absPath)
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Next steps:")
		fmt.Fprintln(w, "  1. Run niwa apply to set up the workspace")
	}
}
