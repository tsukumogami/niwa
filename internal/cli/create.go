package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/workspace"
	"golang.org/x/term"
)

func init() {
	rootCmd.AddCommand(createCmd)
	createCmd.Flags().StringVar(&createName, "name", "", "custom instance name suffix (e.g., --name=hotfix produces <config>-hotfix)")
	createCmd.Flags().StringVarP(&createRepo, "repo", "r", "", "land in this repo after creation")
	createCmd.Flags().BoolVar(&createChannels, "channels", false, "enable channel infrastructure for this invocation (overrides NIWA_CHANNELS)")
	createCmd.Flags().BoolVar(&createNoChannels, "no-channels", false, "disable channel infrastructure for this invocation (overrides --channels and NIWA_CHANNELS)")
	createCmd.ValidArgsFunction = completeWorkspaceNames
}

var (
	createName       string
	createRepo       string
	createChannels   bool
	createNoChannels bool
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

	// Subsequent instances: scan from 2 upward for the first directory name
	// that does not exist on disk. Valid instances are silently skipped (their
	// slot is taken). Non-valid directories are warned about and skipped.
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", configName, n)
		candidateDir := filepath.Join(workspaceRoot, candidate)
		if _, err := os.Stat(candidateDir); os.IsNotExist(err) {
			return candidate, nil
		}
		if _, err := workspace.LoadState(candidateDir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %s: directory exists but is not a valid niwa instance\n", candidateDir)
		}
	}
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
			names := globalCfg.RegisteredNames()
			if len(names) == 0 {
				return fmt.Errorf("workspace %q not found in registry (no workspaces registered)", workspaceName)
			}
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

	configName := cfg.Workspace.Name

	instanceName, err := computeInstanceName(configName, createName, workspaceRoot)
	if err != nil {
		return err
	}

	// Check if the computed instance directory already exists.
	instanceDir := filepath.Join(workspaceRoot, instanceName)
	if _, err := os.Stat(instanceDir); err == nil {
		return fmt.Errorf("instance directory already exists: %s", instanceDir)
	}

	token := resolveGitHubToken()
	gh := github.NewAPIClient(token)

	applier := workspace.NewApplier(gh)
	applier.Reporter = workspace.NewReporterWithTTY(os.Stderr, !noProgress && term.IsTerminal(int(os.Stderr.Fd())))

	// Wire up the global config overlay so vault resolution and personal-wins
	// merging work during create. ConfigSourceURL is a fallback for overlay
	// discovery when no init-time state exists (e.g., bare .niwa/ dir).
	// The registry lookup uses configName (not instanceName) so -2, -3, ...
	// instances find the same entry as the first instance.
	if globalCfg, gErr := config.LoadGlobalConfig(); gErr == nil {
		if gDir, gErr := config.GlobalConfigDir(); gErr == nil {
			applier.GlobalConfigDir = gDir
		}
		if entry := globalCfg.LookupWorkspace(configName); entry != nil {
			applier.ConfigSourceURL = entry.SourceURL
		}
	}

	// Resolve effective channel activation, applying priority rules:
	//   1. --no-channels flag → channels disabled
	//   2. --channels flag    → channels enabled
	//   3. [channels.mesh] config section present → channels already enabled (no synthesis needed)
	//   4. NIWA_CHANNELS=1 env var → channels enabled default
	cfg, applier.ChannelsSynthesized = resolveChannelsActivation(cmd, cfg, createChannels, createNoChannels)

	instancePath, err := applier.Create(cmd.Context(), cfg, configDir, workspaceRoot, instanceName)
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

	if err := writeLandingPath(landingPath); err != nil {
		return err
	}

	hintShellInit(cmd)

	return nil
}
