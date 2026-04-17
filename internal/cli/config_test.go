package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// executeConfigUnsetGlobal runs the config unset global command.
func executeConfigUnsetGlobal(t *testing.T) error {
	t.Helper()
	cmd := configUnsetGlobalCmd
	cmd.SetArgs(nil)
	return cmd.RunE(cmd, nil)
}

func TestRunConfigSetGlobal_StoresRepo(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Test registration storage by directly using config.SaveGlobalConfigTo.
	// The full clone path is tested via integration tests.
	globalCfg, _ := config.LoadGlobalConfig()
	globalCfg.GlobalConfig = config.GlobalConfigSource{Repo: "myorg/my-config"}

	cfgPath, _ := config.GlobalConfigPath()
	if err := config.SaveGlobalConfigTo(cfgPath, globalCfg); err != nil {
		t.Fatalf("save error: %v", err)
	}

	loaded, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if loaded.GlobalConfig.Repo != "myorg/my-config" {
		t.Errorf("GlobalConfig.Repo = %q, want myorg/my-config", loaded.GlobalConfig.Repo)
	}
}

func TestRunConfigUnsetGlobal_ClearsRegistration(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Set up a global config registration.
	globalCfg, _ := config.LoadGlobalConfig()
	globalCfg.GlobalConfig = config.GlobalConfigSource{Repo: "myorg/my-config"}

	cfgPath, _ := config.GlobalConfigPath()
	if err := config.SaveGlobalConfigTo(cfgPath, globalCfg); err != nil {
		t.Fatalf("pre-setup save error: %v", err)
	}

	// Create a fake global config directory to verify removal.
	globalDir := filepath.Join(tmpDir, "niwa", "global")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "niwa.toml"), []byte("[global]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := executeConfigUnsetGlobal(t); err != nil {
		t.Fatalf("unset error: %v", err)
	}

	// Verify repo cleared in config.
	loaded, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatalf("load after unset error: %v", err)
	}
	if loaded.GlobalConfig.Repo != "" {
		t.Errorf("GlobalConfig.Repo = %q after unset, want empty", loaded.GlobalConfig.Repo)
	}

	// Verify clone directory removed.
	if _, err := os.Stat(globalDir); !os.IsNotExist(err) {
		t.Error("global config clone directory should have been removed")
	}
}

func TestRunConfigUnsetGlobal_Noop(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// No global config registered -- should return without error.
	if err := executeConfigUnsetGlobal(t); err != nil {
		t.Fatalf("unset with no registration should not error: %v", err)
	}
}

func TestRunInit_SkipGlobal(t *testing.T) {
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

	// Reset flag before test.
	initSkipGlobal = false
	if err := executeInit(t, "--skip-global"); err != nil {
		t.Fatalf("init --skip-global failed: %v", err)
	}

	state, err := workspace.LoadState(dir)
	if err != nil {
		t.Fatalf("loading instance state: %v", err)
	}
	if !state.SkipGlobal {
		t.Error("SkipGlobal should be true after niwa init --skip-global")
	}
}
