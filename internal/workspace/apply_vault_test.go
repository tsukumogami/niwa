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

// TestApplyEmitsRotatedOnVaultRotation verifies the PRD Rotation AC:
// after a team rotates a vault secret upstream, the next niwa apply
// re-resolves, re-materializes, and reports `rotated <path>` to stderr.
//
// The scenario:
//
//  1. First apply resolves vault://TOKEN against the fake provider
//     (value A), writes .local.env, persists state.
//  2. The fake provider's value changes to B (same key).
//  3. Second apply re-resolves, re-materializes (new plaintext lands on
//     disk), and MUST emit `rotated <absolute-path>` to stderr. The
//     rotation is detected via the SourceFingerprint flip + the vault
//     source's VersionToken change.
//
// The test also asserts `rotated` does NOT appear on the FIRST apply —
// a first-time materialization is not a rotation.
func TestApplyEmitsRotatedOnVaultRotation(t *testing.T) {
	withFakeVaultBackend(t)

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgTOMLFmt := `
[workspace]
name = "apply-rot-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]

[vault.provider]
kind = "fake"

[vault.provider.values]
TOKEN = "%s"

[env.secrets]
TOKEN = "vault://TOKEN"
`
	writeCfg := func(value string) {
		body := strings.Replace(cfgTOMLFmt, "%s", value, 1)
		if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeCfg("first-value-aaaaaaaaaaaa")
	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}},
		},
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "apply-rot-ws")
	groupDir := filepath.Join(instanceRoot, "default")
	if err := os.MkdirAll(filepath.Join(groupDir, "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	// First apply: capture stderr. The file is being materialized for
	// the first time — `rotated` must NOT appear.
	origStderr := os.Stderr
	r1, w1, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w1
	_, createErr := applier.Create(context.Background(), result.Config, niwaDir, workspaceRoot)
	w1.Close()
	os.Stderr = origStderr
	stderr1, _ := io.ReadAll(r1)
	if createErr != nil {
		t.Fatalf("first Create: %v\nstderr: %s", createErr, string(stderr1))
	}
	envFilePath := filepath.Join(instanceRoot, "default", "app", ".local.env")
	if strings.Contains(string(stderr1), "rotated ") {
		t.Errorf("first-time materialization must not emit `rotated`, got:\n%s", string(stderr1))
	}

	// Rotate upstream: rewrite the fake provider's value, reparse.
	writeCfg("second-value-bbbbbbbbbbbb")
	result2, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("config.Load after rotation: %v", err)
	}

	// Second apply: stderr MUST contain "rotated <envFilePath>".
	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w2
	applyErr := applier.Apply(context.Background(), result2.Config, niwaDir, instanceRoot)
	w2.Close()
	os.Stderr = origStderr
	stderr2, _ := io.ReadAll(r2)
	if applyErr != nil {
		t.Fatalf("Apply after rotation: %v\nstderr: %s", applyErr, string(stderr2))
	}

	want := "rotated " + envFilePath
	if !strings.Contains(string(stderr2), want) {
		t.Errorf("stderr missing %q after vault rotation\nfull stderr:\n%s", want, string(stderr2))
	}

	// The materialized file must contain the new value (sanity check
	// that re-resolution actually happened, not just that we emitted
	// the message in isolation).
	data, err := os.ReadFile(envFilePath)
	if err != nil {
		t.Fatalf("reading rotated env file: %v", err)
	}
	if !strings.Contains(string(data), "second-value-bbbbbbbbbbbb") {
		t.Errorf("env file does not contain rotated plaintext, got:\n%s", string(data))
	}
}

