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
	rootCmd.AddCommand(createCmd)
	createCmd.Flags().StringVar(&createName, "name", "", "custom instance name suffix (e.g., --name=hotfix produces <config>-hotfix)")
}

var createName string

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new workspace instance",
	Long: `Create a new workspace instance from the nearest workspace configuration.

Discovers .niwa/workspace.toml by walking up from the current directory, then
creates a new instance directory under the workspace root.

Instance naming:
  - First instance uses the config name (e.g., "tsuku")
  - Subsequent instances are numbered: tsuku-2, tsuku-3, ...
  - With --name=hotfix, produces: tsuku-hotfix`,
	Args: cobra.NoArgs,
	RunE: runCreate,
}

// computeInstanceName determines the instance directory name based on the
// config name, existing instances, and an optional custom name suffix.
func computeInstanceName(configName, customName, workspaceRoot string) (string, error) {
	if customName != "" {
		return configName + "-" + customName, nil
	}

	// First instance: use the config name directly.
	firstDir := filepath.Join(workspaceRoot, configName)
	if _, err := os.Stat(firstDir); os.IsNotExist(err) {
		return configName, nil
	}

	// Subsequent instances: find the next available number.
	nextNum, err := workspace.NextInstanceNumber(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("determining next instance number: %w", err)
	}

	return fmt.Sprintf("%s-%d", configName, nextNum), nil
}

func runCreate(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	configPath, configDir, err := config.Discover(cwd)
	if err != nil {
		return err
	}

	result, err := config.Load(configPath)
	if err != nil {
		return err
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	cfg := result.Config

	workspaceRoot := filepath.Dir(configDir)

	instanceName, err := computeInstanceName(cfg.Workspace.Name, createName, workspaceRoot)
	if err != nil {
		return err
	}

	// Check if the computed instance directory already exists.
	instanceDir := filepath.Join(workspaceRoot, instanceName)
	if _, err := os.Stat(instanceDir); err == nil {
		return fmt.Errorf("instance directory already exists: %s", instanceDir)
	}

	// Set the instance name on the config so Applier.Create uses it for the directory.
	cfg.Workspace.Name = instanceName

	token := os.Getenv("GITHUB_TOKEN")
	gh := github.NewAPIClient(token)

	applier := workspace.NewApplier(gh)
	instancePath, err := applier.Create(cmd.Context(), cfg, configDir, workspaceRoot)
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Created instance: %s\n", instancePath)
	return nil
}
