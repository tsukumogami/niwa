package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(resetCmd)
	resetCmd.Flags().BoolVar(&resetForce, "force", false, "skip uncommitted changes check")
	resetCmd.ValidArgsFunction = completeInstanceNames
}

var resetForce bool

var resetCmd = &cobra.Command{
	Use:   "reset [instance]",
	Short: "Reset a workspace instance",
	Long: `Reset destroys and recreates a workspace instance from its configuration.

If no instance name is given, the current directory is used to discover the
enclosing instance. The workspace configuration must come from a remote source
(cloned config repo). Local-only workspaces cannot be reset because the
configuration would be lost; use destroy + init instead.

By default, reset refuses to proceed if any cloned repository has uncommitted
changes. Use --force to skip this check.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runReset,
}

func runReset(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	var nameArg string
	if len(args) > 0 {
		nameArg = args[0]
	}

	instanceDir, err := workspace.ResolveInstanceTarget(cwd, nameArg)
	if err != nil {
		return err
	}

	if err := workspace.ValidateInstanceDir(instanceDir); err != nil {
		return err
	}

	if !resetForce {
		dirty, err := workspace.CheckUncommittedChanges(instanceDir)
		if err != nil {
			return fmt.Errorf("checking for uncommitted changes: %w", err)
		}
		if len(dirty) > 0 {
			sort.Strings(dirty)
			fmt.Fprintf(cmd.ErrOrStderr(), "Repos with uncommitted changes:\n")
			for _, name := range dirty {
				fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", name)
			}
			return fmt.Errorf("instance has uncommitted changes in %d repo(s); use --force to override", len(dirty))
		}
	}

	// Load state before destroying to capture the instance name.
	state, err := workspace.LoadState(instanceDir)
	if err != nil {
		return fmt.Errorf("loading instance state: %w", err)
	}

	// Determine the workspace root (parent of instance directories).
	workspaceRoot := filepath.Dir(instanceDir)

	// Check that the config comes from a remote source (cloned config repo).
	configDir := filepath.Join(workspaceRoot, config.ConfigDir)
	if !isClonedConfig(configDir) {
		return fmt.Errorf("cannot reset a local-only workspace; the config would be lost. Use destroy + init instead.")
	}

	configPath := filepath.Join(configDir, config.ConfigFile)
	result, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading workspace config: %w", err)
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	cfg := result.Config

	// Destroy the instance.
	if err := workspace.DestroyInstance(instanceDir); err != nil {
		return err
	}

	// Re-create using the same instance name.
	cfg.Workspace.Name = state.InstanceName

	token := resolveGitHubToken()
	gh := github.NewAPIClient(token)

	applier := workspace.NewApplier(gh)
	instancePath, err := applier.Create(cmd.Context(), cfg, configDir, workspaceRoot)
	if err != nil {
		return fmt.Errorf("recreating instance: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Reset instance: %s\n", instancePath)
	return nil
}

// isClonedConfig checks whether the config directory is a cloned git
// repository, indicating the config came from a remote source.
func isClonedConfig(configDir string) bool {
	gitDir := filepath.Join(configDir, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return false
	}
	return info.IsDir()
}
