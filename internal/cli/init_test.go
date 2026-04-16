package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestEmitVaultBootstrapPointer_Infisical confirms the note names
// `infisical login` for an infisical provider. Issue 10 AC: the pointer
// fires when the cloned template declares [vault.provider] or
// [vault.providers.*].
func TestEmitVaultBootstrapPointer_Infisical(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Vault: &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{Kind: "infisical"},
		},
	}

	errBuf := &stringWriter{}
	cmd := initCmd
	cmd.SetErr(errBuf)
	defer cmd.SetErr(os.Stderr)

	emitVaultBootstrapPointer(cmd, cfg)

	got := errBuf.String()
	if !strings.Contains(got, "kind: infisical") {
		t.Errorf("expected note to name the kind, got:\n%s", got)
	}
	if !strings.Contains(got, "infisical login") {
		t.Errorf("expected infisical-specific bootstrap command, got:\n%s", got)
	}
	if !strings.Contains(got, "niwa apply") {
		t.Errorf("expected note to point at next step, got:\n%s", got)
	}
}

// TestEmitVaultBootstrapPointer_UnknownKind falls through to the
// generic "<kind>-specific setup" message. Issue 10 keeps this path
// useful for backends not yet wired into the CLI (sops, future
// providers).
func TestEmitVaultBootstrapPointer_UnknownKind(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Vault: &config.VaultRegistry{
			Providers: map[string]config.VaultProviderConfig{
				"team": {Kind: "sops"},
			},
		},
	}

	errBuf := &stringWriter{}
	cmd := initCmd
	cmd.SetErr(errBuf)
	defer cmd.SetErr(os.Stderr)

	emitVaultBootstrapPointer(cmd, cfg)

	got := errBuf.String()
	if !strings.Contains(got, "kind: sops") {
		t.Errorf("expected generic note for sops, got:\n%s", got)
	}
	if !strings.Contains(got, "sops-specific setup") {
		t.Errorf("expected fallback bootstrap phrase, got:\n%s", got)
	}
}

// TestEmitVaultBootstrapPointer_NoVaultNoOp confirms the pointer is
// a no-op when no [vault.*] is declared. The scaffolded template has
// only commented examples, so new workspaces never see the note
// spuriously.
func TestEmitVaultBootstrapPointer_NoVaultNoOp(t *testing.T) {
	cfg := &config.WorkspaceConfig{}

	errBuf := &stringWriter{}
	cmd := initCmd
	cmd.SetErr(errBuf)
	defer cmd.SetErr(os.Stderr)

	emitVaultBootstrapPointer(cmd, cfg)

	if got := errBuf.String(); got != "" {
		t.Errorf("expected no output without vault config, got:\n%s", got)
	}
}

// TestVaultKindsDeclared_DedupAndSort verifies the helper that drives
// the bootstrap pointer. Order stability is a test-ergonomics
// concern, not a product feature, but it keeps the output stable for
// CI and muscle memory alike.
func TestVaultKindsDeclared_DedupAndSort(t *testing.T) {
	vr := &config.VaultRegistry{
		Provider: &config.VaultProviderConfig{Kind: "infisical"},
		Providers: map[string]config.VaultProviderConfig{
			"team":    {Kind: "sops"},
			"another": {Kind: "infisical"}, // duplicate kind
		},
	}
	got := vaultKindsDeclared(vr)
	if len(got) != 2 {
		t.Fatalf("expected 2 unique kinds, got %v", got)
	}
	if got[0] != "infisical" || got[1] != "sops" {
		t.Errorf("unexpected order: %v", got)
	}
}

// stringWriter is a type alias for strings.Builder, used in these
// tests to capture cobra's stderr output. cmd.SetErr requires an
// io.Writer, which *strings.Builder already satisfies.
type stringWriter = strings.Builder

func TestRunInit_OverlayAndNoOverlayMutuallyExclusive(t *testing.T) {
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

	// Reset flags.
	initOverlay = "acme/my-overlay"
	initNoOverlay = true
	t.Cleanup(func() {
		initOverlay = ""
		initNoOverlay = false
	})

	err = initCmd.RunE(initCmd, []string{})
	if err == nil {
		t.Fatal("expected error when both --overlay and --no-overlay are set, got nil")
	}
}

func TestRunInit_NoOverlayWritesState(t *testing.T) {
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

	// Scaffold first so workspace.toml exists.
	if err := executeInit(t); err != nil {
		t.Fatalf("scaffold init failed: %v", err)
	}

	// Reset flags.
	initNoOverlay = true
	initOverlay = ""
	initSkipGlobal = false
	t.Cleanup(func() {
		initNoOverlay = false
	})

	// Remove the existing .niwa dir so we can reinit — or just call buildInitState directly.
	// Since executeInit will fail (workspace.toml already exists), call buildInitState directly.
	state, err := buildInitState(initCmd, modeScaffold, "")
	if err != nil {
		t.Fatalf("buildInitState returned unexpected error: %v", err)
	}
	if state == nil {
		t.Fatal("buildInitState returned nil with --no-overlay set")
	}
	if !state.NoOverlay {
		t.Error("NoOverlay = false, want true")
	}
	if state.OverlayURL != "" {
		t.Errorf("OverlayURL = %q, want empty for --no-overlay", state.OverlayURL)
	}
}

// gitRunInCLI runs a git command inside dir, failing the test on error.
// This helper mirrors the one in config/overlay_test.go but lives in the cli
// package test so the two packages stay independently testable.
func gitRunInCLI(t *testing.T, dir string, args ...string) {
	t.Helper()
	fullArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", fullArgs...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

// makeLocalGitRepo creates a local git repo in dir with a single empty commit
// so it has a valid HEAD SHA that can be cloned.
func makeLocalGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRunInCLI(t, dir, "init", "--initial-branch=main")
	gitRunInCLI(t, dir, "config", "user.email", "test@test.com")
	gitRunInCLI(t, dir, "config", "user.name", "Test")
	gitRunInCLI(t, dir, "commit", "--allow-empty", "-m", "init")
}

