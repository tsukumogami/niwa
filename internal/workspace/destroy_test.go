package workspace

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/pluginrecord"
)

// destroySetupWorkspace creates a workspace root with .niwa/workspace.toml and
// returns the root directory path.
func destroySetupWorkspace(t *testing.T) string {
	t.Helper()
	return setupWorkspace(t, nil)
}

// destroySetupInstance creates an instance directory under root with the given
// name and returns its path.
func destroySetupInstance(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   name,
		InstanceNumber: 1,
		Root:           dir,
		Created:        time.Now(),
		LastApplied:    time.Now(),
		Repos:          map[string]RepoState{},
	}
	if err := SaveState(dir, state); err != nil {
		t.Fatal(err)
	}

	return dir
}

// --- ResolveInstanceTarget ---

func TestResolveInstanceTarget_ByName(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst1 := destroySetupInstance(t, root, "alpha")
	destroySetupInstance(t, root, "beta")

	// Resolve by name from the workspace root.
	got, err := ResolveInstanceTarget(root, "alpha")
	if err != nil {
		t.Fatalf("ResolveInstanceTarget: %v", err)
	}
	if got != inst1 {
		t.Errorf("got %q, want %q", got, inst1)
	}
}

func TestResolveInstanceTarget_ByNameNotFound(t *testing.T) {
	root := destroySetupWorkspace(t)
	destroySetupInstance(t, root, "alpha")

	_, err := ResolveInstanceTarget(root, "nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent instance name")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention the requested name: %v", err)
	}
}

func TestResolveInstanceTarget_ByNameNoInstances(t *testing.T) {
	root := destroySetupWorkspace(t)

	_, err := ResolveInstanceTarget(root, "anything")
	if err == nil {
		t.Fatal("expected error when no instances exist")
	}
	if !strings.Contains(err.Error(), "no instances exist") {
		t.Errorf("error should say no instances exist: %v", err)
	}
}

func TestResolveInstanceTarget_ByCwd(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "gamma")

	// Create a nested directory inside the instance.
	nested := filepath.Join(inst, "deep", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveInstanceTarget(nested, "")
	if err != nil {
		t.Fatalf("ResolveInstanceTarget: %v", err)
	}
	if got != inst {
		t.Errorf("got %q, want %q", got, inst)
	}
}

func TestResolveInstanceTarget_ByCwdNotInInstance(t *testing.T) {
	dir := t.TempDir()

	_, err := ResolveInstanceTarget(dir, "")
	if err == nil {
		t.Fatal("expected error when cwd is not in an instance")
	}
}

// --- ValidateInstanceDir ---

func TestValidateInstanceDir_Valid(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "valid-instance")

	if err := ValidateInstanceDir(inst); err != nil {
		t.Errorf("ValidateInstanceDir should pass for a valid instance: %v", err)
	}
}

func TestValidateInstanceDir_NotAnInstance(t *testing.T) {
	dir := t.TempDir()

	err := ValidateInstanceDir(dir)
	if err == nil {
		t.Fatal("expected error for directory without instance.json")
	}
	if !strings.Contains(err.Error(), "not an instance") {
		t.Errorf("error should mention 'not an instance': %v", err)
	}
}

func TestValidateInstanceDir_IsWorkspaceRoot(t *testing.T) {
	// Create a directory that has both instance.json AND workspace.toml.
	dir := t.TempDir()
	niwaDir := filepath.Join(dir, StateDir)
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write instance.json.
	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "root",
		InstanceNumber: 1,
		Root:           dir,
		Created:        time.Now(),
		LastApplied:    time.Now(),
		Repos:          map[string]RepoState{},
	}
	if err := SaveState(dir, state); err != nil {
		t.Fatal(err)
	}

	// Write workspace.toml.
	if err := os.WriteFile(filepath.Join(niwaDir, config.ConfigFile), []byte("[workspace]\nname = \"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ValidateInstanceDir(dir)
	if err == nil {
		t.Fatal("expected error when directory is a workspace root")
	}
	if !strings.Contains(err.Error(), "workspace root") {
		t.Errorf("error should mention 'workspace root': %v", err)
	}
}

// --- CheckUncommittedChanges ---

func TestCheckUncommittedChanges_CleanRepos(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "clean-instance")

	// Create a git repo inside the instance.
	repoDir := filepath.Join(inst, "myrepo")
	initGitRepo(t, repoDir)

	// Update instance state to know about the repo.
	state, err := LoadState(inst)
	if err != nil {
		t.Fatal(err)
	}
	state.Repos = map[string]RepoState{
		"myrepo": {URL: "git@github.com:org/myrepo.git", Cloned: true},
	}
	if err := SaveState(inst, state); err != nil {
		t.Fatal(err)
	}

	dirty, err := CheckUncommittedChanges(inst)
	if err != nil {
		t.Fatalf("CheckUncommittedChanges: %v", err)
	}
	if len(dirty) != 0 {
		t.Errorf("expected no dirty repos, got %v", dirty)
	}
}

