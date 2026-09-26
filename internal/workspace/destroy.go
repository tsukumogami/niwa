package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/pluginrecord"
)

// ResolveInstanceTarget determines the absolute path of the target instance
// for a destroy operation.
//
// If nameArg is non-empty, the workspace root is discovered from cwd, all
// instances are enumerated, and the one whose InstanceName matches nameArg is
// returned. If nameArg is empty, the instance containing cwd is returned via
// DiscoverInstance.
func ResolveInstanceTarget(cwd, nameArg string) (string, error) {
	if nameArg != "" {
		return resolveInstanceByName(cwd, nameArg)
	}

	dir, err := DiscoverInstance(cwd)
	if err != nil {
		return "", fmt.Errorf("resolving current instance: %w", err)
	}
	return dir, nil
}

// resolveInstanceByName finds an instance by its InstanceName within the
// workspace discovered from cwd.
func resolveInstanceByName(cwd, name string) (string, error) {
	_, configDir, err := config.Discover(cwd)
	if err != nil {
		return "", fmt.Errorf("finding workspace root: %w", err)
	}

	workspaceRoot := filepath.Dir(configDir)
	instances, err := EnumerateInstances(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("enumerating instances: %w", err)
	}

	for _, dir := range instances {
		state, loadErr := LoadState(dir)
		if loadErr != nil {
			continue
		}
		if state.InstanceName == name {
			return dir, nil
		}
	}

	// Build list of available names for the error message.
	var available []string
	for _, dir := range instances {
		state, loadErr := LoadState(dir)
		if loadErr != nil {
			continue
		}
		available = append(available, state.InstanceName)
	}

	if len(available) == 0 {
		return "", fmt.Errorf("instance %q not found: no instances exist in workspace", name)
	}
	return "", fmt.Errorf("instance %q not found, available instances: %s", name, strings.Join(available, ", "))
}

// ValidateInstanceDir checks that dir is a valid instance directory suitable
// for destruction. It verifies that .niwa/instance.json exists (confirming it
// is an instance) and that .niwa/workspace.toml does NOT exist (confirming it
// is not a workspace root).
func ValidateInstanceDir(dir string) error {
	instancePath := filepath.Join(dir, StateDir, StateFile)
	if _, err := os.Stat(instancePath); err != nil {
		return fmt.Errorf("not an instance directory: %s does not exist", instancePath)
	}

	workspacePath := filepath.Join(dir, config.ConfigDir, config.ConfigFile)
	if _, err := os.Stat(workspacePath); err == nil {
		return fmt.Errorf("refusing to destroy workspace root: %s exists", workspacePath)
	}

	return nil
}

// CheckUncommittedChanges inspects each cloned repo within the instance for
// uncommitted git changes. It loads the instance state, iterates repos where
// Cloned is true, and runs git status --porcelain on each. Repos whose
// directories no longer exist on disk are silently skipped.
//
// Returns the names (map keys) of repos that have uncommitted changes.
func CheckUncommittedChanges(instanceDir string) ([]string, error) {
	state, err := LoadState(instanceDir)
	if err != nil {
		return nil, fmt.Errorf("loading instance state: %w", err)
	}

	var dirty []string
	for name, repo := range state.Repos {
		if !repo.Cloned {
			continue
		}

		repoDir := filepath.Join(instanceDir, name)
		if _, err := os.Stat(repoDir); os.IsNotExist(err) {
			continue
		}

		out, err := exec.Command("git", "-C", repoDir, "status", "--porcelain").Output()
		if err != nil {
			return nil, fmt.Errorf("checking git status for %s: %w", name, err)
		}

		if len(strings.TrimSpace(string(out))) > 0 {
			dirty = append(dirty, name)
		}
	}

	return dirty, nil
}

// destroyOptions holds the resolved configuration for a destroy operation.
type destroyOptions struct {
	// reporter, when set, receives plugin-record prune output. nil is silent.
	reporter *Reporter

	// pluginRecordBaseDir overrides the home directory used to locate Claude
	// Code's plugin registry. Empty means the real ~/.claude. It exists for
	// tests, which point the prune at a t.TempDir.
	pluginRecordBaseDir string
}

// DestroyOption configures a DestroyInstance call.
type DestroyOption func(*destroyOptions)

// WithDestroyReporter routes plugin-record prune output through reporter so a
// removal is never silent.
func WithDestroyReporter(r *Reporter) DestroyOption {
	return func(o *destroyOptions) { o.reporter = r }
}

// WithPluginRecordBaseDir overrides the home directory used to locate the Claude
// Code plugin registry. It exists for tests.
func WithPluginRecordBaseDir(dir string) DestroyOption {
	return func(o *destroyOptions) { o.pluginRecordBaseDir = dir }
}

// DestroyInstance validates that dir is a proper instance directory, prunes the
// Claude Code plugin records owned by that instance root, and then removes the
// directory entirely.
//
// The prune runs BEFORE os.RemoveAll so the instance-owned predicate matches
// against an instance root that still exists, making ownership unambiguous. The
// prune is fail-safe: a missing or malformed registry is reported and ignored,
// never failing the teardown (a registry niwa does not own must not block a
// destroy the user asked for).
func DestroyInstance(dir string, opts ...DestroyOption) error {
	if err := ValidateInstanceDir(dir); err != nil {
		return err
	}

	var o destroyOptions
	for _, opt := range opts {
		opt(&o)
	}

	// Prune the records this instance owns before removing the directory.
	prunePluginRecords(dir, o.reporter, o.pluginRecordBaseDir)

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing instance directory: %w", err)
	}

	return nil
}

// prunePluginRecords removes the Claude Code plugin records whose projectPath is
// owned by instanceRoot, reporting what it removed through reporter. It is
// fail-safe by contract: any error locating, parsing, or rewriting the foreign-
// owned registry is reported (when a reporter is set) and otherwise swallowed,
// so a destroy never fails on registry trouble.
func prunePluginRecords(instanceRoot string, reporter *Reporter, baseDir string) {
	var pruneOpts []pluginrecord.PruneOption
	if baseDir != "" {
		pruneOpts = append(pruneOpts, pluginrecord.WithPruneBaseDir(baseDir))
	}

	report, err := pluginrecord.Prune(pluginrecord.InstanceOwned(instanceRoot), pruneOpts...)
	if err != nil {
		if reporter != nil {
			reporter.Warn("skipping plugin-record cleanup for %s: %v",
				filepath.Base(instanceRoot), err)
		}
		return
	}

	if report.Removed > 0 && reporter != nil {
		reporter.Log("pruned %d plugin record(s) across %d plugin(s) for %s",
			report.Removed, len(report.PerPlugin), filepath.Base(instanceRoot))
	}
}
