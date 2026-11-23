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

// DestroyInstance validates that dir is a proper instance directory directly
// under workspaceRoot, prunes the Claude Code plugin records owned by that
// instance root, withdraws the Claude trust grant its state records, and then
// removes the directory entirely.
//
// dir must be a real directory, never a symlink, and its parent, resolved,
// must be workspaceRoot, resolved: anything else is refused with an error
// and nothing is deleted. A caller passes the root it resolved for the
// command, never one derived from dir.
//
// The prune and the withdrawal run BEFORE os.RemoveAll so both match against
// an instance root that still exists, making ownership unambiguous. Both are
// fail-safe: a missing or malformed registry or config file is reported and
// ignored, never failing the teardown (a file niwa does not own must not block
// a destroy the user asked for).
func DestroyInstance(workspaceRoot, dir string, opts ...DestroyOption) error {
	p, err := PrepareDestroy(workspaceRoot, dir, opts...)
	if err != nil {
		return err
	}
	return p.Remove(dir)
}

// DestroyPlan is a destroy checked and resolved while the instance is still
// at its own path, so the directory removal can run after the instance was
// renamed to a tombstone. The plugin-record prune and the Claude trust
// withdrawal key on the instance's original path: the records and grants
// name it, so on the tombstone's path both would match nothing. Every trust
// key that needs the path to exist is resolved here, before the rename.
type DestroyPlan struct {
	root  string
	dir   string
	info  fs.FileInfo
	o     destroyOptions
	trust trustWithdrawal
}

// PrepareDestroy checks dir as DestroyInstance does (a real directory,
// never a symlink, directly under the resolved workspaceRoot, holding an
// instance and not a workspace root) and resolves the trust keys its
// withdrawal needs. It changes nothing.
func PrepareDestroy(workspaceRoot, dir string, opts ...DestroyOption) (*DestroyPlan, error) {
	info, err := checkInstancePlace(workspaceRoot, dir)
	if err != nil {
		return nil, err
	}
	if err := ValidateInstanceDir(dir); err != nil {
		return nil, err
	}
	var o destroyOptions
	for _, opt := range opts {
		opt(&o)
	}
	return &DestroyPlan{
		root:  workspaceRoot,
		dir:   dir,
		info:  info,
		o:     o,
		trust: resolveClaudeTrust(dir),
	}, nil
}

// Dir is the instance's own path, the one the plan was prepared on.
func (p *DestroyPlan) Dir() string { return p.dir }

// Root is the workspace root the plan checked the instance against.
func (p *DestroyPlan) Root() string { return p.root }

// Remove prunes the plugin records and withdraws the trust grants the plan
// resolved for the instance's original path, then removes the directory at
// at: the instance's own path, or the tombstone it was renamed to. at must
// be a real directory directly under the workspace root, still holding an
// instance, and the same directory the plan checked (the same device and
// inode); anything else is refused and nothing is deleted.
func (p *DestroyPlan) Remove(at string) error {
	if filepath.Clean(at) != filepath.Clean(p.dir) {
		if err := realDirIn(p.root, at); err != nil {
			return fmt.Errorf("refusing to destroy: %w", err)
		}
	}
	info, err := checkInstancePlace(p.root, at)
	if err != nil {
		return err
	}
	if !os.SameFile(info, p.info) {
		return fmt.Errorf("refusing to destroy %s: it is not the directory that was checked", at)
	}
	if err := ValidateInstanceDir(at); err != nil {
		return err
	}

	// Prune the records and withdraw the trust grant this instance owns
	// before removing the directory.
	prunePluginRecords(p.dir, p.o.reporter, p.o.pluginRecordBaseDir)
	p.trust.apply(p.o.reporter)

	if err := os.RemoveAll(at); err != nil {
		return fmt.Errorf("removing instance directory: %w", err)
	}
	return nil
}

