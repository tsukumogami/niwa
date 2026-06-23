package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// worktreeRefreshFixture builds an instance root with one clone (group "apps",
// repo "app") that holds a materialized env output, a git-initialized worktree
// dir, a repo content source, and an active session pointing at the worktree.
// Returns the Applier (with a buffer-backed reporter), the refresh inputs, the
// clone dir, and the worktree dir.
func worktreeRefreshFixture(t *testing.T, cloneEnv string) (*Applier, worktreeRefreshInputs, string, string, *bytes.Buffer) {
	t.Helper()
	tmpDir := t.TempDir()

	// Config dir with a repo content source so InstallRepoContentTo has input.
	configDir := filepath.Join(tmpDir, "config")
	reposDir := filepath.Join(configDir, "claude", "repos")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reposDir, "app.md"), []byte("# {repo_name}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A workspace env file so repoEnvConfigured treats the repo as having env.
	if err := os.WriteFile(filepath.Join(configDir, "workspace.env"), []byte("FOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "myws", ContentDir: "claude"},
		Env:       config.EnvConfig{Files: []string{"workspace.env"}},
		Claude: config.ClaudeConfig{
			Content: config.ContentConfig{
				Repos: map[string]config.RepoContentEntry{
					"app": {Source: "repos/app.md"},
				},
			},
		},
	}

	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instanceRoot, workspaceContextFile), []byte("# ctx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Clone with a materialized env output at the default *.local* target so
	// inheritEnvOutputs copies it without needing a git tree for the source.
	cloneDir := filepath.Join(instanceRoot, "apps", "app")
	if err := os.MkdirAll(cloneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cloneDir, ".local.env"), []byte(cloneEnv), 0o600); err != nil {
		t.Fatal(err)
	}

	// Worktree dir, git-initialized (the inherit primitive asserts git-exclude for
	// custom names; the default .local.env name is base-pattern matched, but a git
	// tree keeps the worktree realistic and lets gitRegistersWorktree be exercised
	// in tests that opt into the real check).
	worktreePath := filepath.Join(instanceRoot, ".niwa", "worktrees", "app-wt")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, worktreePath)

	// Active session pointing at the worktree.
	sessionsDir := filepath.Join(instanceRoot, StateDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := worktree.NewSessionLifecycleState("aabbccdd", "app", "ship", "", worktreePath, "session/aabbccdd")
	if err := worktree.WriteSessionLifecycleState(sessionsDir, state); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	a := &Applier{Reporter: NewReporterWithTTY(&buf, false)}

	in := worktreeRefreshInputs{
		instanceRoot:  instanceRoot,
		cfg:           cfg,
		configDir:     configDir,
		repoIndex:     map[string]string{"app": cloneDir},
		repoGroups:    map[string]string{"app": "apps"},
		now:           time.Now(),
		gitRegistered: func(_, _ string) bool { return true },
	}
	return a, in, cloneDir, worktreePath, &buf
}

// TestRefreshWorktreeEnvs_RefreshesAfterCloneChange covers R6: a live worktree's
// env output is updated to match the clone after a value change.
func TestRefreshWorktreeEnvs_RefreshesAfterCloneChange(t *testing.T) {
	a, in, _, worktreePath, buf := worktreeRefreshFixture(t, "FOO=newvalue\n")

	managed, err := a.refreshWorktreeEnvs(in)
	if err != nil {
		t.Fatalf("refreshWorktreeEnvs: %v", err)
	}
	a.Reporter.FlushDeferred()

	got, err := os.ReadFile(filepath.Join(worktreePath, ".local.env"))
	if err != nil {
		t.Fatalf("reading worktree env: %v\nreporter:\n%s", err, buf.String())
	}
	if string(got) != "FOO=newvalue\n" {
		t.Errorf("worktree env not refreshed: got %q", got)
	}

	// The refreshed env output must be recorded as a managed file.
	wantPath := filepath.Join(worktreePath, ".local.env")
	found := false
	for _, mf := range managed {
		if mf.Path == wantPath {
			found = true
			if mf.ContentHash == "" {
				t.Errorf("refreshed managed file has empty hash")
			}
		}
	}
	if !found {
		t.Errorf("refreshed worktree env not in managed files: %+v", managed)
	}
}

