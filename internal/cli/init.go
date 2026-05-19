package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/plugin"
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
	initCmd.Flags().BoolVar(&initNoInstallPlugins, "no-install-plugins", false, "skip auto-installing the embedded niwa Claude Code plugin (otherwise installed once when a rank-2 source is detected)")
	initCmd.Flags().BoolVar(&initBootstrap, "bootstrap", false, "when the source repo has no .niwa/workspace.toml, scaffold a minimal config and stage it on a niwa-bootstrap branch")
	initCmd.Flags().BoolVar(&initNoBootstrap, "no-bootstrap", false, "explicitly decline bootstrap; equivalent to answering N at the R13 prompt (mutually exclusive with --bootstrap)")
	initCmd.ValidArgsFunction = completeWorkspaceNames
}

var (
	initFrom             string
	initSkipGlobal       bool
	initOverlay          string
	initNoOverlay        bool
	initRebind           bool
	initNoInstallPlugins bool
	initBootstrap        bool
	initNoBootstrap      bool
)

// materializeFromSource is a package-level seam so unit tests can
// inject a fake materialize result (typed errors, custom rank) without
// hitting the network. Production defaults to workspace.MaterializeFromSource.
var materializeFromSource = workspace.MaterializeFromSource

// runBootstrap is the entry point Issue 4 will populate with the real
// scaffold+commit+push orchestrator. Issue 2 stubs it as an
// init-step failure so the existing workspaceCreated defer reclaims
// the directory per R7.
var runBootstrap = func(ctx context.Context, root string, src sourcepkg.Source) error {
	return errors.New("bootstrap step=create: not implemented yet")
}

// handleNoMarkerR13 implements the PRD R13 Flag Interactions table
// for the *config.NoMarkerError arm. It runs AFTER R25 mutual exclusion
// (already validated in runInit) and AFTER R9 host check (gated on
// initBootstrap, already validated for bootstrap callers). The
// effective flag state is whatever the caller passed today —
// initBootstrap / initNoBootstrap are read directly.
//
// Returns (proceed, err):
//   - (true, nil): caller should dispatch to bootstrap (either because
//     --bootstrap was set or because the TTY user typed Y).
//   - (false, nil): TTY user typed N (clean decline, exit 0).
//   - (false, err): fail-fast paths — non-TTY no-flag, or --no-bootstrap.
//
// matErr carries the original materialize error so the NoMarker text
// in the fail-fast strings comes from the typed error's Error() —
// the same text the classifier helper produces.
func handleNoMarkerR13(cmd *cobra.Command, matErr error, noMarker *config.NoMarkerError) (bool, error) {
	// Branch 1: --bootstrap set → proceed (R13 row 1).
	if initBootstrap {
		return true, nil
	}
	// Branch 2: --no-bootstrap set → fail-fast with NoMarker text +
	// decline reason. Exit 4 per R23. Applies to both TTY and non-TTY
	// (rows 2 and 5).
	if initNoBootstrap {
		return false, displayConflict(&workspace.InitConflictError{
			Err:        matErr,
			Detail:     noMarker.Error(),
			Suggestion: "--no-bootstrap was set; rerun without --no-bootstrap to opt into the bootstrap scaffold.",
			ExitCode:   4,
		})
	}

	// Branch 3: neither flag set.
	if !IsStdinTTY() {
		// Row 6: non-TTY no-flag → fail-fast, exact PRD R13 string.
		return false, displayConflict(&workspace.InitConflictError{
			Err:      matErr,
			Detail:   "remote has no .niwa/workspace.toml and stdin is not a terminal; re-run with --bootstrap to scaffold",
			ExitCode: 4,
		})
	}

	// Row 3: TTY no-flag → R13 prompt with re-prompt on unknown input.
	return promptBootstrap(cmd.InOrStdin(), cmd.ErrOrStderr())
}

// promptBootstrap implements the R13 TTY prompt loop. Returns
// (true, nil) on Y/y/bare-Enter, (false, nil) on N/n. Any other input
// re-prompts on the same writer. EOF mid-prompt is surfaced as
// (false, err) so callers can distinguish a closed stdin from a clean
// "N" decline.
//
// Factored out so unit tests can hit the prompt loop without touching
// the rest of runInit. The TTY-vs-non-TTY decision belongs to the
// caller — this helper assumes the caller has already gated on
// IsStdinTTY.
func promptBootstrap(in io.Reader, out io.Writer) (bool, error) {
	const prompt = "Remote has no .niwa/workspace.toml. Scaffold a minimal config and stage it on a niwa-bootstrap branch? [Y/n] "
	reader := bufio.NewReader(in)
	for {
		if _, err := fmt.Fprint(out, prompt); err != nil {
			return false, fmt.Errorf("writing prompt: %w", err)
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			// EOF on a non-empty buffered line counts as a valid
			// final answer; only true EOF on empty input propagates.
			if err == io.EOF && line != "" {
				// Fall through to the switch below.
			} else {
				return false, fmt.Errorf("reading bootstrap confirmation: %w", err)
			}
		}
		switch strings.TrimSpace(line) {
		case "", "y", "Y":
			return true, nil
		case "n", "N":
			return false, nil
		default:
			// Unknown input: re-prompt. Continue the loop.
		}
	}
}

