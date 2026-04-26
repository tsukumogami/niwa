package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	applyCmd.Flags().BoolVar(&applyForce, "force", false,
		"force apply through a detected URL change against a legacy working tree (PRD R26-R27).")
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
	applyForce                 bool
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

	// PRD R26-R27: detect URL change against a legacy working tree
	// before any sync happens. When the registry's source URL differs
	// from what the on-disk dir is tracking, refuse without --force.
	if changeErr := checkConfigSourceURLChange(configDir, cfg, applyForce); changeErr != nil {
		return changeErr
	}

	token := resolveGitHubToken()
	gh := github.NewAPIClient(token)
	applier := workspace.NewApplier(gh)
	applier.Reporter = workspace.NewReporterWithTTY(os.Stderr, !noProgress && term.IsTerminal(int(os.Stderr.Fd())))
	applier.NoPull = applyNoPull
	applier.AllowDirty = applyAllowDirty
	if applyAllowDirty {
		// PRD R32: --allow-dirty is meaningless under the snapshot
		// model and slated for removal in v1.1. Print the deprecation
		// notice once per process invocation.
		fmt.Fprintln(os.Stderr, "warning: --allow-dirty is no longer meaningful under the snapshot model and will be removed in v1.1")
	}
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

// checkConfigSourceURLChange implements PRD R26-R27: refuse apply
// when the registered source URL differs from what the on-disk
// `<workspace>/.niwa/` is tracking, unless --force was passed.
//
// "On-disk URL" comes from one of two places:
//   - Provenance marker (.niwa-snapshot.toml): the snapshot model's
//     source-identity surface.
//   - Legacy git remote (`git remote get-url origin`): when the dir
//     is still a working tree from the pre-snapshot model.
//
// When the marker is absent and the dir has no git remote, this
// function returns nil (no comparison possible — local-only workspace
// or freshly-scaffolded init).
//
// When --force is passed, the function returns nil regardless. The
// caller's downstream `EnsureConfigSnapshot` / `SyncConfigDir` then
// re-materializes from the registered URL.
//
// Validates that the new source's `[workspace].name` matches the
// registered name when --force is in effect, per PRD R27 (workspace-
// name-mismatch refusal). When the new cfg has no workspace name set,
// the check is skipped (the existing config-load path validates name
// independently).
func checkConfigSourceURLChange(configDir string, cfg *config.WorkspaceConfig, force bool) error {
	registeredURL := registeredSourceURLForConfigDir(configDir)
	if registeredURL == "" {
		return nil
	}

	onDiskURL := onDiskSourceURL(configDir)
	if onDiskURL == "" {
		// No comparison possible. New workspace or local-only.
		return nil
	}

	if normalizeSourceURL(onDiskURL) == normalizeSourceURL(registeredURL) {
		return nil
	}

	if !force {
		return fmt.Errorf(`workspace config source changed
  was:  %s
  now:  %s
  The current %s on disk is from the old source. Replacing it will
  discard any uncommitted edits inside.
To proceed:
  1. cd %s && git status   # check for uncommitted work (legacy working tree)
  2. niwa apply --force     # discard and re-materialize from the new source`,
			onDiskURL, registeredURL, configDir, configDir)
	}

	// --force is set. PRD R27: validate workspace-name match.
	registeredName := registeredWorkspaceNameForConfigDir(configDir)
	if registeredName != "" && cfg != nil && cfg.Workspace.Name != "" && cfg.Workspace.Name != registeredName {
		return fmt.Errorf(
			"workspace name mismatch: registered as %q but new source's workspace.toml declares %q. Use a separate `niwa init` for the new workspace, or align the names.",
			registeredName, cfg.Workspace.Name)
	}
	return nil
}

// registeredSourceURLForConfigDir looks up the workspace registered
// for configDir's parent and returns its SourceURL, or "" if not
// registered.
func registeredSourceURLForConfigDir(configDir string) string {
	root := filepath.Dir(configDir)
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return ""
	}
	for _, name := range globalCfg.RegisteredNames() {
		entry := globalCfg.LookupWorkspace(name)
		if entry == nil {
			continue
		}
		if entry.Root == root {
			return entry.SourceURL
		}
	}
	return ""
}

// registeredWorkspaceNameForConfigDir returns the registered workspace
// name for configDir's parent, or "" if not found.
func registeredWorkspaceNameForConfigDir(configDir string) string {
	root := filepath.Dir(configDir)
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return ""
	}
	for _, name := range globalCfg.RegisteredNames() {
		entry := globalCfg.LookupWorkspace(name)
		if entry == nil {
			continue
		}
		if entry.Root == root {
			return name
		}
	}
	return ""
}

// onDiskSourceURL returns the source URL recorded for the snapshot
// or working tree at configDir. Provenance marker takes precedence
// over the legacy git remote.
func onDiskSourceURL(configDir string) string {
	if data, err := os.ReadFile(filepath.Join(configDir, ".niwa-snapshot.toml")); err == nil {
		// Cheap parse for source_url only.
		for _, line := range splitLines(string(data)) {
			if eq := indexEqual(line); eq > 0 {
				key := trimToken(line[:eq])
				val := trimQuoted(line[eq+1:])
				if key == "source_url" {
					return val
				}
			}
		}
	}
	if _, err := os.Stat(filepath.Join(configDir, ".git")); err == nil {
		if out, err := runGitOrigin(configDir); err == nil {
			return out
		}
	}
	return ""
}

// normalizeSourceURL strips trailing .git, normalizes ssh-vs-https
// for github.com, and lowercases for case-insensitive comparison.
// Used by checkConfigSourceURLChange to avoid false positives when
// the registered slug is "org/repo" but the on-disk remote is
// "git@github.com:org/repo.git".
func normalizeSourceURL(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".git")
	if strings.HasPrefix(s, "git@github.com:") {
		s = "github.com/" + strings.TrimPrefix(s, "git@github.com:")
	}
	if strings.HasPrefix(s, "https://github.com/") {
		s = "github.com/" + strings.TrimPrefix(s, "https://github.com/")
	}
	if strings.HasPrefix(s, "github.com/") {
		s = strings.TrimPrefix(s, "github.com/")
	}
	return strings.ToLower(s)
}

// helpers (small enough to keep local; not exporting).

func splitLines(s string) []string { return strings.Split(s, "\n") }

func indexEqual(s string) int {
	return strings.IndexByte(s, '=')
}

func trimToken(s string) string {
	return strings.TrimSpace(s)
}

func trimQuoted(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func runGitOrigin(dir string) (string, error) {
	out, err := execCommand("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// execCommand is a small indirection so tests could substitute, though
// no test currently does. Using exec.Command directly is fine; keep
// this aliased for symmetry with the wider workspace package's pattern.
var execCommand = exec.Command
