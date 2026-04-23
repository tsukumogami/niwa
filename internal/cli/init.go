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

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().StringVar(&initFrom, "from", "", "org/repo or URL to clone workspace config from")
	initCmd.Flags().BoolVar(&initSkipGlobal, "skip-global", false, "disable global config overlay for this instance")
	initCmd.Flags().StringVar(&initOverlay, "overlay", "", "overlay repo (org/repo or URL) to clone and associate with this workspace")
	initCmd.Flags().BoolVar(&initNoOverlay, "no-overlay", false, "disable overlay discovery and association for this workspace")
	initCmd.ValidArgsFunction = completeWorkspaceNames
}

var (
	initFrom       string
	initSkipGlobal bool
	initOverlay    string
	initNoOverlay  bool
)

var initCmd = &cobra.Command{
	Use:   "init [name]",
	Short: "Initialize a new workspace",
	Long: `Initialize a new niwa workspace in the current directory.

Three modes:

  niwa init
    Scaffold a minimal .niwa/workspace.toml with commented examples.
    The workspace name defaults to "workspace". No registry entry is created.

  niwa init <name>
    If the name is registered in the global registry with a source URL,
    clone from that source (same as --from). Otherwise scaffold locally
    and register the workspace as local-only.

  niwa init <name> --from <org/repo>
    Shallow-clone the config repo as .niwa/ and register the name-to-source
    mapping in the global registry.`,
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

	// Pre-flight: check for conflicts before any writes.
	if err := workspace.CheckInitConflicts(cwd); err != nil {
		var conflict *workspace.InitConflictError
		if errors.As(err, &conflict) {
			return fmt.Errorf("%s\n  %s", conflict.Detail, conflict.Suggestion)
		}
		return err
	}

	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("loading global config: %w", err)
	}

	mode, name, source := resolveInitMode(args, initFrom, globalCfg)

	switch mode {
	case modeScaffold:
		if err := workspace.Scaffold(cwd, ""); err != nil {
			return fmt.Errorf("scaffolding workspace: %w", err)
		}

	case modeNamed:
		if err := workspace.Scaffold(cwd, name); err != nil {
			return fmt.Errorf("scaffolding workspace: %w", err)
		}

	case modeClone:
		// Validate the slug shape early via the canonical parser
		// (PRD R3 strict parsing). Skip when source looks like a full
		// URL or SSH address — those go straight to ResolveCloneURL
		// per today's behavior.
		if !looksLikeURL(source) {
			if _, parseErr := sourcepkg.Parse(source); parseErr != nil {
				return fmt.Errorf("parsing --from slug: %w", parseErr)
			}
		}

		cloneURL, err := workspace.ResolveCloneURL(source, globalCfg.CloneProtocol())
		if err != nil {
			return fmt.Errorf("resolving clone URL: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Initializing from: %s\n", cloneURL)

		niwaDir := filepath.Join(cwd, workspace.StateDir)
		cloner := &workspace.Cloner{}
		_, err = cloner.CloneWith(cmd.Context(), cloneURL, niwaDir, workspace.CloneOptions{Depth: 1}, workspace.NewReporter(os.Stderr))
		if err != nil {
			return fmt.Errorf("cloning config repo: %w", err)
		}
	}

	// Post-flight: verify workspace.toml exists and parses.
	configPath := filepath.Join(cwd, workspace.StateDir, workspace.WorkspaceConfigFile)
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
		absRoot, err := filepath.Abs(cwd)
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
	// set; a missing state file is fine (apply will create it later).
	state, stateErr := buildInitState(cmd, mode, source)
	if stateErr != nil {
		return stateErr
	}
	if state != nil {
		if saveErr := workspace.SaveState(cwd, state); saveErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not write instance state: %v\n", saveErr)
		}
	}

	printSuccess(cmd, mode, name, result.Config.Workspace.Name)
	return nil
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
// pre-apply state (--skip-global, --no-overlay, --overlay). Returns (nil, nil)
// when no state flags were set so that no state file is written. Returns a
// non-nil error when an explicit --overlay clone fails (hard error by design).
func buildInitState(cmd *cobra.Command, mode initMode, source string) (*workspace.InstanceState, error) {
	needsState := initSkipGlobal || initNoOverlay || initOverlay != "" || (mode == modeClone)
	if !needsState {
		return nil, nil
	}

	state := &workspace.InstanceState{
		SchemaVersion: workspace.SchemaVersion,
		SkipGlobal:    initSkipGlobal,
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
		_, cloneErr := workspace.CloneOrSyncOverlay(initOverlay, overlayDir)
		if cloneErr != nil {
			return nil, fmt.Errorf("overlay clone failed: %w", cloneErr)
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
					wasCloneAttempt, cloneErr := workspace.CloneOrSyncOverlay(conventionURL, overlayDir)
					if cloneErr != nil {
						// Any clone failure is silently skipped — the overlay repo may
						// not exist or may be inaccessible. wasCloneAttempt=false errors
						// (pull failure on an existing clone) are also non-fatal at init
						// time since no state has been written yet.
						_ = wasCloneAttempt
					} else {
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

// printSuccess outputs a success message with next steps.
func printSuccess(cmd *cobra.Command, mode initMode, name, resolvedName string) {
	w := cmd.OutOrStdout()

	switch mode {
	case modeScaffold:
		fmt.Fprintln(w, "Workspace initialized.")
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Next steps:")
		fmt.Fprintln(w, "  1. Edit .niwa/workspace.toml to configure sources and groups")
		fmt.Fprintln(w, "  2. Run niwa apply to set up the workspace")
	case modeNamed:
		fmt.Fprintf(w, "Workspace %q initialized.\n", resolvedName)
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Next steps:")
		fmt.Fprintln(w, "  1. Edit .niwa/workspace.toml to configure sources and groups")
		fmt.Fprintln(w, "  2. Run niwa apply to set up the workspace")
	case modeClone:
		fmt.Fprintf(w, "Workspace %q initialized from remote config.\n", resolvedName)
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Next steps:")
		fmt.Fprintln(w, "  1. Run niwa apply to set up the workspace")
	}
}
