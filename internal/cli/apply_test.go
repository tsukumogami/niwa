package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func TestCombineInstanceErrors_SingleError(t *testing.T) {
	errs := []instanceError{
		{instance: "/workspace/ws-1", err: errForTest("failed to load state")},
	}

	combined := combineInstanceErrors(errs)
	if combined == nil {
		t.Fatal("expected non-nil error")
	}

	msg := combined.Error()
	if got, want := contains(msg, "ws-1"), true; got != want {
		t.Errorf("error should mention instance: %s", msg)
	}
	if got, want := contains(msg, "failed to load state"), true; got != want {
		t.Errorf("error should mention cause: %s", msg)
	}
}

func TestCombineInstanceErrors_MultipleErrors(t *testing.T) {
	errs := []instanceError{
		{instance: "/workspace/ws-1", err: errForTest("state error")},
		{instance: "/workspace/ws-2", err: errForTest("clone error")},
	}

	combined := combineInstanceErrors(errs)
	if combined == nil {
		t.Fatal("expected non-nil error")
	}

	msg := combined.Error()
	if !contains(msg, "2 instances") {
		t.Errorf("error should mention count: %s", msg)
	}
	if !contains(msg, "ws-1") || !contains(msg, "ws-2") {
		t.Errorf("error should mention both instances: %s", msg)
	}
}

func TestResolveRegistryScope_NotFound(t *testing.T) {
	// Use a temp dir for XDG_CONFIG_HOME so there's no real registry.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := resolveRegistryScope("nonexistent-workspace")
	if err == nil {
		t.Fatal("expected error for non-existent workspace")
	}
	if !contains(err.Error(), "not found in registry") {
		t.Errorf("error should mention registry: %v", err)
	}
}

func TestApplyCmd_HasInstanceFlag(t *testing.T) {
	// Verify the --instance flag is registered on the command.
	flag := applyCmd.Flags().Lookup("instance")
	if flag == nil {
		t.Fatal("expected --instance flag to be registered")
	}
	if flag.DefValue != "" {
		t.Errorf("expected empty default, got %q", flag.DefValue)
	}
}

// TestApplyCmd_HasAllowMissingSecretsFlag verifies the Issue 10 flag
// is registered and defaults to false. The flag is plumbed into
// workspace.Applier.AllowMissingSecrets, which is exercised in
// internal/workspace/apply_vault_test.go; here we only check the CLI
// wiring.
func TestApplyCmd_HasAllowMissingSecretsFlag(t *testing.T) {
	flag := applyCmd.Flags().Lookup("allow-missing-secrets")
	if flag == nil {
		t.Fatal("expected --allow-missing-secrets flag to be registered")
	}
	if flag.DefValue != "false" {
		t.Errorf("expected default false, got %q", flag.DefValue)
	}
}

// TestApplyCmd_HasAllowPlaintextSecretsFlag verifies the Issue 10 flag
// is registered and defaults to false. The flag is plumbed into
// workspace.Applier.AllowPlaintextSecrets, which the guardrail test
// covers; here we only check the CLI wiring.
func TestApplyCmd_HasAllowPlaintextSecretsFlag(t *testing.T) {
	flag := applyCmd.Flags().Lookup("allow-plaintext-secrets")
	if flag == nil {
		t.Fatal("expected --allow-plaintext-secrets flag to be registered")
	}
	if flag.DefValue != "false" {
		t.Errorf("expected default false, got %q", flag.DefValue)
	}
}