// TestRefreshWorktreeEnvs_LockedSkippedAndForwardCarried covers R7 + the
// forward-carry invariant: an attached (locked) worktree is skipped with a
// warning naming it, and its prior managed-file entries are carried forward so a
// subsequent cleanRemovedFiles does not delete its live secret file.
func TestRefreshWorktreeEnvs_LockedSkippedAndForwardCarried(t *testing.T) {
	a, in, _, worktreePath, buf := worktreeRefreshFixture(t, "FOO=v2\n")

	// Pre-seed the worktree's env output (as a prior apply would have) and record
	// it in existingState so it is a candidate for forward-carry.
	wtEnv := filepath.Join(worktreePath, ".local.env")
	if err := os.WriteFile(wtEnv, []byte("FOO=v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in.existingState = &InstanceState{ManagedFiles: []ManagedFile{{Path: wtEnv, ContentHash: "old"}}}

	// Lock the worktree: a live attach sentinel owned by this process.
	if err := os.MkdirAll(filepath.Join(worktreePath, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	start, _ := worktree.PIDStartTime(os.Getpid())
	if err := worktree.WriteAttachState(worktreePath, worktree.AttachState{
		V:              1,
		OwnerPID:       os.Getpid(),
		OwnerStartTime: start,
		LockPath:       ".niwa/attach.lock",
	}); err != nil {
		t.Fatal(err)
	}

	managed, err := a.refreshWorktreeEnvs(in)
	if err != nil {
		t.Fatalf("refreshWorktreeEnvs: %v", err)
	}

	// The env must NOT be refreshed (skip gates the write).
	got, _ := os.ReadFile(wtEnv)
	if string(got) != "FOO=v1\n" {
		t.Errorf("locked worktree was refreshed, want skip: got %q", got)
	}

	// A warning naming the worktree must be deferred.
	a.Reporter.FlushDeferred()
	if !bytes.Contains(buf.Bytes(), []byte(worktreePath)) {
		t.Errorf("expected a warning naming the locked worktree; got:\n%s", buf.String())
	}

	// Forward-carry: the prior entry must be in the result so cleanup keeps it.
	carried := false
	for _, mf := range managed {
		if mf.Path == wtEnv {
			carried = true
		}
	}
	if !carried {
		t.Fatalf("locked-but-live worktree entry not forward-carried: %+v", managed)
	}

	// Simulate the next apply's cleanup: the carried entry is in the current
	// result, so cleanRemovedFiles must NOT delete the live secret file.
	result := &pipelineResult{managedFiles: managed}
	a.cleanRemovedFiles(in.existingState, result)
	if _, statErr := os.Stat(wtEnv); statErr != nil {
		t.Errorf("forward-carried secret file was deleted by cleanup: %v", statErr)
	}
}

// TestRefreshWorktreeEnvs_MissingDirPruned covers the absent case: a worktree
// whose dir is gone is skipped with a warning and its entries are NOT
// forward-carried, so the next cleanup prunes them.
func TestRefreshWorktreeEnvs_MissingDirPruned(t *testing.T) {
	a, in, _, worktreePath, buf := worktreeRefreshFixture(t, "FOO=v2\n")

	// Record a prior managed entry under the (about-to-be-removed) worktree.
	wtEnv := filepath.Join(worktreePath, ".local.env")
	in.existingState = &InstanceState{ManagedFiles: []ManagedFile{{Path: wtEnv, ContentHash: "old"}}}

	// Remove the worktree dir to simulate a destroyed worktree.
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatal(err)
	}

	managed, err := a.refreshWorktreeEnvs(in)
	if err != nil {
		t.Fatalf("refreshWorktreeEnvs: %v", err)
	}

	a.Reporter.FlushDeferred()
	if !bytes.Contains(buf.Bytes(), []byte(worktreePath)) {
		t.Errorf("expected a warning naming the missing worktree; got:\n%s", buf.String())
	}

	// The absent worktree's entry must NOT be forward-carried.
	for _, mf := range managed {
		if mf.Path == wtEnv {
			t.Fatalf("absent worktree entry was forward-carried (should be pruned): %+v", managed)
		}
	}
}

// TestRefreshWorktreeEnvs_DetachedSkippedAndForwardCarried covers the detached
// edge: git no longer registers the worktree, so it is skipped with a warning
// and its entries are forward-carried (it is still live on disk).
func TestRefreshWorktreeEnvs_DetachedSkippedAndForwardCarried(t *testing.T) {
	a, in, _, worktreePath, buf := worktreeRefreshFixture(t, "FOO=v2\n")
	in.gitRegistered = func(_, _ string) bool { return false }

	wtEnv := filepath.Join(worktreePath, ".local.env")
	if err := os.WriteFile(wtEnv, []byte("FOO=v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in.existingState = &InstanceState{ManagedFiles: []ManagedFile{{Path: wtEnv, ContentHash: "old"}}}

	managed, err := a.refreshWorktreeEnvs(in)
	if err != nil {
		t.Fatalf("refreshWorktreeEnvs: %v", err)
	}

	got, _ := os.ReadFile(wtEnv)
	if string(got) != "FOO=v1\n" {
		t.Errorf("detached worktree was refreshed, want skip: got %q", got)
	}

	a.Reporter.FlushDeferred()
	if !bytes.Contains(buf.Bytes(), []byte(worktreePath)) {
		t.Errorf("expected a warning naming the detached worktree; got:\n%s", buf.String())
	}

	carried := false
	for _, mf := range managed {
		if mf.Path == wtEnv {
			carried = true
		}
	}
	if !carried {
		t.Errorf("detached-but-live worktree entry not forward-carried: %+v", managed)
	}
}

// TestRefreshWorktreeEnvs_RemovedRepoSilentlySkipped covers the out-of-scope
// case: a session whose repo is not in repoIndex is skipped silently (no warning)
// and its entries drop so cleanup prunes them along with the removed clone.
func TestRefreshWorktreeEnvs_RemovedRepoSilentlySkipped(t *testing.T) {
	a, in, _, worktreePath, buf := worktreeRefreshFixture(t, "FOO=v2\n")
	// Remove the repo from scope.
	delete(in.repoIndex, "app")
	delete(in.repoGroups, "app")

	wtEnv := filepath.Join(worktreePath, ".local.env")
	in.existingState = &InstanceState{ManagedFiles: []ManagedFile{{Path: wtEnv, ContentHash: "old"}}}

	managed, err := a.refreshWorktreeEnvs(in)
	if err != nil {
		t.Fatalf("refreshWorktreeEnvs: %v", err)
	}
	if len(managed) != 0 {
		t.Errorf("removed-repo worktree should contribute no managed files, got %+v", managed)
	}
	a.Reporter.FlushDeferred()
	if bytes.Contains(buf.Bytes(), []byte(worktreePath)) {
		t.Errorf("removed-repo worktree should be skipped silently; got warning:\n%s", buf.String())
	}
}
