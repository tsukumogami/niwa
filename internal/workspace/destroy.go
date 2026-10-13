package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/claudetrust"
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
	// reporter, when set, receives plugin-record prune output and Claude
	// trust withdrawal warnings. nil is silent.
	reporter *Reporter

	// pluginRecordBaseDir overrides the home directory used to locate Claude
	// Code's plugin registry. Empty means the real ~/.claude. It exists for
	// tests, which point the prune at a t.TempDir.
	pluginRecordBaseDir string
}

// DestroyOption configures a DestroyInstance call.
type DestroyOption func(*destroyOptions)

// WithDestroyReporter routes plugin-record prune output and Claude trust
// withdrawal warnings through reporter so a removal or a skip is never silent.
func WithDestroyReporter(r *Reporter) DestroyOption {
	return func(o *destroyOptions) { o.reporter = r }
}

// WithPluginRecordBaseDir overrides the home directory used to locate the Claude
// Code plugin registry. It exists for tests.
func WithPluginRecordBaseDir(dir string) DestroyOption {
	return func(o *destroyOptions) { o.pluginRecordBaseDir = dir }
}

// DestroyInstance validates that dir is a proper instance directory, prunes the
// Claude Code plugin records owned by that instance root, withdraws the Claude
// trust grant its state records, and then removes the directory entirely.
//
// The prune and the withdrawal run BEFORE os.RemoveAll so both match against
// an instance root that still exists, making ownership unambiguous. Both are
// fail-safe: a missing or malformed registry or config file is reported and
// ignored, never failing the teardown (a file niwa does not own must not block
// a destroy the user asked for).
func DestroyInstance(dir string, opts ...DestroyOption) error {
	if err := ValidateInstanceDir(dir); err != nil {
		return err
	}

	var o destroyOptions
	for _, opt := range opts {
		opt(&o)
	}

	// Prune the records and withdraw the trust grant this instance owns
	// before removing the directory.
	prunePluginRecords(dir, o.reporter, o.pluginRecordBaseDir)
	withdrawClaudeTrust(dir, o.reporter)

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

// withdrawClaudeTrust removes the Claude Code trust entries, or only their
// flags, that instanceDir's state records niwa as having written: the
// instance root's and each repository directory's. Only recorded grants are
// acted on: an entry with no record is the developer's own and is left
// alone. The expected keys are computed here, while the directories still
// exist, so a record naming any other path is refused rather than followed.
// A repository grant's expected key is the resolved path of
// <instanceDir>/<recorded path>; a recorded path that is absolute, contains
// "..", or resolves outside the instance is refused, and one whose directory
// is already gone has no expected key, so Withdraw refuses it with a warning.
//
// It is fail-safe by contract: a missing state file means there is nothing to
// withdraw, and a state file that can't be read, a root that can't be
// resolved, or a refused or skipped withdrawal is reported (when a reporter
// is set) and otherwise swallowed, so a destroy never fails on trust cleanup.
// A nil reporter drops the warning, as prunePluginRecords does; every CLI
// route passes one.
func withdrawClaudeTrust(instanceDir string, reporter *Reporter) {
	warn := func(format string, a ...any) {
		if reporter != nil {
			reporter.Warn("instance %s: "+format, append([]any{filepath.Base(instanceDir)}, a...)...)
		}
	}

	state, err := LoadState(instanceDir)
	if err != nil {
		// The state is about to be removed with the instance, so a grant it
		// records can't be withdrawn later either.
		if !errors.Is(err, fs.ErrNotExist) {
			warn("any Claude trust entry the instance state records was left in place: %v", err)
		}
		return
	}
	if state == nil {
		return
	}
	repos := sortedGrantPaths(state.ClaudeTrustRepos)
	if state.ClaudeTrust == nil && len(repos) == 0 {
		return
	}

	key, keyErr := claudeTrustKey(instanceDir)
	if g := state.ClaudeTrust; g != nil {
		if keyErr != nil {
			warn("Claude trust entry %q in %s left in place: the instance root could not be resolved: %v", g.Key, g.ConfigFile, keyErr)
		} else if w := claudetrust.Withdraw(g, key, claudetrust.Destroy); w != "" {
			warn("%s", w)
		}
	}

	for _, rel := range repos {
		g := state.ClaudeTrustRepos[rel]
		if keyErr != nil {
			warn("Claude trust entry %q in %s left in place: the instance root could not be resolved: %v", g.Key, g.ConfigFile, keyErr)
			continue
		}
		repoKey, err := repoTrustKey(instanceDir, key, rel)
		if err != nil {
			warn("Claude trust entry %q in %s left in place: refusing the record for repository %q: %v", g.Key, g.ConfigFile, rel, err)
			continue
		}
		if w := claudetrust.Withdraw(g, repoKey, claudetrust.Destroy); w != "" {
			warn("%s", w)
		}
	}
}

// claudeTrustKey computes the projects key withdrawClaudeTrust checks a record
// against. It is a variable so a test can make the root unresolvable.
var claudeTrustKey = claudetrust.Key
