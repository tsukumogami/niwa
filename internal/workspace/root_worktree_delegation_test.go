package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

// readSettingsText returns a settings document's text, or "" when it doesn't
// exist.
func readSettingsText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// TestRootSettingsMaterializer_WorktreeDelegation checks that the instance
// root's settings carry the same worktree integration a repo's do: the hooks
// on a supported harness, the deny on an unsupported one, neither when the
// decision is nil.
func TestRootSettingsMaterializer_WorktreeDelegation(t *testing.T) {
	cases := []struct {
		name       string
		delegation *WorktreeDelegation
		wantHook   bool
		wantDeny   bool
	}{
		{"supported", &WorktreeDelegation{Supported: true, NiwaPath: "/abs/niwa"}, true, false},
		{"unsupported", &WorktreeDelegation{Supported: false}, false, true},
		{"nil", nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			configDir := filepath.Join(tmp, ".niwa")
			instanceRoot := filepath.Join(tmp, "instance")
			for _, d := range []string{configDir, instanceRoot} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws"}}
			if _, err := (&RootSettingsMaterializer{}).Materialize(&MaterializeContext{
				Config:             cfg,
				ConfigDir:          configDir,
				RepoDir:            instanceRoot,
				RepoIndex:          map[string]string{},
				WorktreeDelegation: tc.delegation,
			}); err != nil {
				t.Fatalf("RootSettingsMaterializer: %v", err)
			}
			text := readSettingsText(t, filepath.Join(instanceRoot, ".claude", "settings.json"))
			if got := strings.Contains(text, "WorktreeCreate") && strings.Contains(text, "worktree from-hook"); got != tc.wantHook {
				t.Errorf("hook present = %v, want %v:\n%s", got, tc.wantHook, text)
			}
			if got := strings.Contains(text, `"EnterWorktree"`); got != tc.wantDeny {
				t.Errorf("deny present = %v, want %v:\n%s", got, tc.wantDeny, text)
			}
		})
	}
}

// TestMaterializeWorkspaceRoot_WorktreeDelegation checks the workspace root's
// settings: the integration when the caller passes a decision, nothing when it
// passes none.
func TestMaterializeWorkspaceRoot_WorktreeDelegation(t *testing.T) {
	cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws"}}

	_, root := materializeRoot(t, cfg, RootMaterializeOptions{
		WorktreeDelegation: &WorktreeDelegation{Supported: true, NiwaPath: "/abs/niwa"},
	})
	text := readSettingsText(t, filepath.Join(root, rootClaudeDir, rootSettingsFile))
	if !strings.Contains(text, "WorktreeCreate") || !strings.Contains(text, "WorktreeRemove") {
		t.Errorf("workspace root settings lack the worktree hooks:\n%s", text)
	}

	_, root = materializeRoot(t, cfg, RootMaterializeOptions{
		WorktreeDelegation: &WorktreeDelegation{Supported: false},
	})
	text = readSettingsText(t, filepath.Join(root, rootClaudeDir, rootSettingsFile))
	if !strings.Contains(text, `"EnterWorktree"`) || strings.Contains(text, "WorktreeCreate") {
		t.Errorf("workspace root settings should carry the deny and no hook:\n%s", text)
	}

	_, root = materializeRoot(t, cfg, RootMaterializeOptions{})
	text = readSettingsText(t, filepath.Join(root, rootClaudeDir, rootSettingsFile))
	if strings.Contains(text, "WorktreeCreate") || strings.Contains(text, `"EnterWorktree"`) {
		t.Errorf("workspace root settings should carry no worktree integration:\n%s", text)
	}
}

// TestWorktreeDelegationEnabled checks the workspace setting's default.
func TestWorktreeDelegationEnabled(t *testing.T) {
	off, on := false, true
	if !(config.ClaudeConfig{}).WorktreeDelegationEnabled() {
		t.Error("absent worktree_delegation should mean enabled")
	}
	if !(config.ClaudeConfig{WorktreeDelegation: &on}).WorktreeDelegationEnabled() {
		t.Error("worktree_delegation = true should mean enabled")
	}
	if (config.ClaudeConfig{WorktreeDelegation: &off}).WorktreeDelegationEnabled() {
		t.Error("worktree_delegation = false should mean disabled")
	}
}

// createWithHarness runs Create for a one-repo workspace with the given probe
// result (nil lets the Applier probe) and opt-out, and returns the instance
// root's and the repo's settings text.
func createWithHarness(t *testing.T, harness *HarnessSupport, optOut bool) (rootText, repoText string) {
	t.Helper()
	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.public]
visibility = "public"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

	workspaceRoot := tmpDir
	if optOut {
		if err := SaveState(workspaceRoot, &InstanceState{
			SchemaVersion:        SchemaVersion,
			NoWorktreeDelegation: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "app", Visibility: "public", SSHURL: "git@github.com:testorg/app.git"}},
		},
	}
	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.Harness = harness

	instanceRoot := filepath.Join(workspaceRoot, "test-ws")
	repoDir := filepath.Join(instanceRoot, "public", "app")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot, cfg.Workspace.Name); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	return readSettingsText(t, filepath.Join(instanceRoot, ".claude", "settings.json")),
		readSettingsText(t, filepath.Join(repoDir, ".claude", "settings.local.json"))
}

