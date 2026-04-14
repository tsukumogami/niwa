package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(createCmd)
	createCmd.Flags().StringVar(&createName, "name", "", "custom instance name suffix (e.g., --name=hotfix produces <config>-hotfix)")
	createCmd.Flags().StringVarP(&createRepo, "repo", "r", "", "land in this repo after creation")
}

var (
	createName string
	createRepo string
)

var createCmd = &cobra.Command{
	Use:   "create [workspace-name]",
	Short: "Create a new workspace instance",
	Long: `Create a new workspace instance from a workspace configuration.

Without arguments, discovers .niwa/workspace.toml by walking up from the
current directory. With a workspace name argument, looks it up in the global
registry (~/.config/niwa/config.toml).

Use -r/--repo to land in a specific repo directory after creation, instead
of the instance root.

Instance naming:
  - First instance uses the config name (e.g., "tsuku")
  - Subsequent instances are numbered: tsuku-2, tsuku-3, ...
  - With --name=hotfix, produces: tsuku-hotfix`,
	Args: cobra.MaximumNArgs(1),
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
	var configPath, configDir string

	if len(args) == 1 {
		workspaceName := args[0]
		globalCfg, err := config.LoadGlobalConfig()
		if err != nil {
			return fmt.Errorf("loading global config: %w", err)
		}
		entry := globalCfg.LookupWorkspace(workspaceName)
		if entry == nil {
			var names []string
			for name := range globalCfg.Registry {
				names = append(names, name)
			}
			if len(names) == 0 {
				return fmt.Errorf("workspace %q not found in registry (no workspaces registered)", workspaceName)
			}
			sort.Strings(names)
			return fmt.Errorf("workspace %q not found in registry. Registered workspaces: %s", workspaceName, strings.Join(names, ", "))
		}
		configPath = entry.Source
		configDir = filepath.Dir(configPath)
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		var discoverErr error
		configPath, configDir, discoverErr = config.Discover(cwd)
		if discoverErr != nil {
			return fmt.Errorf("not inside a workspace. Pass a workspace name or run from within a workspace directory")
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

	token := resolveGitHubToken()
	gh := github.NewAPIClient(token)

	applier := workspace.NewApplier(gh)
	instancePath, err := applier.Create(cmd.Context(), cfg, configDir, workspaceRoot)
	if err != nil {
		return err
	}

	landingPath := instancePath
	if createRepo != "" {
		repoDir, err := findRepoDir(instancePath, createRepo)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "instance created at: %s\n", instancePath)
			return fmt.Errorf("repo %q not found in instance: %w", createRepo, err)
		}
		landingPath = repoDir
	}

	if err := validateLandingPath(landingPath); err != nil {
		return err
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "Created instance: %s\n", instancePath)
	if err := writeLandingPath(cmd, landingPath); err != nil {
		return err
	}

	hintShellInit(cmd)

	return nil
}
