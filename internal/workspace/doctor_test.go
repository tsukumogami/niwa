package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/vault"
)

// fakeExportCommander implements infisical's unexported commander
// interface (Run(ctx, name, args) (stdout, stderr []byte, exitCode int,
// err error)) structurally -- Go interfaces are satisfied by method set
// alone, so this type, defined here, is accepted by
// infisical.Factory.Open's config["_commander"] type assertion despite
// living in a different package. This drives CheckProviderAuth through
// the REAL "infisical" vault.DefaultRegistry factory and the REAL
// `infisical export` code path (subprocess.go), matching PRD AC-18b's
// wording that the wizard-end read "resolves through the credential-sync
// provider's infisical export path" -- without forking a real binary.
type fakeExportCommander struct {
	stdout   string
	stderr   string
	exitCode int
}

func (f *fakeExportCommander) Run(_ context.Context, _ string, _ []string) ([]byte, []byte, int, error) {
	return []byte(f.stdout), []byte(f.stderr), f.exitCode, nil
}

// testGlobalOverride builds a *config.GlobalConfigOverride declaring an
// anonymous [global.vault.provider] of kind "infisical" for project,
// wired to cmd via the _commander test hook.
func testGlobalOverride(project string, cmd *fakeExportCommander) *config.GlobalConfigOverride {
	return &config.GlobalConfigOverride{
		Global: config.GlobalOverride{
			Vault: &config.VaultRegistry{
				Provider: &config.VaultProviderConfig{
					Kind: "infisical",
					Config: map[string]any{
						"project":    project,
						"_commander": cmd,
					},
				},
			},
		},
	}
}

// setIsolatedNiwaConfigDir points NiwaConfigDir() at a fresh temp
// directory so CheckProviderAuth's LoadProviderAuth call never reads
// the real machine's ~/.config/niwa/provider-auth.toml.
func setIsolatedNiwaConfigDir(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestCheckProviderAuth_HappyPath(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	cmd := &fakeExportCommander{stdout: `{"p-uuid-1": "version = \"1\"\nclient_id = \"cid\"\nclient_secret = \"csec\"\n"}`}
	override := testGlobalOverride("sync-project", cmd)

	result, err := CheckProviderAuth(context.Background(), override, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Err != nil {
		t.Fatalf("unexpected per-pair error: %v", result.Err)
	}
	if result.Source != SourceVault {
		t.Errorf("Source = %q, want %q", result.Source, SourceVault)
	}
	if result.Kind != "infisical" || result.Project != "uuid-1" {
		t.Errorf("Kind/Project = %s/%s, want infisical/uuid-1", result.Kind, result.Project)
	}
}

func TestCheckProviderAuth_MalformedBody(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	cmd := &fakeExportCommander{stdout: `{"p-uuid-1": "not = [valid toml"}`}
	override := testGlobalOverride("sync-project", cmd)

	result, err := CheckProviderAuth(context.Background(), override, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Err == nil {
		t.Fatal("want a per-pair error for a malformed body, got nil")
	}
	if !strings.Contains(result.Err.Error(), "malformed") {
		t.Errorf("error = %q, want it to name the body as malformed", result.Err.Error())
	}
}

func TestCheckProviderAuth_MissingField(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	// client_secret is absent.
	cmd := &fakeExportCommander{stdout: `{"p-uuid-1": "version = \"1\"\nclient_id = \"cid\"\n"}`}
	override := testGlobalOverride("sync-project", cmd)

	result, err := CheckProviderAuth(context.Background(), override, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Err == nil {
		t.Fatal("want a per-pair error for a missing field, got nil")
	}
	if !strings.Contains(result.Err.Error(), "client_secret") {
		t.Errorf("error = %q, want it to name the missing field client_secret", result.Err.Error())
	}
}

func TestCheckProviderAuth_UnsupportedVersion(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	cmd := &fakeExportCommander{stdout: `{"p-uuid-1": "version = \"2\"\nclient_id = \"cid\"\nclient_secret = \"csec\"\n"}`}
	override := testGlobalOverride("sync-project", cmd)

	result, err := CheckProviderAuth(context.Background(), override, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Err == nil {
		t.Fatal("want a per-pair error for an unsupported version, got nil")
	}
	if !strings.Contains(result.Err.Error(), "unsupported schema version") {
		t.Errorf("error = %q, want it to name the unsupported schema version", result.Err.Error())
	}
}

func TestCheckProviderAuth_MissingEntry(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	// The export succeeds but carries no entry for this key -- the
	// project has no secrets at this path, or a different pair.
	cmd := &fakeExportCommander{stdout: `{}`}
	override := testGlobalOverride("sync-project", cmd)

	result, err := CheckProviderAuth(context.Background(), override, "infisical", "uuid-missing")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Err == nil {
		t.Fatal("want ErrProviderAuthMissingEntry, got nil")
	}
	if !errors.Is(result.Err, ErrProviderAuthMissingEntry) {
		t.Errorf("err = %v, want it to wrap ErrProviderAuthMissingEntry", result.Err)
	}
	if result.Source != SourceCLISession {
		t.Errorf("Source = %q, want %q (no file, no vault entry)", result.Source, SourceCLISession)
	}
}

func TestCheckProviderAuth_VaultUnreachable(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	cmd := &fakeExportCommander{stderr: "Error: 401 unauthorized", exitCode: 1}
	override := testGlobalOverride("sync-project", cmd)

	result, err := CheckProviderAuth(context.Background(), override, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Err == nil {
		t.Fatal("want a per-pair vault-unreachable error, got nil")
	}
	if !errors.Is(result.Err, vault.ErrProviderUnreachable) {
		t.Errorf("err = %v, want it to wrap vault.ErrProviderUnreachable", result.Err)
	}
}

func TestCheckProviderAuth_NilGlobalOverride(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	_, err := CheckProviderAuth(context.Background(), nil, "infisical", "uuid-1")
	if err == nil {
		t.Fatal("want a setup-level error for a nil globalOverride, got nil")
	}
}

func TestCheckProviderAuth_NoSyncProviderDeclared(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	override := &config.GlobalConfigOverride{} // no [global.vault.provider]
	_, err := CheckProviderAuth(context.Background(), override, "infisical", "uuid-1")
	if err == nil {
		t.Fatal("want a setup-level error when no credential-sync provider is declared, got nil")
	}
}

func TestCheckProviderAuth_SelfReferentialGuardIsCallerError(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	cmd := &fakeExportCommander{stdout: `{}`}
	override := testGlobalOverride("sync-project", cmd)

	// (kind, project) matches the sync provider's own pair exactly.
	_, err := CheckProviderAuth(context.Background(), override, "infisical", "sync-project")
	if err == nil {
		t.Fatal("want a setup-level caller-bug error for the self-referential pair, got nil")
	}
}
