package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(goCmd)
	goCmd.Flags().StringVarP(&goWorkspace, "workspace", "w", "", "force workspace resolution via registry")
	goCmd.Flags().StringVarP(&goRepo, "repo", "r", "", "target repo in current instance")
	goCmd.ValidArgsFunction = completeGoTarget
	_ = goCmd.RegisterFlagCompletionFunc("workspace", completeWorkspaceNames)
	_ = goCmd.RegisterFlagCompletionFunc("repo", completeRepoNames)
}

var (
	goWorkspace string
	goRepo      string
)

var goCmd = &cobra.Command{
	Use:   "go [target]",
	Short: "Navigate to a workspace or repo",
	Long: `Navigate to a workspace root or repo directory.

Without arguments, navigates to the current workspace root.

With a single argument, uses context-aware resolution:
  - If inside an instance, tries as a repo name first
  - Falls back to workspace name lookup via global registry
  - Use -w to force workspace lookup, -r for explicit repo targeting

Examples:
  niwa go                    # workspace root from cwd
  niwa go tsuku              # repo "tsuku" in current instance, or workspace "tsuku"
  niwa go -w codespar        # workspace "codespar" via registry
  niwa go -r niwa            # repo "niwa" in current instance
  niwa go -w codespar -r api # repo "api" in workspace "codespar"`,
	Args: cobra.MaximumNArgs(1),
	RunE: runGo,
}

func runGo(cmd *cobra.Command, args []string) error {
	if len(args) == 1 && goWorkspace != "" {
		return fmt.Errorf("cannot combine positional argument with -w flag; use one or the other")
	}
	if len(args) == 1 && goRepo != "" {
		return fmt.Errorf("cannot combine positional argument with -r flag; use one or the other")
	}

	var targetPath string
	var err error

	switch {
	case goWorkspace != "" && goRepo != "":
		targetPath, err = resolveWorkspaceRepo(cmd, goWorkspace, goRepo)
	case goWorkspace != "":
		targetPath, err = resolveWorkspaceRoot(cmd, goWorkspace)
	case goRepo != "":
		targetPath, err = resolveCurrentInstanceRepo(cmd, goRepo)
	case len(args) == 1:
		targetPath, err = resolveContextAware(cmd, args[0])
	default:
		targetPath, err = resolveCurrentWorkspaceRoot(cmd)
	}

	if err != nil {
		return err
	}

	if err := validateLandingPath(targetPath); err != nil {
		return err
	}

	if err := writeLandingPath(targetPath); err != nil {
		return err
	}
	hintShellInit(cmd)
	return nil
}

func resolveCurrentWorkspaceRoot(cmd *cobra.Command) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	_, configDir, err := config.Discover(cwd)
	if err != nil {
		return "", formatNotInWorkspaceError()
	}
	workspaceRoot := filepath.Dir(configDir)
	fmt.Fprintf(cmd.ErrOrStderr(), "go: workspace root\n")
	return workspaceRoot, nil
}

func resolveWorkspaceRoot(cmd *cobra.Command, name string) (string, error) {
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return "", fmt.Errorf("loading global config: %w", err)
	}
	entry := globalCfg.LookupWorkspace(name)
	if entry == nil {
		return "", formatWorkspaceNotFoundError(name, globalCfg)
	}
	root := entry.Root
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return "", fmt.Errorf("workspace %q root directory not found: %s\nThe registry entry may be stale. Re-register with: niwa init", name, root)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "go: workspace %q\n", name)
	return root, nil
}

func resolveCurrentInstanceRepo(cmd *cobra.Command, repoName string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	instanceRoot, err := workspace.DiscoverInstance(cwd)
	if err != nil {
		return "", fmt.Errorf("-r requires being inside a workspace instance\nhint: use \"niwa go -w <workspace> -r %s\" to target a specific workspace", repoName)
	}
	repoDir, err := findRepoDir(instanceRoot, repoName)
	if err != nil {
		return "", fmt.Errorf("repo %q not found in current instance: %w\nhint: use \"niwa status\" to list available repos", repoName, err)
	}
	instanceName := filepath.Base(instanceRoot)
	fmt.Fprintf(cmd.ErrOrStderr(), "go: repo %q in %s\n", repoName, instanceName)
	return repoDir, nil
}

