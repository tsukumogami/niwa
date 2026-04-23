package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	configCmd.AddCommand(configSetCmd)
	configSetCmd.AddCommand(configSetGlobalCmd)
}

var configSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set a configuration value",
}

var configSetGlobalCmd = &cobra.Command{
	Use:   "global <repo>",
	Short: "Register a global config repo",
	Long: `Register a GitHub-backed repo as the global config source.

The repo is cloned to $XDG_CONFIG_HOME/niwa/global and its configuration
is applied as an overlay on top of every workspace config during apply.

Re-running this command replaces any existing registration and re-clones
from the new URL.

<repo> may be an "org/repo" shorthand or a full clone URL.`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigSetGlobal,
}

func runConfigSetGlobal(cmd *cobra.Command, args []string) error {
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("loading global config: %w", err)
	}

	repo := args[0]
	src, err := workspace.ParseSourceURL(repo)
	if err != nil {
		return fmt.Errorf("parsing global config source %q: %w", repo, err)
	}
	cloneURL, err := workspace.ResolveCloneURL(repo, globalCfg.CloneProtocol())
	if err != nil {
		return fmt.Errorf("resolving clone URL: %w", err)
	}

	globalConfigDir, err := config.GlobalConfigDir()
	if err != nil {
		return fmt.Errorf("determining global config directory: %w", err)
	}

	// Remove existing snapshot if present so we re-materialize cleanly.
	if _, statErr := os.Stat(globalConfigDir); statErr == nil {
		if err := os.RemoveAll(globalConfigDir); err != nil {
			return fmt.Errorf("removing existing global config snapshot: %w", err)
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Cloning global config from: %s\n", cloneURL)

	fetcher := github.NewAPIClient(resolveGitHubToken())
	if err := workspace.MaterializeFromSource(cmd.Context(), src, globalConfigDir, fetcher, workspace.NewReporter(os.Stderr)); err != nil {
		return fmt.Errorf("materializing global config repo: %w", err)
	}

	globalCfg.GlobalConfig = config.GlobalConfigSource{Repo: repo}

	cfgPath, err := config.GlobalConfigPath()
	if err != nil {
		return fmt.Errorf("determining global config path: %w", err)
	}
	if err := config.SaveGlobalConfigTo(cfgPath, globalCfg); err != nil {
		return fmt.Errorf("saving global config: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Global config registered: %s\n", repo)
	return nil
}
