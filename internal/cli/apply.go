package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/workspace"
	"golang.org/x/term"
)

func init() {
	rootCmd.AddCommand(applyCmd)
	applyCmd.Flags().StringVar(&applyInstance, "instance", "", "target a specific instance by name")
	applyCmd.Flags().BoolVar(&applyAllowDirty, "allow-dirty", false, "apply even if config directory has uncommitted changes")
	applyCmd.Flags().BoolVar(&applyNoPull, "no-pull", false, "skip pulling latest changes into existing repos")
	applyCmd.Flags().BoolVar(&applyAllowMissingSecrets, "allow-missing-secrets", false,
		"downgrade unresolved vault:// references to empty strings with stderr warnings. "+
			"Does NOT override *.required misses. One-shot -- re-evaluated each invocation.")
	applyCmd.Flags().BoolVar(&applyAllowPlaintextSecrets, "allow-plaintext-secrets", false,
		"bypass the public-repo plaintext-secrets guardrail. Strictly one-shot -- no state persistence.")
	applyCmd.Flags().BoolVar(&applyChannels, "channels", false, "enable channel infrastructure for this invocation (overrides NIWA_CHANNELS)")
	applyCmd.Flags().BoolVar(&applyNoChannels, "no-channels", false, "disable channel infrastructure for this invocation (overrides --channels and NIWA_CHANNELS)")
	applyCmd.ValidArgsFunction = completeWorkspaceNames
	_ = applyCmd.RegisterFlagCompletionFunc("instance", completeInstanceNames)
}

var (
	applyInstance              string
	applyAllowDirty            bool
	applyNoPull                bool
	applyAllowMissingSecrets   bool
	applyAllowPlaintextSecrets bool
	applyChannels              bool
	applyNoChannels            bool
)