// TestBuildInitState_OverlaySuccessWritesBothFields verifies that when --overlay
// is specified and the clone succeeds, both OverlayURL and OverlayCommit are
// written to the returned state.
func TestBuildInitState_OverlaySuccessWritesBothFields(t *testing.T) {
	tmp := t.TempDir()

	// Set up XDG so OverlayDir resolves inside tmp.
	xdgDir := filepath.Join(tmp, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	// Create a local repo to clone from.
	remoteDir := filepath.Join(tmp, "remote")
	makeLocalGitRepo(t, remoteDir)

	// Point --overlay at the local repo using file:// URL shorthand (absolute path).
	// OverlayDir parses org/repo shorthands — use the absolute path directly since
	// CloneOrSyncOverlay accepts any git-cloneable URL.
	// We need a parseable org/repo for OverlayDir, so we create a fake one in XDG
	// and patch the call by setting initOverlay to an org/repo shorthand while the
	// actual clone is done via a file URL.
	//
	// Simpler approach: call buildInitState with initOverlay set to a valid
	// org/repo shorthand. OverlayDir will resolve a path under XDG. Then the
	// clone from that shorthand will fail because it's not a real repo — but that
	// tests the hard-error path, not the success path.
	//
	// For the success path, we need to arrange that CloneOrSyncOverlay is called
	// with a URL that actually resolves. We can do this by pre-populating the
	// overlay clone target directory with a valid git repo (simulating a
	// previously-cloned overlay) so that CloneOrSyncOverlay takes the pull path.
	// However that still requires the pull to succeed.
	//
	// The simplest end-to-end approach: set initOverlay to an absolute path of the
	// local repo. OverlayDir will fail to parse it (not org/repo). So instead,
	// we directly call config.CloneOrSyncOverlay in a separate temp dir to clone
	// our local repo, then call buildInitState with initOverlay pointing to a
	// file:// URL that parses as a shorthand.
	//
	// Actually, the cleanest approach is to call buildInitState with mocked state
	// globals (initOverlay, etc.) and a real clone URL, which means we need the
	// overlay directory to match what OverlayDir computes for our chosen initOverlay
	// value. Let's use a shorthand "testorg/testoverlay" for initOverlay; OverlayDir
	// will return <xdg>/niwa/overlays/testorg-testoverlay. We pre-clone remoteDir
	// there so CloneOrSyncOverlay finds it and does a pull (firstTime=false).

	// Pre-clone the remote into the path OverlayDir would return for "testorg/testoverlay".
	overlayCloneTarget := filepath.Join(xdgDir, "niwa", "overlays", "testorg-testoverlay")
	if err := os.MkdirAll(filepath.Dir(overlayCloneTarget), 0o755); err != nil {
		t.Fatal(err)
	}
	cloneCmd := exec.Command("git", "clone", remoteDir, overlayCloneTarget)
	cloneCmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		t.Fatalf("pre-clone failed: %v\n%s", err, out)
	}

	// Set globals that buildInitState reads.
	initOverlay = "testorg/testoverlay"
	initNoOverlay = false
	initSkipGlobal = false
	t.Cleanup(func() {
		initOverlay = ""
		initNoOverlay = false
		initSkipGlobal = false
	})

	state, err := buildInitState(initCmd, modeScaffold, "")
	if err != nil {
		t.Fatalf("buildInitState returned unexpected error: %v", err)
	}
	if state == nil {
		t.Fatal("buildInitState returned nil state")
	}
	if state.OverlayURL != "testorg/testoverlay" {
		t.Errorf("OverlayURL = %q, want %q", state.OverlayURL, "testorg/testoverlay")
	}
	if state.OverlayCommit == "" {
		t.Error("OverlayCommit is empty; expected a HEAD SHA to be written")
	}
}

// TestBuildInitState_ConventionDiscoverySilentSkipNoURL verifies that when
// convention discovery (modeClone, no --overlay flag) fails on a first-time
// clone (firstTime=true), OverlayURL is NOT written to state.
func TestBuildInitState_ConventionDiscoverySilentSkipNoURL(t *testing.T) {
	tmp := t.TempDir()

	xdgDir := filepath.Join(tmp, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	// Ensure globals are clear so convention discovery branch is taken.
	initOverlay = ""
	initNoOverlay = false
	// needsState requires at least one flag set; set initSkipGlobal so we enter buildInitState.
	initSkipGlobal = true
	t.Cleanup(func() {
		initOverlay = ""
		initNoOverlay = false
		initSkipGlobal = false
	})

	// Use a source URL whose derived overlay URL will not be cloneable.
	// DeriveOverlayURL("acme/dot-niwa") = "acme/dot-niwa-overlay" which
	// will fail to clone since no such repo exists locally, returning firstTime=true.
	source := "acme/dot-niwa"

	state, err := buildInitState(initCmd, modeClone, source)
	if err != nil {
		t.Fatalf("buildInitState returned unexpected error: %v", err)
	}
	if state == nil {
		t.Fatal("buildInitState returned nil state")
	}
	// On first-time clone failure, OverlayURL must NOT be written.
	if state.OverlayURL != "" {
		t.Errorf("OverlayURL = %q, want empty after silent-skip convention discovery failure", state.OverlayURL)
	}
	if state.OverlayCommit != "" {
		t.Errorf("OverlayCommit = %q, want empty after silent-skip convention discovery failure", state.OverlayCommit)
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