// initConflictDisplay wraps a *workspace.InitConflictError so the
// rendered error text matches the historical "Detail\n  Suggestion"
// shape that runInit's existing display layer emits, while preserving
// the typed shape via Unwrap so cli.Execute() can recover ExitCode
// through errors.As. The R23 exit-code mapping reads the inner
// *InitConflictError's ExitCode field after type-asserting through
// this wrapper.
type initConflictDisplay struct {
	inner *workspace.InitConflictError
}

func (e *initConflictDisplay) Error() string {
	if e.inner.Suggestion == "" {
		return e.inner.Detail
	}
	return fmt.Sprintf("%s\n  %s", e.inner.Detail, e.inner.Suggestion)
}

func (e *initConflictDisplay) Unwrap() error { return e.inner }

// displayConflict wraps c so it prints with the legacy format and the
// ExitCode survives the chain via errors.As.
func displayConflict(c *workspace.InitConflictError) error {
	return &initConflictDisplay{inner: c}
}

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

	// PRD R25: --bootstrap and --no-bootstrap are mutually exclusive
	// and run UPSTREAM of every other init validation. Exit code 2 per
	// PRD R23. The InitConflictError carries the ExitCode field so
	// cli.Execute() maps it to os.Exit(2); the display wrapper prints
	// only the exact PRD-mandated string (no "sentinel:" prefix, no
	// trailing suggestion line).
	if initBootstrap && initNoBootstrap {
		return displayConflict(&workspace.InitConflictError{
			Err:      errors.New("--bootstrap and --no-bootstrap are mutually exclusive"),
			Detail:   "--bootstrap and --no-bootstrap are mutually exclusive",
			ExitCode: 2,
		})
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

	// PRD R2 + R9: when --bootstrap is set with a clone source, parse
	// the source up front so we can (a) derive the workspace name from
	// the repo basename when no positional was supplied (R2), and (b)
	// run the GitHub host check BEFORE any git invocation (R9 + R21).
	// This step is gated on initBootstrap so the non-bootstrap clone
	// path keeps its existing semantics — slug validation, ResolveCloneURL,
	// MaterializeFromSource — unchanged.
	//
	// The parsed source is reused inside the modeClone case below to
	// avoid a second parse call; we stash it in bootstrapSrc when set.
	var bootstrapSrc sourcepkg.Source
	var bootstrapSrcParsed bool
	if initBootstrap && mode == modeClone && source != "" {
		// Re-run slug-shape validation for non-URL inputs so a malformed
		// --from slug surfaces the canonical parser error rather than
		// "unsupported host" from IsGitHub on a garbage Host.
		if !looksLikeURL(source) {
			if _, parseErr := sourcepkg.Parse(source); parseErr != nil {
				return fmt.Errorf("parsing --from slug: %w", parseErr)
			}
		}
		src, parseErr := parseInitSource(source)
		if parseErr != nil {
			return fmt.Errorf("parsing --from: %w", parseErr)
		}
		bootstrapSrc = src
		bootstrapSrcParsed = true

		// R9 / R21: refuse non-GitHub hosts BEFORE any git invocation.
		// Canonical slug form leaves src.Host == ""; IsGitHub treats
		// that as GitHub per source.go:148. Exit code 3 per R23.
		if !src.IsGitHub() {
			return displayConflict(&workspace.InitConflictError{
				Err:      errors.New("non-github host"),
				Detail:   fmt.Sprintf("bootstrap supports only GitHub sources in v1; got host=%s", src.Host),
				ExitCode: 3,
			})
		}

		// R2: no positional arg + --bootstrap → derive name from src.Repo.
		// Non-bootstrap no-name behavior (line 156-160 in resolveInitMode)
		// is intentionally unchanged.
		if name == "" {
			name = src.Repo
		}
	}

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
	//
	// PRD R5: if the workspace directory did not exist before init and
	// init fails before scaffolding completes, the directory must be
	// removed. The deferred cleanup below clears workspaceCreated on
	// success so it only fires on the error path.
	var workspaceCreated bool
	if name != "" {
		if err := os.Mkdir(workspaceRoot, 0o755); err != nil {
			return fmt.Errorf("creating workspace directory: %w", err)
		}
		workspaceCreated = true
		defer func() {
			if workspaceCreated {
				_ = os.RemoveAll(workspaceRoot)
			}
		}()
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
		// Skipped entirely when the bootstrap path already pre-parsed
		// the source (R9 / R2) so we don't double-validate.
		if !bootstrapSrcParsed && !looksLikeURL(source) {
			if _, parseErr := sourcepkg.Parse(source); parseErr != nil {
				return fmt.Errorf("parsing --from slug: %w", parseErr)
			}
		}

		var src sourcepkg.Source
		if bootstrapSrcParsed {
			src = bootstrapSrc
		} else {
			parsed, parseErr := parseInitSource(source)
			if parseErr != nil {
				return fmt.Errorf("parsing --from: %w", parseErr)
			}
			src = parsed
		}

		cloneURL, err := workspace.ResolveCloneURL(source, globalCfg.CloneProtocol())
		if err != nil {
			return fmt.Errorf("resolving clone URL: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Initializing from: %s\n", cloneURL)

		niwaDir := filepath.Join(workspaceRoot, workspace.StateDir)
		fetcher := github.NewAPIClient(resolveGitHubToken())
		reporter := workspace.NewReporter(os.Stderr)
		teamConfigRank, materializeErr := materializeFromSource(cmd.Context(), src, source, niwaDir, config.TeamConfigMarkerSet(), fetcher, reporter)
		if materializeErr != nil {
			// PRD R13 NoMarker dispatch runs in the cli layer, not in
			// the classifier — the prompt + TTY-detection branches
			// can mutate the effective "should we bootstrap?" decision
			// before the classifier maps the error into an exit code.
			//
			// Walk the chain once for *config.NoMarkerError so we can
			// honor the R13 Flag Interactions table before calling
			// classifyMaterializeError.
			var noMarker *config.NoMarkerError
			if errors.As(materializeErr, &noMarker) {
				proceed, dispatchErr := handleNoMarkerR13(cmd, materializeErr, noMarker)
				if dispatchErr != nil {
					return dispatchErr
				}
				if !proceed {
					// TTY-decline: clean exit 0. Disarm the workspace-dir
					// cleanup defer so the user keeps the empty dir they
					// can re-init in. R7 leaves cleanup off the table for
					// the "clean decline" path; this matches the R23
					// "exit 0" semantics for typed N.
					workspaceCreated = false
					return nil
				}
				// proceed == true → fall through to the bootstrap stub
				// below as if --bootstrap had been set originally.
				return runBootstrap(cmd.Context(), workspaceRoot, src)
			}

			// Non-NoMarker error → run the precedence classifier from
			// Issue 1. classifyMaterializeError returns either a typed
			// conflict (rendered via displayConflict) or the original
			// error for the existing bare wrap to keep semantic parity.
			conflict, rest := classifyMaterializeError(materializeErr, initBootstrap)
			if conflict != nil {
				return displayConflict(conflict)
			}
			if rest != nil {
				return fmt.Errorf("materializing config repo: %w", rest)
			}
			// (nil, nil) from the classifier only fires on the
			// NoMarker + --bootstrap arm — already handled above by the
			// NoMarker fast path. Reaching here is unreachable.
			return runBootstrap(cmd.Context(), workspaceRoot, src)
		}
		// PRD R10: emit the rank-2 deprecation notice for the team
		// config when the source resolves to the legacy whole-repo
		// layout. State is nil here because the workspace's
		// InstanceState is created later in scaffoldInstance — the
		// disclosed-notices guard fires on subsequent applies (apply
		// captures team-config rank and gates on workspace-root state).
		if teamConfigRank == 2 {
			workspace.EmitRank2Notice(workspace.NoticeIDRank2TeamConfig, source, reporter)
			// PRD R16-R20: install the embedded niwa Claude Code plugin
			// so /niwa:migrate-config is available next time the user
			// invokes Claude Code. SkipInstall ORs the per-invocation
			// --no-install-plugins flag with the persistent
			// auto_install_plugins = false global-config setting (PRD R19).
			skipInstall := initNoInstallPlugins || globalCfg.SkipPluginInstall()
			plugin.Install(nil, reporter, plugin.InstallOpts{SkipInstall: skipInstall})
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
	// Success — disarm the workspace-dir cleanup defer.
	workspaceCreated = false
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
			workspace.EmitRank2Notice(workspace.NoticeIDRank2Overlay, initOverlay, reporter)
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
							workspace.EmitRank2Notice(workspace.NoticeIDRank2Overlay, conventionURL, reporter)
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