// TestApplyCmd_AllowFlagsThreadToApplier verifies that parsing the
// two flags populates the package-level vars that runApply copies
// onto the Applier struct. The full pipeline integration (that the
// Applier then honors these fields) lives in
// internal/workspace/apply_vault_test.go and
// internal/guardrail/githubpublic_test.go; this test pins the CLI
// wiring so a future refactor can't silently drop the plumbing.
func TestApplyCmd_AllowFlagsThreadToApplier(t *testing.T) {
	// Save and restore package-level state so the test is idempotent
	// relative to other tests that inspect applyAllowMissingSecrets.
	savedMissing := applyAllowMissingSecrets
	savedPlain := applyAllowPlaintextSecrets
	t.Cleanup(func() {
		applyAllowMissingSecrets = savedMissing
		applyAllowPlaintextSecrets = savedPlain
	})

	// Reset to false to make sure the ParseFlags call actually sets
	// them; start from true so a no-op parse wouldn't accidentally
	// pass the assertions below.
	applyAllowMissingSecrets = false
	applyAllowPlaintextSecrets = false

	if err := applyCmd.ParseFlags([]string{"--allow-missing-secrets", "--allow-plaintext-secrets"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if !applyAllowMissingSecrets {
		t.Error("expected applyAllowMissingSecrets to be true after --allow-missing-secrets")
	}
	if !applyAllowPlaintextSecrets {
		t.Error("expected applyAllowPlaintextSecrets to be true after --allow-plaintext-secrets")
	}
}

func TestApplyCmd_AcceptsPositionalArg(t *testing.T) {
	// cobra.MaximumNArgs(1) should accept 0 or 1 args.
	if err := applyCmd.Args(applyCmd, []string{}); err != nil {
		t.Errorf("should accept zero args: %v", err)
	}
	if err := applyCmd.Args(applyCmd, []string{"my-workspace"}); err != nil {
		t.Errorf("should accept one arg: %v", err)
	}
	if err := applyCmd.Args(applyCmd, []string{"a", "b"}); err == nil {
		t.Error("should reject two args")
	}
}

func TestApplyModes_Values(t *testing.T) {
	// Verify the mode constants exist and are distinct, to confirm we're
	// using the right types from the workspace package.
	modes := []workspace.ApplyMode{
		workspace.ApplySingle,
		workspace.ApplyAll,
		workspace.ApplyNamed,
	}
	seen := map[workspace.ApplyMode]bool{}
	for _, m := range modes {
		if seen[m] {
			t.Errorf("duplicate ApplyMode value: %d", m)
		}
		seen[m] = true
	}
}

// TestUpdateRegistry_PreservesSourceURL verifies that updateRegistry does not
// overwrite an existing SourceURL in the registry entry. This ensures that the
// original GitHub URL recorded at init time remains available for convention
// overlay discovery across subsequent apply invocations.
func TestUpdateRegistry_PreservesSourceURL(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Seed the registry with an entry that has SourceURL set (as init --from would).
	cfgDir := filepath.Join(tmpDir, "niwa")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	seedCfg := &config.GlobalConfig{}
	seedCfg.SetRegistryEntry("my-ws", config.RegistryEntry{
		Source:    "/old/path/.niwa/workspace.toml",
		Root:      "/old/path",
		SourceURL: "https://github.com/acme/my-ws",
	})
	if err := config.SaveGlobalConfigTo(filepath.Join(cfgDir, "config.toml"), seedCfg); err != nil {
		t.Fatal(err)
	}

	// Create a fake workspace.toml so updateRegistry can resolve an abs path.
	wsDir := filepath.Join(tmpDir, "workspace")
	niwaConfigDir := filepath.Join(wsDir, ".niwa")
	if err := os.MkdirAll(niwaConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(niwaConfigDir, "workspace.toml")
	if err := os.WriteFile(configPath, []byte("[workspace]\nname=\"my-ws\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Call updateRegistry as apply does after each run.
	if err := updateRegistry(configPath, niwaConfigDir, "my-ws"); err != nil {
		t.Fatalf("updateRegistry failed: %v", err)
	}

	// Reload and verify SourceURL is preserved.
	loaded, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatalf("loading global config: %v", err)
	}
	entry := loaded.LookupWorkspace("my-ws")
	if entry == nil {
		t.Fatal("expected registry entry after updateRegistry")
	}
	if entry.SourceURL != "https://github.com/acme/my-ws" {
		t.Errorf("SourceURL = %q, want %q", entry.SourceURL, "https://github.com/acme/my-ws")
	}
	// Source should now be the local config path.
	absConfigPath, _ := filepath.Abs(configPath)
	if entry.Source != absConfigPath {
		t.Errorf("Source = %q, want %q", entry.Source, absConfigPath)
	}
}

// errForTest is a simple error type for test assertions.
type testErr string

func errForTest(msg string) error { return testErr(msg) }
func (e testErr) Error() string   { return string(e) }

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
