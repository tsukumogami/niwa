package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

// readSettingsEnv reads the "env" block from a materialized settings JSON file.
func readSettingsEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return doc.Env
}

// pluginPathEnvWorkspace lays down a minimal workspace whose config declares a
// [[claude.plugin_path_env]] binding, pre-creates the repo clone dir, and
// returns the niwaDir, workspaceRoot, cfg, and the resolved-repo settings path.
func pluginPathEnvWorkspace(t *testing.T) (niwaDir, workspaceRoot string, cfg *config.WorkspaceConfig) {
	t.Helper()
	tmpDir := t.TempDir()
	niwaDir = filepath.Join(tmpDir, ".niwa")
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

[[claude.plugin_path_env]]
name = "SHIRABE_WORK_SUMMARY"
plugin = "work-summary@shirabe"
path = "scripts/render.sh"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "workspace.md"), []byte("# ws\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg = result.Config

	workspaceRoot = tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "test-ws")
	repoDir := filepath.Join(instanceRoot, "public", "repo1")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return niwaDir, workspaceRoot, cfg
}

func pluginPathEnvApplier(installDir string) *Applier {
	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"}},
		},
	}
	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	if installDir != "" {
		applier.pluginInstallPath = func(key string) (string, bool) {
			if pluginKeyMatches("work-summary@shirabe", key) {
				return installDir, true
			}
			return "", false
		}
	} else {
		// Unresolvable: no plugin installed.
		applier.pluginInstallPath = func(string) (string, bool) { return "", false }
	}
	return applier
}

// TestPipelineInjectsPluginPathEnv_Create proves the resolved plugin path is
// injected as SHIRABE_WORK_SUMMARY into BOTH the instance-root settings.json and
// each repo's settings.local.json when provisioning via Create (the path
// niwa create and niwa dispatch share).
func TestPipelineInjectsPluginPathEnv_Create(t *testing.T) {
	niwaDir, workspaceRoot, cfg := pluginPathEnvWorkspace(t)
	installDir := fakePluginDir(t, "scripts/render.sh")
	wantPath := filepath.Join(installDir, "scripts/render.sh")

	applier := pluginPathEnvApplier(installDir)
	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot, cfg.Workspace.Name)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Per-repo settings.local.json.
	repoEnv := readSettingsEnv(t, filepath.Join(instanceRoot, "public", "repo1", ".claude", "settings.local.json"))
	if repoEnv["SHIRABE_WORK_SUMMARY"] != wantPath {
		t.Errorf("repo settings SHIRABE_WORK_SUMMARY = %q, want %q", repoEnv["SHIRABE_WORK_SUMMARY"], wantPath)
	}

	// Instance-root settings.json.
	rootEnv := readSettingsEnv(t, filepath.Join(instanceRoot, ".claude", "settings.json"))
	if rootEnv["SHIRABE_WORK_SUMMARY"] != wantPath {
		t.Errorf("instance-root settings SHIRABE_WORK_SUMMARY = %q, want %q", rootEnv["SHIRABE_WORK_SUMMARY"], wantPath)
	}
}

// TestPipelineInjectsPluginPathEnv_ApplyRefresh proves niwa apply re-resolves the
// path, so a plugin version bump (new install dir) refreshes the injected value.
func TestPipelineInjectsPluginPathEnv_ApplyRefresh(t *testing.T) {
	niwaDir, workspaceRoot, cfg := pluginPathEnvWorkspace(t)

	// First provision at v1.
	installV1 := fakePluginDir(t, "scripts/render.sh")
	applierV1 := pluginPathEnvApplier(installV1)
	instanceRoot, err := applierV1.Create(context.Background(), cfg, niwaDir, workspaceRoot, cfg.Workspace.Name)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	repoSettings := filepath.Join(instanceRoot, "public", "repo1", ".claude", "settings.local.json")
	if got := readSettingsEnv(t, repoSettings)["SHIRABE_WORK_SUMMARY"]; got != filepath.Join(installV1, "scripts/render.sh") {
		t.Fatalf("after create, SHIRABE_WORK_SUMMARY = %q, want v1 path", got)
	}

	// Simulate a plugin version bump: new install dir.
	installV2 := fakePluginDir(t, "scripts/render.sh")
	applierV2 := pluginPathEnvApplier(installV2)
	if err := applierV2.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	wantV2 := filepath.Join(installV2, "scripts/render.sh")
	if got := readSettingsEnv(t, repoSettings)["SHIRABE_WORK_SUMMARY"]; got != wantV2 {
		t.Errorf("after apply, SHIRABE_WORK_SUMMARY = %q, want refreshed v2 path %q", got, wantV2)
	}
}

// TestPipelineInjectsPluginPathEnv_UnresolvableIsAbsent proves the fail-safe:
// when the plugin is not installed, no SHIRABE_WORK_SUMMARY is injected (the
// hook reads an empty value and no-ops) and provisioning still succeeds.
func TestPipelineInjectsPluginPathEnv_UnresolvableIsAbsent(t *testing.T) {
	niwaDir, workspaceRoot, cfg := pluginPathEnvWorkspace(t)

	applier := pluginPathEnvApplier("") // no plugin installed
	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot, cfg.Workspace.Name)
	if err != nil {
		t.Fatalf("create must still succeed when plugin is unresolvable: %v", err)
	}

	repoSettings := filepath.Join(instanceRoot, "public", "repo1", ".claude", "settings.local.json")
	if _, err := os.Stat(repoSettings); err == nil {
		if _, present := readSettingsEnv(t, repoSettings)["SHIRABE_WORK_SUMMARY"]; present {
			t.Error("SHIRABE_WORK_SUMMARY must be absent when the plugin is unresolvable (fail-safe)")
		}
	}
	rootEnv := readSettingsEnv(t, filepath.Join(instanceRoot, ".claude", "settings.json"))
	if _, present := rootEnv["SHIRABE_WORK_SUMMARY"]; present {
		t.Error("instance-root SHIRABE_WORK_SUMMARY must be absent when the plugin is unresolvable (fail-safe)")
	}
}
