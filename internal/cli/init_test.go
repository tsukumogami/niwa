package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func TestResolveInitMode_NoArgs(t *testing.T) {
	globalCfg := &config.GlobalConfig{}
	mode, name, source := resolveInitMode(nil, "", globalCfg)

	if mode != modeScaffold {
		t.Errorf("expected modeScaffold, got %d", mode)
	}
	if name != "" {
		t.Errorf("expected empty name, got %q", name)
	}
	if source != "" {
		t.Errorf("expected empty source, got %q", source)
	}
}

func TestResolveInitMode_NamedUnregistered(t *testing.T) {
	globalCfg := &config.GlobalConfig{}
	mode, name, source := resolveInitMode([]string{"my-project"}, "", globalCfg)

	if mode != modeNamed {
		t.Errorf("expected modeNamed, got %d", mode)
	}
	if name != "my-project" {
		t.Errorf("expected name %q, got %q", "my-project", name)
	}
	if source != "" {
		t.Errorf("expected empty source, got %q", source)
	}
}

func TestResolveInitMode_NamedRegisteredWithSource(t *testing.T) {
	globalCfg := &config.GlobalConfig{}
	globalCfg.SetRegistryEntry("my-project", config.RegistryEntry{
		Source: "my-org/my-config",
		Root:   "/some/path",
	})

	mode, name, source := resolveInitMode([]string{"my-project"}, "", globalCfg)

	if mode != modeClone {
		t.Errorf("expected modeClone, got %d", mode)
	}
	if name != "my-project" {
		t.Errorf("expected name %q, got %q", "my-project", name)
	}
	if source != "my-org/my-config" {
		t.Errorf("expected source %q, got %q", "my-org/my-config", source)
	}
}

func TestResolveInitMode_NamedRegisteredWithoutSource(t *testing.T) {
	globalCfg := &config.GlobalConfig{}
	globalCfg.SetRegistryEntry("my-project", config.RegistryEntry{
		Root: "/some/path",
	})

	mode, name, source := resolveInitMode([]string{"my-project"}, "", globalCfg)

	if mode != modeNamed {
		t.Errorf("expected modeNamed, got %d", mode)
	}
	if name != "my-project" {
		t.Errorf("expected name %q, got %q", "my-project", name)
	}
	if source != "" {
		t.Errorf("expected empty source, got %q", source)
	}
}

func TestResolveInitMode_FromFlag(t *testing.T) {
	globalCfg := &config.GlobalConfig{}
	mode, name, source := resolveInitMode([]string{"my-project"}, "my-org/my-config", globalCfg)

	if mode != modeClone {
		t.Errorf("expected modeClone, got %d", mode)
	}
	if name != "my-project" {
		t.Errorf("expected name %q, got %q", "my-project", name)
	}
	if source != "my-org/my-config" {
		t.Errorf("expected source %q, got %q", "my-org/my-config", source)
	}
}

func TestResolveInitMode_FromFlagOverridesRegistry(t *testing.T) {
	globalCfg := &config.GlobalConfig{}
	globalCfg.SetRegistryEntry("my-project", config.RegistryEntry{
		Source: "old-org/old-config",
		Root:   "/some/path",
	})

	mode, name, source := resolveInitMode([]string{"my-project"}, "new-org/new-config", globalCfg)

	if mode != modeClone {
		t.Errorf("expected modeClone, got %d", mode)
	}
	if source != "new-org/new-config" {
		t.Errorf("expected --from to override registry, got %q", source)
	}
	_ = name
}

// executeInit runs the init command with the given args by calling RunE directly.
// This avoids cobra's Execute() which routes through the root command.
func executeInit(t *testing.T, args ...string) error {
	t.Helper()
	cmd := initCmd
	cmd.SetArgs(args)
	// Reset the --from flag before each test.
	initFrom = ""
	if err := cmd.ParseFlags(args); err != nil {
		return err
	}
	return cmd.RunE(cmd, cmd.Flags().Args())
}

func TestRunInit_ScaffoldMode(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))

	if err := executeInit(t); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// Verify workspace.toml was created.
	configPath := filepath.Join(dir, workspace.StateDir, workspace.WorkspaceConfigFile)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("workspace.toml not created: %v", err)
	}

	// Verify it parses with default name.
	result, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("workspace.toml doesn't parse: %v", err)
	}
	if result.Config.Workspace.Name != "workspace" {
		t.Errorf("expected default name %q, got %q", "workspace", result.Config.Workspace.Name)
	}

	// Verify no registry entry was created (detached mode).
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatalf("loading global config: %v", err)
	}
	if entry := globalCfg.LookupWorkspace("workspace"); entry != nil {
		t.Error("expected no registry entry for detached workspace")
	}
}

func TestRunInit_NamedMode(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	xdgDir := filepath.Join(dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	if err := executeInit(t, "my-project"); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// Verify workspace.toml was created with the given name.
	configPath := filepath.Join(dir, workspace.StateDir, workspace.WorkspaceConfigFile)
	result, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("workspace.toml doesn't parse: %v", err)
	}
	if result.Config.Workspace.Name != "my-project" {
		t.Errorf("expected name %q, got %q", "my-project", result.Config.Workspace.Name)
	}

	// Verify registry was updated.
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatalf("loading global config: %v", err)
	}
	entry := globalCfg.LookupWorkspace("my-project")
	if entry == nil {
		t.Fatal("expected registry entry for my-project")
	}
}

func TestRunInit_ConflictExistingWorkspace(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))

	// Create an existing workspace.
	niwaDir := filepath.Join(dir, workspace.StateDir)
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, workspace.WorkspaceConfigFile), []byte("[workspace]\nname = \"existing\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err = executeInit(t)
	if err == nil {
		t.Fatal("expected error for existing workspace, got nil")
	}
}

func TestRunInit_ConflictOrphanedNiwaDir(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))

	// Create an orphaned .niwa/ directory.
	niwaDir := filepath.Join(dir, workspace.StateDir)
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	err = executeInit(t)
	if err == nil {
		t.Fatal("expected error for orphaned .niwa/ directory, got nil")
	}
}