func TestCheckUncommittedChanges_DirtyRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "dirty-instance")

	repoDir := filepath.Join(inst, "myrepo")
	initGitRepo(t, repoDir)

	// Create an uncommitted file.
	if err := os.WriteFile(filepath.Join(repoDir, "uncommitted.txt"), []byte("changes"), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := LoadState(inst)
	if err != nil {
		t.Fatal(err)
	}
	state.Repos = map[string]RepoState{
		"myrepo": {URL: "git@github.com:org/myrepo.git", Cloned: true},
	}
	if err := SaveState(inst, state); err != nil {
		t.Fatal(err)
	}

	dirty, err := CheckUncommittedChanges(inst)
	if err != nil {
		t.Fatalf("CheckUncommittedChanges: %v", err)
	}
	if len(dirty) != 1 || dirty[0] != "myrepo" {
		t.Errorf("expected [myrepo], got %v", dirty)
	}
}

func TestCheckUncommittedChanges_SkipsNotCloned(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "skip-instance")

	state, err := LoadState(inst)
	if err != nil {
		t.Fatal(err)
	}
	state.Repos = map[string]RepoState{
		"not-cloned": {URL: "git@github.com:org/repo.git", Cloned: false},
	}
	if err := SaveState(inst, state); err != nil {
		t.Fatal(err)
	}

	dirty, err := CheckUncommittedChanges(inst)
	if err != nil {
		t.Fatalf("CheckUncommittedChanges: %v", err)
	}
	if len(dirty) != 0 {
		t.Errorf("expected no dirty repos for uncloned, got %v", dirty)
	}
}

func TestCheckUncommittedChanges_SkipsMissingDir(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "missing-dir-instance")

	state, err := LoadState(inst)
	if err != nil {
		t.Fatal(err)
	}
	state.Repos = map[string]RepoState{
		"gone": {URL: "git@github.com:org/gone.git", Cloned: true},
	}
	if err := SaveState(inst, state); err != nil {
		t.Fatal(err)
	}

	dirty, err := CheckUncommittedChanges(inst)
	if err != nil {
		t.Fatalf("CheckUncommittedChanges: %v", err)
	}
	if len(dirty) != 0 {
		t.Errorf("expected no dirty repos for missing dir, got %v", dirty)
	}
}

func TestCheckUncommittedChanges_MultipleRepos(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "multi-instance")

	// Create two repos: one clean, one dirty.
	cleanDir := filepath.Join(inst, "clean")
	initGitRepo(t, cleanDir)

	dirtyDir := filepath.Join(inst, "dirty")
	initGitRepo(t, dirtyDir)
	if err := os.WriteFile(filepath.Join(dirtyDir, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := LoadState(inst)
	if err != nil {
		t.Fatal(err)
	}
	state.Repos = map[string]RepoState{
		"clean": {URL: "git@github.com:org/clean.git", Cloned: true},
		"dirty": {URL: "git@github.com:org/dirty.git", Cloned: true},
	}
	if err := SaveState(inst, state); err != nil {
		t.Fatal(err)
	}

	dirtyRepos, err := CheckUncommittedChanges(inst)
	if err != nil {
		t.Fatalf("CheckUncommittedChanges: %v", err)
	}

	sort.Strings(dirtyRepos)
	if len(dirtyRepos) != 1 || dirtyRepos[0] != "dirty" {
		t.Errorf("expected [dirty], got %v", dirtyRepos)
	}
}

// --- DestroyInstance ---

func TestDestroyInstance_Success(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "doomed")

	// Add some content to make the removal meaningful.
	if err := os.WriteFile(filepath.Join(inst, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := DestroyInstance(inst); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}

	if _, err := os.Stat(inst); !os.IsNotExist(err) {
		t.Errorf("instance directory should be removed, got err: %v", err)
	}
}

func TestDestroyInstance_RejectsNonInstance(t *testing.T) {
	dir := t.TempDir()

	err := DestroyInstance(dir)
	if err == nil {
		t.Fatal("expected error when destroying non-instance directory")
	}
}

func TestDestroyInstance_RejectsWorkspaceRoot(t *testing.T) {
	root := destroySetupWorkspace(t)

	// Also make it look like an instance by adding instance.json.
	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "root",
		InstanceNumber: 1,
		Root:           root,
		Created:        time.Now(),
		LastApplied:    time.Now(),
		Repos:          map[string]RepoState{},
	}
	if err := SaveState(root, state); err != nil {
		t.Fatal(err)
	}

	err := DestroyInstance(root)
	if err == nil {
		t.Fatal("expected error when destroying workspace root")
	}
	if !strings.Contains(err.Error(), "workspace root") {
		t.Errorf("error should mention workspace root: %v", err)
	}

	// Directory should still exist.
	if _, err := os.Stat(root); err != nil {
		t.Errorf("workspace root should not be removed: %v", err)
	}
}

// --- DestroyInstance plugin-record pruning ---

// registryBaseDir returns a fresh temp directory standing in for the user's
// home, where pluginrecord locates ~/.claude/plugins/installed_plugins.json.
func registryBaseDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// registryPath returns the on-disk registry path under a base (home) dir.
func registryPath(base string) string {
	return filepath.Join(base, ".claude", "plugins", "installed_plugins.json")
}