// checkInstancePlace refuses a destroy of dir unless dir is a real
// directory, never a symlink, whose parent, resolved, is workspaceRoot,
// resolved. It returns what Lstat saw at dir.
func checkInstancePlace(workspaceRoot, dir string) (fs.FileInfo, error) {
	if workspaceRoot == "" {
		return nil, fmt.Errorf("refusing to destroy %s: no workspace root was given", dir)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("refusing to destroy %s: %w", dir, err)
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, fmt.Errorf("refusing to destroy %s: it is a symlink", dir)
	case !info.IsDir():
		return nil, fmt.Errorf("refusing to destroy %s: it is not a directory", dir)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("refusing to destroy %s: %w", dir, err)
	}
	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("refusing to destroy %s: the workspace root can't be resolved: %w", dir, err)
	}
	if root, err = filepath.Abs(root); err != nil {
		return nil, fmt.Errorf("refusing to destroy %s: %w", dir, err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absDir))
	if err != nil {
		return nil, fmt.Errorf("refusing to destroy %s: its parent can't be resolved: %w", dir, err)
	}
	if filepath.Clean(parent) != filepath.Clean(root) {
		return nil, fmt.Errorf("refusing to destroy %s: it is not directly under the workspace root %s", dir, workspaceRoot)
	}
	return info, nil
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
// is already gone has no expected key, so its entry is left and the warning
// says the directory no longer exists and to remove the entry by hand.
//
// It is fail-safe by contract: a missing state file means there is nothing to
// withdraw, and a state file that can't be read, a root that can't be
// resolved, or a refused or skipped withdrawal is reported (when a reporter
// is set) and otherwise swallowed, so a destroy never fails on trust cleanup.
// A nil reporter drops the warning, as prunePluginRecords does; every CLI
// route passes one.
func withdrawClaudeTrust(instanceDir string, reporter *Reporter) {
	resolveClaudeTrust(instanceDir).apply(reporter)
}

// trustWithdrawal is withdrawClaudeTrust's work with every key resolved
// while the instance is at its own path, so it can be applied after the
// directory was renamed: a key is the path resolved, which needs the path
// to exist.
type trustWithdrawal struct {
	// name is the instance's directory name, for warnings.
	name string
	// stateErr is why the state couldn't be read, when it exists.
	stateErr error
	root     *claudetrust.Grant
	key      string
	keyErr   error
	repos    []repoTrustWithdrawal
}

// repoTrustWithdrawal is one repository directory's recorded grant with
// its key resolved.
type repoTrustWithdrawal struct {
	rel    string
	g      *claudetrust.Grant
	key    string
	keyErr error
}

// resolveClaudeTrust reads instanceDir's state and resolves the key of
// every grant it records. It changes nothing.
func resolveClaudeTrust(instanceDir string) trustWithdrawal {
	t := trustWithdrawal{name: filepath.Base(instanceDir)}
	state, err := LoadState(instanceDir)
	if err != nil {
		// The state is about to be removed with the instance, so a grant it
		// records can't be withdrawn later either.
		if !errors.Is(err, fs.ErrNotExist) {
			t.stateErr = err
		}
		return t
	}
	if state == nil {
		return t
	}
	repos := sortedGrantPaths(state.ClaudeTrustRepos)
	if state.ClaudeTrust == nil && len(repos) == 0 {
		return t
	}
	t.root = state.ClaudeTrust
	t.key, t.keyErr = claudeTrustKey(instanceDir)
	for _, rel := range repos {
		r := repoTrustWithdrawal{rel: rel, g: state.ClaudeTrustRepos[rel]}
		if t.keyErr == nil {
			r.key, r.keyErr = repoTrustKey(instanceDir, t.key, rel)
		}
		t.repos = append(t.repos, r)
	}
	return t
}

// apply withdraws the grants t resolved, warning through reporter for each
// one left in place.
func (t trustWithdrawal) apply(reporter *Reporter) {
	warn := func(format string, a ...any) {
		if reporter != nil {
			reporter.Warn("instance %s: "+format, append([]any{t.name}, a...)...)
		}
	}
	if t.stateErr != nil {
		warn("any Claude trust entry the instance state records was left in place: %v", t.stateErr)
		return
	}
	if g := t.root; g != nil {
		if t.keyErr != nil {
			warn("Claude trust entry %q in %s left in place: the instance root could not be resolved: %v", g.Key, g.ConfigFile, t.keyErr)
		} else if w := claudetrust.Withdraw(g, t.key, claudetrust.Destroy); w != "" {
			warn("%s", w)
		}
	}
	for _, r := range t.repos {
		if t.keyErr != nil {
			warn("Claude trust entry %q in %s left in place: the instance root could not be resolved: %v", r.g.Key, r.g.ConfigFile, t.keyErr)
			continue
		}
		// The same per-directory ladder apply uses (settleRepoGrant), with
		// the key resolved beforehand, so a recorded directory that is gone
		// gets apply's wording rather than a key mismatch.
		var w string
		switch {
		case errors.Is(r.keyErr, errRepoTrustPathRefused):
			w = fmt.Sprintf("Claude trust entry %q in %s left in place: refusing the record for repository %q: %v", r.g.Key, r.g.ConfigFile, r.rel, r.keyErr)
		case r.keyErr == nil && r.key == "":
			w = repoGoneWarning(r.g, r.rel)
		default:
			_, w = settleGrant(r.g, r.key, r.keyErr, claudetrust.Destroy)
		}
		if w != "" {
			warn("%s", w)
		}
	}
}

// claudeTrustKey computes the projects key withdrawClaudeTrust checks a record
// against. It is a variable so a test can make the root unresolvable.
var claudeTrustKey = claudetrust.Key