// TestApplyFailsOnMissingRequiredEnvSecret enforces PRD R33: a
// [env.secrets.required] key with no corresponding value MUST cause
// niwa apply to fail. The error must name the missing key and include
// the team-authored description string.
func TestApplyFailsOnMissingRequiredEnvSecret(t *testing.T) {
	withFakeVaultBackend(t)

	configTOML := `
[workspace]
name = "required-miss-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]

[env.secrets.required]
GITHUB_TOKEN = "GitHub PAT with repo:read scope"
`

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	parsed, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}},
		},
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "required-miss-ws")
	groupDir := filepath.Join(instanceRoot, "default")
	if err := os.MkdirAll(filepath.Join(groupDir, "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	_, err = applier.Create(context.Background(), parsed.Config, niwaDir, workspaceRoot)
	if err == nil {
		t.Fatal("expected apply to fail when a required env secret is missing")
	}
	msg := err.Error()
	if !strings.Contains(msg, "GITHUB_TOKEN") {
		t.Errorf("error must name the missing key, got: %v", err)
	}
	if !strings.Contains(msg, "GitHub PAT with repo:read scope") {
		t.Errorf("error must include the required-table description, got: %v", err)
	}
	if !strings.Contains(msg, "env.secrets") {
		t.Errorf("error must name the offending scope, got: %v", err)
	}
}

// TestApplyAllowMissingSecretsDoesNotDowngradeRequired enforces PRD R34:
// --allow-missing-secrets downgrades missing vault refs to empty, but
// MUST NOT downgrade a [env.*.required] declaration. The required
// check runs on the post-resolve value and fires on the empty
// MaybeSecret the resolver produced.
func TestApplyAllowMissingSecretsDoesNotDowngradeRequired(t *testing.T) {
	withFakeVaultBackend(t)

	configTOML := `
[workspace]
name = "required-allow-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]

[vault.provider]
kind = "fake"

[env.secrets.required]
GITHUB_TOKEN = "GitHub PAT with repo:read scope"

[env.secrets]
GITHUB_TOKEN = "vault://GITHUB_TOKEN"
`

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	parsed, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}},
		},
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "required-allow-ws")
	groupDir := filepath.Join(instanceRoot, "default")
	if err := os.MkdirAll(filepath.Join(groupDir, "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.AllowMissingSecrets = true

	_, err = applier.Create(context.Background(), parsed.Config, niwaDir, workspaceRoot)
	if err == nil {
		t.Fatal("expected apply to fail even with --allow-missing-secrets when a required key is missing (R34)")
	}
	msg := err.Error()
	if !strings.Contains(msg, "GITHUB_TOKEN") {
		t.Errorf("error must name the missing key, got: %v", err)
	}
	if !strings.Contains(msg, "GitHub PAT with repo:read scope") {
		t.Errorf("error must include the required-table description, got: %v", err)
	}
}

// TestApplyMissingRecommendedEmitsStderrWarning enforces the
// recommended-sub-table contract: a miss emits a stderr warning line
// (loud but non-fatal) and apply continues.
func TestApplyMissingRecommendedEmitsStderrWarning(t *testing.T) {
	withFakeVaultBackend(t)

	configTOML := `
[workspace]
name = "recommended-miss-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]

[env.secrets.recommended]
SENTRY_DSN = "Sentry error reporting"
`

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	parsed, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}},
		},
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "recommended-miss-ws")
	groupDir := filepath.Join(instanceRoot, "default")
	if err := os.MkdirAll(filepath.Join(groupDir, "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Capture stderr.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	_, createErr := applier.Create(context.Background(), parsed.Config, niwaDir, workspaceRoot)
	w.Close()
	os.Stderr = origStderr

	stderrBytes, _ := io.ReadAll(r)
	if createErr != nil {
		t.Fatalf("apply should succeed when a recommended key is missing, got: %v\nstderr:\n%s", createErr, string(stderrBytes))
	}

	out := string(stderrBytes)
	if !strings.Contains(out, "SENTRY_DSN") {
		t.Errorf("stderr must name the missing recommended key, got:\n%s", out)
	}
	if !strings.Contains(out, "Sentry error reporting") {
		t.Errorf("stderr must include the description, got:\n%s", out)
	}
	if !strings.Contains(out, "warning: recommended") {
		t.Errorf("stderr must flag the line as a recommended-key warning, got:\n%s", out)
	}
}

// TestApplyMissingOptionalSilent enforces the optional-sub-table
// contract: a miss is silent in v1 (no verbose flag yet). Apply
// succeeds and stderr carries no mention of the missing optional key.
func TestApplyMissingOptionalSilent(t *testing.T) {
	withFakeVaultBackend(t)

	configTOML := `
[workspace]
name = "optional-miss-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]

[env.vars.optional]
DEBUG_WEBHOOK_URL = "Personal debug webhook"
`

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	parsed, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}},
		},
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "optional-miss-ws")
	groupDir := filepath.Join(instanceRoot, "default")
	if err := os.MkdirAll(filepath.Join(groupDir, "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	_, createErr := applier.Create(context.Background(), parsed.Config, niwaDir, workspaceRoot)
	w.Close()
	os.Stderr = origStderr

	stderrBytes, _ := io.ReadAll(r)
	if createErr != nil {
		t.Fatalf("apply should succeed when an optional key is missing, got: %v\nstderr:\n%s", createErr, string(stderrBytes))
	}

	out := string(stderrBytes)
	if strings.Contains(out, "DEBUG_WEBHOOK_URL") {
		t.Errorf("optional-key miss must be silent in v1, but stderr mentioned the key:\n%s", out)
	}
}

// TestResolveMultiSourceWithoutVaultScopeFails enforces PRD R5: a
// workspace with more than one [[sources]] block AND an active
// personal overlay MUST fail apply if [workspace].vault_scope is
// unset. The error names the candidate source orgs so the user has
// an obvious path forward.
func TestResolveMultiSourceWithoutVaultScopeFails(t *testing.T) {
	withFakeVaultBackend(t)

	// Multi-source workspace with no vault_scope. Two different orgs
	// so the ambiguity is obvious in the error message.
	configTOML := `
[workspace]
name = "multi-source-ws"

[[sources]]
org = "tsukumogami"

[[sources]]
org = "codespar"

[groups.default]
repos = ["app"]
`

	// Personal overlay that offers scope-specific blocks for both
	// orgs. The resolver has no way to pick between them without
	// explicit vault_scope.
	overlayTOML := `
[workspaces.tsukumogami.env.vars]
LOG_LEVEL = "tsukumogami-debug"

[workspaces.codespar.env.vars]
LOG_LEVEL = "codespar-debug"
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

	parsed, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	// Point the resolver at BOTH orgs so Classify doesn't reject the
	// fixture for a missing repo lookup. For this test the classifier
	// runs but fails the scope check before touching the providers.
	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"tsukumogami": {{Name: "app", SSHURL: "git@github.com:tsukumogami/app.git"}},
		},
	}

	workspaceRoot := tmpDir
	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.GlobalConfigDir = globalDir

	_, err = applier.Create(context.Background(), parsed.Config, niwaDir, workspaceRoot)
	if err == nil {
		t.Fatal("expected apply to fail on multi-source workspace without vault_scope (R5)")
	}
	msg := err.Error()
	if !strings.Contains(msg, "vault_scope") {
		t.Errorf("error must name vault_scope as the remediation, got: %v", err)
	}
	if !strings.Contains(msg, "tsukumogami") || !strings.Contains(msg, "codespar") {
		t.Errorf("error must list both source orgs so the user can pick, got: %v", err)
	}
}
