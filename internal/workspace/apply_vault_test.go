package workspace

import (
	"context"
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
