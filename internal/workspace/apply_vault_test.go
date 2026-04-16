package workspace

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/fake"
)

// withFakeVaultBackend registers fake.NewFactory() on
// vault.DefaultRegistry for the duration of the test and ensures it is
// unregistered on cleanup. The apply pipeline consults DefaultRegistry
// via resolve.BuildBundle, so this registration is how tests let the
// production code path resolve vault:// URIs against a test fixture
// without hard-coding a separate registry.
//
// The factory is unregistered in t.Cleanup even if the test fails, so
// parallel or subsequent tests see a clean DefaultRegistry.
func withFakeVaultBackend(t *testing.T) {
	t.Helper()
	factory := fake.NewFactory()
	if err := vault.DefaultRegistry.Register(factory); err != nil {
		t.Fatalf("registering fake backend: %v", err)
	}
	t.Cleanup(func() {
		if err := vault.DefaultRegistry.Unregister(factory.Kind()); err != nil {
			t.Errorf("unregistering fake backend: %v", err)
		}
	})
}

// TestApplyResolvesVaultSecretEndToEnd is the Issue 4 integration
// test: parse → resolve → merge → materialize with a vault://-backed
// secret. It exercises apply.go's new wiring from end to end against
// the fake backend and asserts that the resolved secret reaches the
// materializer via the MaybeSecret.Secret branch.
//
// What this test locks in:
//
//   - apply.go calls resolve.ResolveWorkspace BEFORE merge.
//   - The team bundle resolves a vault:// ref declared in a team
//     [env.secrets] key.
//   - The materialized env file contains the resolved plaintext.
//   - Parsing workspaces with [vault.provider] declarations succeeds.
//
// This test is minimal on purpose: broader coverage of the
// MaybeSecret data model lives in internal/config tests, of the
// resolver proper in internal/vault/resolve tests, and of merge
// semantics in override_test.go.
func TestApplyResolvesVaultSecretEndToEnd(t *testing.T) {
	withFakeVaultBackend(t)

	// Build a fixture with a fake vault provider and an env.secrets
	// vault:// ref.
	configTOML := `
[workspace]
name = "vault-it-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]

[vault.provider]
kind = "fake"

[vault.provider.values]
API_TOKEN = "resolved-token-value-xxxxx"

[env.secrets]
API_TOKEN = "vault://API_TOKEN"
`

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading workspace config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "app", SSHURL: "git@github.com:testorg/app.git"},
			},
		},
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "vault-it-ws")

	// Pre-create the repo dir with a .git marker so the Cloner skips
	// it. This mirrors setupTestWorkspace's convention for integration
	// tests that don't exercise the Cloner.
	groupDir := filepath.Join(instanceRoot, "default")
	if err := os.MkdirAll(filepath.Join(groupDir, "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(groupDir, "app", ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	if _, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The env materializer writes .local.env in the repo dir.
	envPath := filepath.Join(instanceRoot, "default", "app", ".local.env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("reading materialized env file: %v", err)
	}
	content := string(data)
	// After Issue 6 hardening, the materializer uses
	// reveal.UnsafeReveal to extract plaintext from resolved
	// MaybeSecret values, so the plaintext from the fake provider
	// lands in the file verbatim. The integration-level invariants
	// are:
	//   - The resolved plaintext bytes reach the file.
	//   - The redacted "***" placeholder does not.
	//   - The literal "vault://" URI does not.
	want := "API_TOKEN=resolved-token-value-xxxxx"
	if !strings.Contains(content, want) {
		t.Errorf("env file missing %q, got:\n%s", want, content)
	}
	if strings.Contains(content, "***") {
		t.Errorf("env file must not contain redacted placeholder: %s", content)
	}
	if strings.Contains(content, "vault://API_TOKEN") {
		t.Errorf("env file must not contain the unresolved vault URI: %s", content)
	}

	// Issue 6 also enforces 0o600 on every materialized file.
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("stat env file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("env file mode = %o, want 0o600", got)
	}
}

// TestApplyVaultProviderMissingKeyErrors confirms the apply pipeline
// surfaces resolver errors (a missing vault key without
// ?required=false and without AllowMissingSecrets).
func TestApplyVaultProviderMissingKeyErrors(t *testing.T) {
	withFakeVaultBackend(t)

	configTOML := `
[workspace]
name = "vault-miss-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]

[vault.provider]
kind = "fake"

[env.secrets]
MISSING = "vault://MISSING"
`

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading workspace config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}}},
	}

	workspaceRoot := tmpDir
	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	_, err = applier.Create(context.Background(), cfg, niwaDir, workspaceRoot)
	if err == nil {
		t.Fatal("expected error for missing vault key")
	}
	if !strings.Contains(err.Error(), "MISSING") {
		t.Errorf("expected error to name the missing key, got %v", err)
	}
}

