package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

// TestApplyPrunesStaleManagedHook reproduces the orphaned-mesh-hook
// scenario. Before mesh removal, `niwa apply` synthesized a Stop hook
// (.claude/hooks/stop/report-progress.sh calling `niwa mesh
// report-progress`) and tracked it in InstanceState.ManagedFiles. After
// mesh removal the generator is gone, so a subsequent apply no longer
// produces the hook. This test asserts that such a previously-managed
// hook file is pruned on the next apply once it is no longer produced.
func TestApplyPrunesStaleManagedHook(t *testing.T) {
	tmpDir := t.TempDir()

	niwaDir := filepath.Join(tmpDir, ".niwa")
	contentDir := filepath.Join(niwaDir, "claude")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configTOML := `
[workspace]
name = "test-ws"
content_dir = "claude"

[[sources]]
org = "testorg"

[groups.public]
visibility = "public"

[content.workspace]
source = "workspace.md"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "workspace.md"), []byte("# ws\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A legitimately declared Stop hook in the config source. It must be
	// installed and tracked, and must survive the same apply that prunes the
	// orphan — proving the fix is selective, not a blanket "drop all hooks".
	declaredHookDir := filepath.Join(niwaDir, "hooks", "stop")
	if err := os.MkdirAll(declaredHookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(declaredHookDir, "keep-me.sh"), []byte("#!/bin/sh\necho keep\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"},
			},
		},
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "test-ws")
	repoDir := filepath.Join(instanceRoot, "public", "repo1")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Initial create produces a baseline state we can amend.
	if _, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot, cfg.Workspace.Name); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Simulate the mesh-era leftover: a synthesized Stop hook on disk that
	// the previous (mesh-capable) niwa recorded as a managed file. Current
	// niwa produces no such hook, so apply should prune it.
	staleHook := filepath.Join(instanceRoot, ".claude", "hooks", "stop", "report-progress.sh")
	if err := os.MkdirAll(filepath.Dir(staleHook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleHook, []byte("#!/bin/sh\nniwa mesh report-progress\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Record the hash that matches the on-disk bytes, mirroring how niwa
	// tracked the hook when it wrote it: the live workspace's recorded hash
	// matches its file, so the hook is NOT drifted.
	hookHash, err := HashFile(staleHook)
	if err != nil {
		t.Fatal(err)
	}

	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("loading state: %v", err)
	}
	state.ManagedFiles = append(state.ManagedFiles, ManagedFile{
		Path:        staleHook,
		ContentHash: hookHash,
		Generated:   time.Now(),
	})
	if err := SaveState(instanceRoot, state); err != nil {
		t.Fatalf("saving amended state: %v", err)
	}

	// Re-apply with the same config. The hook is no longer produced.
	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if _, err := os.Stat(staleHook); err == nil {
		t.Error("stale mesh Stop hook should be pruned after apply, but it still exists")
	} else if !os.IsNotExist(err) {
		t.Errorf("unexpected error stating stale hook: %v", err)
	}

	// The declared hook must survive and stay tracked.
	keptHook := filepath.Join(instanceRoot, ".claude", "hooks", "stop", "keep-me.sh")
	if _, err := os.Stat(keptHook); err != nil {
		t.Errorf("declared Stop hook should survive apply: %v", err)
	}

	// The orphan must be dropped from tracked state (no phantom managed file),
	// and the declared hook must remain tracked.
	postState, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("loading post-apply state: %v", err)
	}
	staleTracked, keptTracked := false, false
	for _, mf := range postState.ManagedFiles {
		switch mf.Path {
		case staleHook:
			staleTracked = true
		case keptHook:
			keptTracked = true
		}
	}
	if staleTracked {
		t.Error("stale mesh Stop hook should no longer be tracked in managed files")
	}
	if !keptTracked {
		t.Error("declared Stop hook should remain tracked in managed files")
	}
}
