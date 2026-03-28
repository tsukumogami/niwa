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
	Use:   "apply",
	Short: "Apply workspace configuration",
	Long: `Apply discovers .niwa/workspace.toml by walking up from the current
directory, queries GitHub for repos in each source org, classifies them
into groups, clones missing repos, and installs CLAUDE.md content files.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}

		configPath, configDir, err := config.Discover(cwd)
		if err != nil {
			return err
		}

		cfg, err := config.Load(configPath)
		if err != nil {
			return err
		}

		// Workspace root is the parent of the config directory.
		workspaceRoot := filepath.Dir(configDir)

		token := os.Getenv("GITHUB_TOKEN")
		gh := github.NewAPIClient(token)

		applier := workspace.NewApplier(gh)
		return applier.Apply(cmd.Context(), cfg, configDir, workspaceRoot)
	},
}
