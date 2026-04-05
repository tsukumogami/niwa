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
)

func init() {
	rootCmd.AddCommand(applyCmd)
	applyCmd.Flags().StringVar(&applyInstance, "instance", "", "target a specific instance by name")
	applyCmd.Flags().BoolVar(&applyAllowDirty, "allow-dirty", false, "apply even if config directory has uncommitted changes")
	applyCmd.Flags().BoolVar(&applyNoPull, "no-pull", false, "skip pulling latest changes into existing repos")
}

var (
	applyInstance   string
	applyAllowDirty bool
	applyNoPull     bool
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

	// Auto-pull config from origin if it's a git repo with a remote.
	configDir := filepath.Dir(configPath)
	if syncErr := workspace.SyncConfigDir(configDir, applyAllowDirty); syncErr != nil {
		return syncErr
	}

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
	applier.NoPull = applyNoPull
	applier.AllowDirty = applyAllowDirty

	// Wire global config if registered and reachable.
	if globalCfg, gErr := config.LoadGlobalConfig(); gErr == nil && globalCfg.GlobalConfig.Repo != "" {
		if gDir, gErr := config.GlobalConfigDir(); gErr == nil {
			applier.GlobalConfigDir = gDir
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

	return &workspace.ApplyScope{
		Mode:      workspace.ApplyAll,
		Instances: instances,
		Config:    configPath,
	}, nil
}

// updateRegistry updates the global registry with the workspace config path.
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

	globalCfg.SetRegistryEntry(workspaceName, config.RegistryEntry{
		Source: absConfigPath,
		Root:   absRoot,
	})

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