var applyCmd = &cobra.Command{
	Use:   "apply [workspace-name]",
	Short: "Apply workspace configuration",
	Long: `Apply discovers the workspace configuration and applies it to one or more
instances. For each managed repo, apply clones missing repos and pulls latest
changes into existing repos that are clean and on their configured default branch.
Repos with uncommitted changes or on non-default branches are skipped with a
warning. Use --no-pull to skip pulling entirely.

The default branch for each repo is resolved from: per-repo branch config,
workspace default_branch setting, or "main" as the fallback.

Scope resolution (when no workspace-name argument is given):
  1. If --instance is set, find the workspace root and apply to that instance.
  2. If cwd is inside an instance, apply to that single instance.
  3. If cwd is at the workspace root, apply to all instances.

If a workspace name is given as a positional argument, it is resolved through
the global registry (~/.config/niwa/config.toml) to find the workspace root
directory, then all instances are applied.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runApply,
}

// runApply implements the apply command logic. It is extracted from the cobra
// command for testability.
func runApply(cmd *cobra.Command, args []string) error {
	var (
		scope *workspace.ApplyScope
		err   error
	)

	if len(args) == 1 {
		// Registry lookup path: resolve workspace name to root, then apply all.
		scope, err = resolveRegistryScope(args[0])
	} else {
		// Scope resolution from cwd.
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return fmt.Errorf("getting working directory: %w", cwdErr)
		}
		scope, err = workspace.ResolveApplyScope(cwd, applyInstance)
	}
	if err != nil {
		return err
	}

	configPath := scope.Config
	if configPath == "" {
		return fmt.Errorf("could not locate workspace configuration")
	}

	configDir := filepath.Dir(configPath)
	result, err := config.Load(configPath)
	if err != nil {
		return err
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	cfg := result.Config

	token := resolveGitHubToken()
	gh := github.NewAPIClient(token)
	applier := workspace.NewApplier(gh)
	applier.Reporter = workspace.NewReporterWithTTY(os.Stderr, !noProgress && term.IsTerminal(int(os.Stderr.Fd())))
	applier.NoPull = applyNoPull
	applier.AllowDirty = applyAllowDirty
	applier.AllowMissingSecrets = applyAllowMissingSecrets
	applier.AllowPlaintextSecrets = applyAllowPlaintextSecrets

	// Resolve effective channel activation and synthesize cfg.Channels.Mesh when
	// --channels or NIWA_CHANNELS activates channels without a config section.
	cfg, applier.ChannelsSynthesized = resolveChannelsActivation(cmd, cfg, applyChannels, applyNoChannels)

	// Wire global config and ConfigSourceURL from the registry if available.
	if globalCfg, gErr := config.LoadGlobalConfig(); gErr == nil {
		// Always set GlobalConfigDir when the path resolves. SyncConfigDir
		// is a no-op when the directory has no git remote, and the niwa.toml
		// reader silently skips the file when it doesn't exist — so the guard
		// on GlobalConfig.Repo is not needed for safety, and omitting it lets
		// manually-maintained personal overlays (no remote configured) work.
		if gDir, gErr := config.GlobalConfigDir(); gErr == nil {
			applier.GlobalConfigDir = gDir
		}
		// ConfigSourceURL is the original GitHub URL stored at init time.
		// It enables convention overlay discovery when OverlayURL is not yet
		// in InstanceState (i.e., overlay was never discovered for this instance).
		if entry := globalCfg.LookupWorkspace(cfg.Workspace.Name); entry != nil {
			applier.ConfigSourceURL = entry.SourceURL
		}
	}

	// Apply to each instance, collecting errors instead of aborting on first failure.
	var applyErrors []instanceError
	for _, instanceRoot := range scope.Instances {
		if applyErr := applier.Apply(cmd.Context(), cfg, configDir, instanceRoot); applyErr != nil {
			applyErrors = append(applyErrors, instanceError{
				instance: instanceRoot,
				err:      applyErr,
			})
			fmt.Fprintf(os.Stderr, "error: applying to %s: %v\n", instanceRoot, applyErr)
		}
	}

	// Update the global registry after all instances complete.
	if regErr := updateRegistry(configPath, configDir, cfg.Workspace.Name); regErr != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", regErr)
	}

	if len(applyErrors) > 0 {
		return combineInstanceErrors(applyErrors)
	}

	return nil
}

// resolveRegistryScope looks up a workspace name in the global registry and
// returns an ApplyAll scope targeting all instances under that workspace root.
func resolveRegistryScope(name string) (*workspace.ApplyScope, error) {
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return nil, fmt.Errorf("loading global config: %w", err)
	}

	entry := globalCfg.LookupWorkspace(name)
	if entry == nil {
		return nil, fmt.Errorf("workspace %q not found in registry", name)
	}

	configPath := entry.Source
	configDir := filepath.Dir(configPath)
	workspaceRoot := filepath.Dir(configDir)

	instances, err := workspace.EnumerateInstances(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("enumerating instances: %w", err)
	}

	// Single-instance layout: the workspace root is itself the instance
	// (instance.json lives at workspaceRoot/.niwa/instance.json rather than
	// in a child subdirectory). EnumerateInstances only scans children, so
	// it returns empty for this layout. Fall back to treating workspaceRoot
	// as the sole instance.
	if len(instances) == 0 {
		if _, statErr := os.Stat(filepath.Join(workspaceRoot, workspace.StateDir, workspace.StateFile)); statErr == nil {
			instances = []string{workspaceRoot}
		}
	}

	return &workspace.ApplyScope{
		Mode:      workspace.ApplyAll,
		Instances: instances,
		Config:    configPath,
	}, nil
}

// updateRegistry updates the global registry with the workspace config path,
// preserving any existing SourceURL from a prior init --from registration.
func updateRegistry(configPath, configDir, workspaceName string) error {
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("could not load global config for registry update: %w", err)
	}

	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("could not resolve config path: %w", err)
	}

	workspaceRoot := filepath.Dir(configDir)
	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return fmt.Errorf("could not resolve workspace root: %w", err)
	}

	// Preserve SourceURL from any existing registry entry so that convention
	// overlay discovery (which needs the original GitHub URL) keeps working
	// across multiple apply invocations.
	entry := config.RegistryEntry{
		Source: absConfigPath,
		Root:   absRoot,
	}
	if existing := globalCfg.LookupWorkspace(workspaceName); existing != nil {
		entry.SourceURL = existing.SourceURL
	}

	globalCfg.SetRegistryEntry(workspaceName, entry)

	if err := config.SaveGlobalConfig(globalCfg); err != nil {
		return fmt.Errorf("could not update registry: %w", err)
	}

	return nil
}

// instanceError pairs an instance path with its apply error.
type instanceError struct {
	instance string
	err      error
}

// combineInstanceErrors returns a single error summarizing all instance failures.
func combineInstanceErrors(errs []instanceError) error {
	if len(errs) == 1 {
		return fmt.Errorf("apply failed for %s: %w", errs[0].instance, errs[0].err)
	}

	var combined []error
	for _, ie := range errs {
		combined = append(combined, fmt.Errorf("%s: %w", ie.instance, ie.err))
	}
	return fmt.Errorf("apply failed for %d instances: %w", len(errs), errors.Join(combined...))
}
