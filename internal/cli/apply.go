package cli

import (
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
}

var applyCmd = &cobra.Command{
	Use:   "apply [workspace-name]",
	Short: "Apply workspace configuration",
	Long: `Apply discovers .niwa/workspace.toml by walking up from the current
directory, queries GitHub for repos in each source org, classifies them
into groups, clones missing repos, and installs CLAUDE.md content files.

If a workspace name is given, it is resolved through the global registry
(~/.config/niwa/config.toml) to find the workspace root directory.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var configPath, configDir string

		if len(args) == 1 {
			// Resolve workspace name through the global registry.
			globalCfg, err := config.LoadGlobalConfig()
			if err != nil {
				return fmt.Errorf("loading global config: %w", err)
			}

			entry := globalCfg.LookupWorkspace(args[0])
			if entry == nil {
				return fmt.Errorf("workspace %q not found in registry", args[0])
			}

			configPath = entry.Source
			configDir = filepath.Dir(configPath)
		} else {
			// Discover from current directory.
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting working directory: %w", err)
			}

			var discoverErr error
			configPath, configDir, discoverErr = config.Discover(cwd)
			if discoverErr != nil {
				return discoverErr
			}
		}

		result, err := config.Load(configPath)
		if err != nil {
			return err
		}
		for _, w := range result.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
		cfg := result.Config

		// Workspace root is the parent of the config directory.
		workspaceRoot := filepath.Dir(configDir)

		token := os.Getenv("GITHUB_TOKEN")
		gh := github.NewAPIClient(token)

		applier := workspace.NewApplier(gh)
		if err := applier.Apply(cmd.Context(), cfg, configDir, workspaceRoot); err != nil {
			return err
		}

		// Update the global registry after successful apply.
		globalCfg, err := config.LoadGlobalConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not load global config for registry update: %v\n", err)
			return nil
		}

		absConfigPath, err := filepath.Abs(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not resolve config path: %v\n", err)
			return nil
		}
		absRoot, err := filepath.Abs(workspaceRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not resolve workspace root: %v\n", err)
			return nil
		}

		globalCfg.SetRegistryEntry(cfg.Workspace.Name, config.RegistryEntry{
			Source: absConfigPath,
			Root:   absRoot,
		})

		if err := config.SaveGlobalConfig(globalCfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not update registry: %v\n", err)
		}

		return nil
	},
}