// seedRegistry writes a registry under base whose single plugin key holds one
// record per supplied projectPath. The records carry an installPath that exists
// (the base dir) so the dangling predicate would not match — only ownership
// distinguishes them, which is what these tests exercise.
func seedRegistry(t *testing.T, base string, projectPaths ...string) {
	t.Helper()

	type record struct {
		Scope       string `json:"scope"`
		ProjectPath string `json:"projectPath"`
		InstallPath string `json:"installPath"`
		Version     string `json:"version"`
	}
	records := make([]record, 0, len(projectPaths))
	for _, p := range projectPaths {
		records = append(records, record{
			Scope:       "project",
			ProjectPath: p,
			InstallPath: base, // an existing dir, so not dangling
			Version:     "1.0.0",
		})
	}

	doc := map[string]any{
		"version": "1",
		"plugins": map[string]any{
			"example@market": records,
		},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	path := registryPath(base)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// loadRegistryProjectPaths returns the projectPath of every record remaining in
// the registry under base, sorted, for assertions.
func loadRegistryProjectPaths(t *testing.T, base string) []string {
	t.Helper()

	reg, err := pluginrecord.Load(pluginrecord.WithBaseDir(base))
	if err != nil {
		t.Fatalf("loading registry: %v", err)
	}
	var paths []string
	for _, records := range reg.Plugins {
		for _, rec := range records {
			paths = append(paths, rec.ProjectPath)
		}
	}
	sort.Strings(paths)
	return paths
}

// TestDestroyInstance_PrunesOwnedRecords destroys one of two sibling instances
// whose directory names share a textual prefix and asserts the destroyed
// instance's records are removed while the sibling's are left intact. This
// exercises the instance-owned predicate's prefix precision.
func TestDestroyInstance_PrunesOwnedRecords(t *testing.T) {
	root := destroySetupWorkspace(t)
	target := destroySetupInstance(t, root, "alpha")
	sibling := destroySetupInstance(t, root, "alpha-sibling")

	base := registryBaseDir(t)
	// Records under the target (and a nested repo of it) plus the sibling.
	seedRegistry(t, base,
		target,
		filepath.Join(target, "repo"),
		sibling,
		filepath.Join(sibling, "repo"),
	)

	if err := DestroyInstance(target, WithPluginRecordBaseDir(base)); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("instance directory should be removed, got err: %v", err)
	}

	got := loadRegistryProjectPaths(t, base)
	want := []string{sibling, filepath.Join(sibling, "repo")}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("remaining records = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("remaining records = %v, want %v", got, want)
		}
	}
}

// TestDestroyInstance_FailSafeAbsentRegistry asserts destroy succeeds when no
// registry file exists at all (the common case on machines without Claude Code
// plugins installed).
func TestDestroyInstance_FailSafeAbsentRegistry(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "doomed")

	base := registryBaseDir(t) // no registry seeded

	if err := DestroyInstance(inst, WithPluginRecordBaseDir(base)); err != nil {
		t.Fatalf("DestroyInstance should succeed with absent registry: %v", err)
	}
	if _, err := os.Stat(inst); !os.IsNotExist(err) {
		t.Errorf("instance directory should be removed, got err: %v", err)
	}
}

// TestDestroyInstance_FailSafeMalformedRegistry asserts destroy succeeds when
// the registry is present but unparseable, and the malformed file is left
// untouched (niwa never overwrites a foreign file it cannot parse).
func TestDestroyInstance_FailSafeMalformedRegistry(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "doomed")

	base := registryBaseDir(t)
	path := registryPath(base)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	garbage := []byte("{ this is not valid json")
	if err := os.WriteFile(path, garbage, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := DestroyInstance(inst, WithPluginRecordBaseDir(base)); err != nil {
		t.Fatalf("DestroyInstance should succeed with malformed registry: %v", err)
	}
	if _, err := os.Stat(inst); !os.IsNotExist(err) {
		t.Errorf("instance directory should be removed, got err: %v", err)
	}

	// The malformed file must be left exactly as it was.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry after destroy: %v", err)
	}
	if string(after) != string(garbage) {
		t.Errorf("malformed registry was modified: got %q, want %q", after, garbage)
	}
}

// TestDestroyWorkspace_PrunesEachInstance asserts the workspace-wipe path prunes
// records for every instance root it removes.
func TestDestroyWorkspace_PrunesEachInstance(t *testing.T) {
	root := destroySetupWorkspace(t)
	a := destroySetupInstance(t, root, "alpha")
	b := destroySetupInstance(t, root, "beta")

	base := registryBaseDir(t)
	seedRegistry(t, base, a, b)

	if err := DestroyWorkspace(root, DestroyWorkspaceOpts{PluginRecordBaseDir: base}); err != nil {
		t.Fatalf("DestroyWorkspace: %v", err)
	}

	got := loadRegistryProjectPaths(t, base)
	if len(got) != 0 {
		t.Errorf("expected all records pruned, got %v", got)
	}
}

// initGitRepo creates a git repository at dir with an initial commit.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmds := [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
		{"git", "-C", dir, "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}
}