// TestCreate_InstanceRootGetsWorktreeIntegration checks that the instance root
// and the repo make the same hook-or-deny choice from one probe result, that
// an opt-out removes it from both, and that an Applier with no supplied probe
// (the provisioning path) still writes it.
func TestCreate_InstanceRootGetsWorktreeIntegration(t *testing.T) {
	t.Run("supported", func(t *testing.T) {
		rootText, repoText := createWithHarness(t, &HarnessSupport{WorktreeHooks: true}, false)
		for name, text := range map[string]string{"instance root": rootText, "repo": repoText} {
			if !strings.Contains(text, "WorktreeCreate") || strings.Contains(text, `"EnterWorktree"`) {
				t.Errorf("%s settings should carry the hook and no deny:\n%s", name, text)
			}
		}
	})
	t.Run("unsupported", func(t *testing.T) {
		rootText, repoText := createWithHarness(t, &HarnessSupport{WorktreeHooks: false}, false)
		for name, text := range map[string]string{"instance root": rootText, "repo": repoText} {
			if !strings.Contains(text, `"EnterWorktree"`) || strings.Contains(text, "WorktreeCreate") {
				t.Errorf("%s settings should carry the deny and no hook:\n%s", name, text)
			}
		}
	})
	t.Run("opt-out", func(t *testing.T) {
		rootText, repoText := createWithHarness(t, &HarnessSupport{WorktreeHooks: true}, true)
		for name, text := range map[string]string{"instance root": rootText, "repo": repoText} {
			if strings.Contains(text, "WorktreeCreate") || strings.Contains(text, `"EnterWorktree"`) {
				t.Errorf("%s settings should carry no worktree integration:\n%s", name, text)
			}
		}
	})
	t.Run("applier probes when none supplied", func(t *testing.T) {
		// The provisioning path (newProvisionApplier) and create/reset build
		// Appliers with no Harness; the Applier must probe itself.
		probes := 0
		restore := probeHarness
		probeHarness = func(context.Context) HarnessSupport {
			probes++
			return HarnessSupport{WorktreeHooks: true}
		}
		t.Cleanup(func() { probeHarness = restore })

		rootText, repoText := createWithHarness(t, nil, false)
		for name, text := range map[string]string{"instance root": rootText, "repo": repoText} {
			if !strings.Contains(text, "WorktreeCreate") {
				t.Errorf("%s settings should carry the WorktreeCreate hook:\n%s", name, text)
			}
		}
		if probes != 1 {
			t.Errorf("Applier probed %d times, want exactly 1", probes)
		}
	})
}

// TestApply_WorktreeIntegrationIsIdempotent checks that a second apply leaves
// the instance root's and the repo's settings byte-identical.
func TestApply_WorktreeIntegrationIsIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.public]
visibility = "public"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config
	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "app", Visibility: "public", SSHURL: "git@github.com:testorg/app.git"}},
		},
	}
	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.NoPull = true
	applier.Harness = &HarnessSupport{WorktreeHooks: true}

	instanceRoot := filepath.Join(tmpDir, "test-ws")
	repoDir := filepath.Join(instanceRoot, "public", "app")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := applier.Create(context.Background(), cfg, niwaDir, tmpDir, cfg.Workspace.Name); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	rootPath := filepath.Join(instanceRoot, ".claude", "settings.json")
	repoPath := filepath.Join(repoDir, ".claude", "settings.local.json")
	firstRoot, firstRepo := readSettingsText(t, rootPath), readSettingsText(t, repoPath)

	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if got := readSettingsText(t, rootPath); got != firstRoot {
		t.Errorf("instance root settings changed on a second apply:\nbefore:\n%s\nafter:\n%s", firstRoot, got)
	}
	if got := readSettingsText(t, repoPath); got != firstRepo {
		t.Errorf("repo settings changed on a second apply:\nbefore:\n%s\nafter:\n%s", firstRepo, got)
	}
}

// TestMaterializeWorkspaceRoot_SwitchingOffRemovesIntegration checks that a
// workspace root written with the integration loses it when the next write
// carries none, as an apply does after worktree_delegation is turned off.
func TestMaterializeWorkspaceRoot_SwitchingOffRemovesIntegration(t *testing.T) {
	cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws"}}
	root := t.TempDir()
	settingsPath := filepath.Join(root, rootClaudeDir, rootSettingsFile)

	if _, err := MaterializeWorkspaceRoot(cfg, root, RootMaterializeOptions{
		NiwaPath:           "/abs/niwa",
		WorktreeDelegation: &WorktreeDelegation{Supported: true, NiwaPath: "/abs/niwa"},
	}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if !strings.Contains(readSettingsText(t, settingsPath), "WorktreeCreate") {
		t.Fatal("first write should carry the hook")
	}
	if _, err := MaterializeWorkspaceRoot(cfg, root, RootMaterializeOptions{NiwaPath: "/abs/niwa"}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if text := readSettingsText(t, settingsPath); strings.Contains(text, "WorktreeCreate") {
		t.Errorf("integration should be gone after switching off:\n%s", text)
	}
}