// TestApplyEmitsShadowStderr wires the end-to-end apply flow with a
// team config and a personal overlay that redeclares the same
// env.secrets key. It captures the pipeline's stderr output through
// os.Pipe so the assertion covers the exact diagnostic line emitted
// by runPipeline, including that no secret bytes reach stderr.
func TestApplyEmitsShadowStderr(t *testing.T) {
	withFakeVaultBackend(t)

	const teamPlaintext = "team-resolved-token-zzzzz"
	const overlayPlaintext = "overlay-resolved-token-zzzzz"

	configTOML := `
[workspace]
name = "shadow-it-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]

[vault.provider]
kind = "fake"

[vault.provider.values]
API_TOKEN = "` + teamPlaintext + `"

[env.secrets]
API_TOKEN = "vault://API_TOKEN"
`

	// Overlay re-declares API_TOKEN under a DIFFERENT provider name
	// so R12 collision does not fire. DetectShadows must still flag
	// the env.secrets key as shadowed.
	overlayTOML := `
[global.vault.providers.personal]
kind = "fake"

[global.vault.providers.personal.values]
API_TOKEN = "` + overlayPlaintext + `"

[global.env.secrets]
API_TOKEN = "vault://personal/API_TOKEN"
`

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	globalDir := filepath.Join(tmpDir, "global-config")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "niwa.toml"), []byte(overlayTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading workspace config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}},
		},
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "shadow-it-ws")
	groupDir := filepath.Join(instanceRoot, "default")
	if err := os.MkdirAll(filepath.Join(groupDir, "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Redirect os.Stderr to capture the pipeline's shadow diagnostic.
	// runPipeline writes directly to os.Stderr via fmt.Fprintf, so
	// we swap the file descriptor for the duration of the Create
	// call and restore it on cleanup.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.GlobalConfigDir = globalDir

	if _, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot); err != nil {
		os.Stderr = origStderr
		w.Close()
		t.Fatalf("Create: %v", err)
	}
	w.Close()
	os.Stderr = origStderr

	stderrBytes, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading captured stderr: %v", err)
	}
	stderrStr := string(stderrBytes)

	want := `shadowed env-secret "API_TOKEN" [personal-overlay shadows team: team=workspace.toml, personal=niwa.toml]`
	if !strings.Contains(stderrStr, want) {
		t.Errorf("stderr missing shadow diagnostic %q\nfull stderr:\n%s", want, stderrStr)
	}

	// R22: no secret bytes anywhere in the captured stderr. Covers
	// both the team-resolved and overlay-resolved plaintext.
	if strings.Contains(stderrStr, teamPlaintext) {
		t.Errorf("stderr leaked team secret bytes %q:\n%s", teamPlaintext, stderrStr)
	}
	if strings.Contains(stderrStr, overlayPlaintext) {
		t.Errorf("stderr leaked overlay secret bytes %q:\n%s", overlayPlaintext, stderrStr)
	}
}

