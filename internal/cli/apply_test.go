package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
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

// TestApplyCmd_HasNoCascadeFlag verifies the --no-cascade flag is registered
// and defaults to false (so apply cascades into the subtree by default).
func TestApplyCmd_HasNoCascadeFlag(t *testing.T) {
	flag := applyCmd.Flags().Lookup("no-cascade")
	if flag == nil {
		t.Fatal("expected --no-cascade flag to be registered")
	}
	if flag.DefValue != "false" {
		t.Errorf("expected default false, got %q", flag.DefValue)
	}
}

// TestApplyCmd_NoCascadeFlagParses verifies parsing --no-cascade sets the
// package-level var runApply reads to cap the operation at the current scope.
func TestApplyCmd_NoCascadeFlagParses(t *testing.T) {
	saved := applyNoCascade
	t.Cleanup(func() { applyNoCascade = saved })

	applyNoCascade = false
	if err := applyCmd.ParseFlags([]string{"--no-cascade"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if !applyNoCascade {
		t.Error("expected applyNoCascade to be true after --no-cascade")
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
		workspace.ApplyWorktree,
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

// setupRootScopeFixture builds a hermetic workspace-root fixture with one
// child instance and returns the workspace root and the instance directory.
// The root carries .niwa/workspace.toml (so cwd resolves to ApplyAll) and the
// instance carries .niwa/instance.json (so it is enumerated as a cascade
// target). No git remotes are configured, so the apply pipeline does no
// network I/O — the cascade converges the repo-less instance entirely from
// local state.
func setupRootScopeFixture(t *testing.T) (workspaceRoot, instanceDir string) {
	t.Helper()
	workspaceRoot = t.TempDir()

	niwaDir := filepath.Join(workspaceRoot, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"),
		[]byte("[workspace]\nname = \"cascade-ws\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	instanceDir = filepath.Join(workspaceRoot, "inst-1")
	st := &workspace.InstanceState{
		SchemaVersion: 1,
		InstanceName:  "inst-1",
		Root:          instanceDir,
	}
	if err := workspace.SaveState(instanceDir, st); err != nil {
		t.Fatal(err)
	}
	return workspaceRoot, instanceDir
}

// runApplyAtRoot drives runApply with cwd at the given workspace root and the
// given --no-cascade setting, restoring both the cwd and the package-level flag
// afterward. It returns the runApply error so callers can assert on it.
func runApplyAtRoot(t *testing.T, workspaceRoot string, noCascade bool) error {
	t.Helper()

	// Hermetic registry: updateRegistry writes into XDG_CONFIG_HOME.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	savedWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(savedWD) })
	if err := os.Chdir(workspaceRoot); err != nil {
		t.Fatal(err)
	}

	savedCascade := applyNoCascade
	t.Cleanup(func() { applyNoCascade = savedCascade })
	applyNoCascade = noCascade

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	return runApply(cmd, nil)
}

// TestRunApply_RootScopeCascadesIntoInstances drives runApply at ROOT scope
// (cwd at the workspace root, an ApplyAll fixture with one instance) and
// asserts the context-aware apply wiring: a normal root apply materializes the
// root-managed config (root .claude/settings.json) AND proceeds into the
// instance cascade. The cascade is observed via a sentinel on the instance's
// state file: a converged instance has its LastApplied stamped and managed
// files recorded. This exercises behavior, not flag parsing — it confirms the
// instance loop actually runs Applier.Apply.
func TestRunApply_RootScopeCascadesIntoInstances(t *testing.T) {
	root, instanceDir := setupRootScopeFixture(t)

	if err := runApplyAtRoot(t, root, false); err != nil {
		t.Fatalf("runApply (cascade) returned error: %v", err)
	}

	// Root-managed config materialized.
	rootSettings := filepath.Join(root, ".claude", "settings.json")
	if _, err := os.Stat(rootSettings); err != nil {
		t.Errorf("expected root-managed settings at %s: %v", rootSettings, err)
	}

	// Sentinel: the instance was converged by the cascade. A fresh instance
	// fixture has a zero LastApplied and no managed files; Applier.Apply
	// stamps LastApplied and records managed files when it runs.
	st, err := workspace.LoadState(instanceDir)
	if err != nil {
		t.Fatalf("loading instance state after cascade: %v", err)
	}
	if st.LastApplied.IsZero() {
		t.Error("expected instance LastApplied to be stamped after cascade, got zero")
	}
	if len(st.ManagedFiles) == 0 {
		t.Error("expected instance to have managed files after cascade, got none")
	}
}

// TestRunApply_RootScopeNoCascadeSkipsInstances drives runApply at ROOT scope
// with --no-cascade and asserts the short-circuit behavior: the root-managed
// config is still materialized (root .claude/settings.json), but the operation
// does NOT re-converge the instances beneath it. The same instance-state
// sentinel proves the cascade was skipped: the instance's state file is
// untouched (zero LastApplied, no managed files), which can only hold if
// Applier.Apply was never entered for it.
//
// This is the real --no-cascade guarantee under the #168 inherit model:
// --no-cascade caps the operation at the root scope. It is not flag-parse
// wiring — it observes that the instance loop did not run.
func TestRunApply_RootScopeNoCascadeSkipsInstances(t *testing.T) {
	root, instanceDir := setupRootScopeFixture(t)

	if err := runApplyAtRoot(t, root, true); err != nil {
		t.Fatalf("runApply (--no-cascade) returned error: %v", err)
	}

	// Root-managed config is still materialized: --no-cascade at the root
	// refreshes the root-managed config, it does not suppress it.
	rootSettings := filepath.Join(root, ".claude", "settings.json")
	if _, err := os.Stat(rootSettings); err != nil {
		t.Errorf("expected root-managed settings at %s even with --no-cascade: %v", rootSettings, err)
	}

	// Sentinel: the instance was NOT converged. Its state file is exactly as
	// the fixture wrote it — zero LastApplied, no managed files — proving the
	// instance cascade was short-circuited.
	st, err := workspace.LoadState(instanceDir)
	if err != nil {
		t.Fatalf("loading instance state after --no-cascade: %v", err)
	}
	if !st.LastApplied.IsZero() {
		t.Errorf("expected instance LastApplied to stay zero with --no-cascade, got %v", st.LastApplied)
	}
	if len(st.ManagedFiles) != 0 {
		t.Errorf("expected instance to have no managed files with --no-cascade, got %d", len(st.ManagedFiles))
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