func resolveWorkspaceRepo(cmd *cobra.Command, wsName, repoName string) (string, error) {
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return "", fmt.Errorf("loading global config: %w", err)
	}
	entry := globalCfg.LookupWorkspace(wsName)
	if entry == nil {
		return "", formatWorkspaceNotFoundError(wsName, globalCfg)
	}
	root := entry.Root
	instances, err := workspace.EnumerateInstances(root)
	if err != nil {
		return "", fmt.Errorf("enumerating instances in %q: %w", wsName, err)
	}
	if len(instances) == 0 {
		return "", fmt.Errorf("workspace %q has no instances. Create one with: niwa create %s", wsName, wsName)
	}
	sort.Strings(instances)
	instanceRoot := instances[0]

	repoDir, err := findRepoDir(instanceRoot, repoName)
	if err != nil {
		return "", fmt.Errorf("repo %q not found in %s: %w", repoName, filepath.Base(instanceRoot), err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "go: repo %q in %s\n", repoName, filepath.Base(instanceRoot))
	return repoDir, nil
}

func resolveContextAware(cmd *cobra.Command, target string) (string, error) {
	if strings.Contains(target, "/") || strings.Contains(target, "..") {
		return "", fmt.Errorf("invalid target: %q (names cannot contain path separators or traversal components)", target)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Try as repo in current instance.
	instanceRoot, instanceErr := workspace.DiscoverInstance(cwd)
	var repoMatch string
	if instanceErr == nil {
		repoDir, repoErr := findRepoDir(instanceRoot, target)
		if repoErr == nil {
			repoMatch = repoDir
		}
	}

	// Try as workspace in registry.
	globalCfg, _ := config.LoadGlobalConfig()
	var wsMatch string
	if globalCfg != nil {
		if entry := globalCfg.LookupWorkspace(target); entry != nil {
			wsMatch = entry.Root
		}
	}

	switch {
	case repoMatch != "" && wsMatch != "":
		instanceName := filepath.Base(instanceRoot)
		fmt.Fprintf(cmd.ErrOrStderr(), "go: repo %q in %s (also a workspace; use -w to navigate there)\n", target, instanceName)
		return repoMatch, nil
	case repoMatch != "":
		instanceName := filepath.Base(instanceRoot)
		fmt.Fprintf(cmd.ErrOrStderr(), "go: repo %q in %s\n", target, instanceName)
		return repoMatch, nil
	case wsMatch != "":
		if _, err := os.Stat(wsMatch); os.IsNotExist(err) {
			return "", fmt.Errorf("workspace %q root directory not found: %s\nThe registry entry may be stale.", target, wsMatch)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "go: workspace %q\n", target)
		return wsMatch, nil
	default:
		return "", formatTargetNotFoundError(target, instanceErr == nil, globalCfg)
	}
}

func formatNotInWorkspaceError() error {
	globalCfg, _ := config.LoadGlobalConfig()
	if names := globalCfg.RegisteredNames(); len(names) > 0 {
		return fmt.Errorf("not inside a workspace\nhint: use \"niwa go <workspace>\" to navigate to a registered workspace: %s", strings.Join(names, ", "))
	}
	return fmt.Errorf("not inside a workspace (no workspaces registered)")
}

func formatWorkspaceNotFoundError(name string, globalCfg *config.GlobalConfig) error {
	names := globalCfg.RegisteredNames()
	if len(names) == 0 {
		return fmt.Errorf("workspace %q not found (no workspaces registered)", name)
	}
	return fmt.Errorf("workspace %q not found. Registered workspaces: %s", name, strings.Join(names, ", "))
}

func formatTargetNotFoundError(target string, inInstance bool, globalCfg *config.GlobalConfig) error {
	var hints []string
	if inInstance {
		hints = append(hints, "not a repo in the current instance")
	}
	wsNames := globalCfg.RegisteredNames()
	if len(wsNames) > 0 {
		hints = append(hints, fmt.Sprintf("not a registered workspace (registered: %s)", strings.Join(wsNames, ", ")))
	} else {
		hints = append(hints, "no workspaces registered")
	}
	return fmt.Errorf("%q not found: %s", target, strings.Join(hints, "; "))
}