// TestApplyPersistsShadowsInState runs apply with a shadowing overlay
// and asserts the saved InstanceState.Shadows slice carries the
// detected record.
func TestApplyPersistsShadowsInState(t *testing.T) {
	withFakeVaultBackend(t)

	configTOML := `
[workspace]
name = "shadow-persist-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]

[env.vars]
LOG_LEVEL = "debug"
`

	// Overlay shadows the [env.vars] LOG_LEVEL key. No vault in
	// play; the shadow path is independent of the resolver.
	overlayTOML := `
[global.env.vars]
LOG_LEVEL = "trace"
`

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	globalDir := filepath.Join(tmpDir, "global-config")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "niwa.toml"), []byte(overlayTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading workspace config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}},
		},
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "shadow-persist-ws")
	groupDir := filepath.Join(instanceRoot, "default")
	if err := os.MkdirAll(filepath.Join(groupDir, "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.GlobalConfigDir = globalDir

	if _, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot); err != nil {
		t.Fatalf("Create: %v", err)
	}

	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Shadows) != 1 {
		t.Fatalf("state.Shadows = %+v, want one entry", state.Shadows)
	}
	got := state.Shadows[0]
	if got.Kind != "env-var" || got.Name != "LOG_LEVEL" {
		t.Errorf("state.Shadows[0] = %+v, want env-var LOG_LEVEL", got)
	}
	if got.Layer != "personal-overlay" {
		t.Errorf("Layer = %q, want personal-overlay", got.Layer)
	}
}

// TestApplyVaultAllowMissingSecretsDowngrades confirms the
// AllowMissingSecrets plumbing threads through to the resolver and
// downgrades the missing key instead of failing apply.
func TestApplyVaultAllowMissingSecretsDowngrades(t *testing.T) {
	withFakeVaultBackend(t)

	configTOML := `
[workspace]
name = "vault-allow-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]

[vault.provider]
kind = "fake"

[env.secrets]
MISSING = "vault://MISSING"
`

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading workspace config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}}},
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "vault-allow-ws")
	groupDir := filepath.Join(instanceRoot, "default")
	if err := os.MkdirAll(filepath.Join(groupDir, "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.AllowMissingSecrets = true

	if _, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot); err != nil {
		t.Fatalf("expected apply to succeed with AllowMissingSecrets, got %v", err)
	}
}

// TestApplyVaultRequiredFalseDowngradesSilently exercises the
// `?required=false` URI flag end-to-end through the apply pipeline.
// The key is NOT configured in the fake backend; because the URI
// opts out of the required check, apply must succeed without any
// stderr warning AND without AllowMissingSecrets being set.
//
// This locks in Issue 10 AC #4: the CLI path must not strip or
// mangle the query string; `vault://team/anthropic?required=false`
// flows from config through the resolver unchanged.
func TestApplyVaultRequiredFalseDowngradesSilently(t *testing.T) {
	withFakeVaultBackend(t)

	configTOML := `
[workspace]
name = "vault-optional-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]

[vault.provider]
kind = "fake"

[env.secrets]
ANTHROPIC_KEY = "vault://ANTHROPIC_KEY?required=false"
`

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading workspace config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}}},
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "vault-optional-ws")
	groupDir := filepath.Join(instanceRoot, "default")
	if err := os.MkdirAll(filepath.Join(groupDir, "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Capture stderr through a pipe so we can assert no warning
	// line is emitted. AllowMissingSecrets is intentionally false —
	// the ?required=false query flag alone must drive the downgrade.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	_, createErr := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot)
	w.Close()
	os.Stderr = origStderr

	stderrBytes, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("reading captured stderr: %v", readErr)
	}

	if createErr != nil {
		t.Fatalf("expected apply to succeed with ?required=false, got %v\nstderr:\n%s",
			createErr, string(stderrBytes))
	}

	out := string(stderrBytes)
	// The canonical AllowMissing warning shape is
	// "warning: vault: ... --allow-missing-secrets". ?required=false
	// must downgrade silently, so that string must NOT appear.
	if strings.Contains(out, "--allow-missing-secrets") {
		t.Errorf("?required=false must not emit AllowMissing warning, got:\n%s", out)
	}
	// Broader check: no "warning: vault:" line at all, since the
	// URI opts into the silent downgrade.
	if strings.Contains(out, "warning: vault:") {
		t.Errorf("unexpected vault warning for optional key:\n%s", out)
	}
}
