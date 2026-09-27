package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// writeOnboardWorkspace creates a temp instance directory with the
// given [vault.provider] body (already including the "[vault.provider]"
// header line, or empty for no vault block at all) and chdirs the
// test process into it.
func writeOnboardWorkspace(t *testing.T, vaultBlock string) {
	t.Helper()
	instanceDir := t.TempDir()
	niwaDir := filepath.Join(instanceDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatalf("creating .niwa dir: %v", err)
	}
	cfg := "[workspace]\nname = \"test-ws\"\n\n" + vaultBlock
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("writing workspace.toml: %v", err)
	}
	t.Chdir(instanceDir)
}

// sandboxOnboardHome points HOME/XDG_CONFIG_HOME at a fresh temp dir
// so config.GlobalConfigDir() never touches the real developer
// machine's config.
func sandboxOnboardHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

func TestLoadOnboardConfig_NoWorkspaceConfigIsClearGuidanceNotPanic(t *testing.T) {
	sandboxOnboardHome(t)
	t.Chdir(t.TempDir()) // no .niwa/workspace.toml anywhere above this

	_, err := loadOnboardConfig()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "niwa onboard must be run from inside a niwa workspace or instance") {
		t.Errorf("err = %q, want it to name the missing-workspace condition clearly", err.Error())
	}
}

func TestLoadOnboardConfig_EmptyVaultBlockIsClearGuidanceNotPanic(t *testing.T) {
	sandboxOnboardHome(t)
	writeOnboardWorkspace(t, "")

	_, err := loadOnboardConfig()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "declares no [vault.provider] block yet") {
		t.Errorf("err = %q, want it to name the missing [vault.provider] block", err.Error())
	}
}

func TestLoadOnboardConfig_NamedProviderShapeNotYetSupportedIsClearGuidance(t *testing.T) {
	sandboxOnboardHome(t)
	writeOnboardWorkspace(t, "[vault.providers.acme]\nkind = \"infisical\"\nproject = \"proj-1\"\n")

	_, err := loadOnboardConfig()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "anonymous [vault.provider] shape") {
		t.Errorf("err = %q, want it to explain only the anonymous shape is supported", err.Error())
	}
}

func TestLoadOnboardConfig_MissingIdentityFieldsNameEachOne(t *testing.T) {
	sandboxOnboardHome(t)
	// Declares kind and project only -- identity_id, identity_name, and
	// env are all absent.
	writeOnboardWorkspace(t, "[vault.provider]\nkind = \"infisical\"\nproject = \"proj-1\"\n")

	_, err := loadOnboardConfig()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	for _, field := range []string{"identity_id", "identity_name", "env"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("err = %q, want it to name missing field %q", err.Error(), field)
		}
	}
	if strings.Contains(err.Error(), "project") {
		t.Errorf("err = %q, must not claim \"project\" is missing -- it was declared", err.Error())
	}
}

func TestLoadOnboardConfig_FullyDeclaredProviderResolvesEveryField(t *testing.T) {
	sandboxOnboardHome(t)
	writeOnboardWorkspace(t, "[vault.provider]\n"+
		"kind = \"infisical\"\n"+
		"project = \"proj-1\"\n"+
		"identity_id = \"ident-1\"\n"+
		"identity_name = \"Test Identity\"\n"+
		"auth_method = \"Universal Auth\"\n"+
		"env = \"dev\"\n"+
		"path = \"/team\"\n")

	bundle, err := loadOnboardConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cases := map[string]string{
		"kind":            bundle.kind,
		"projectID":       bundle.projectID,
		"identityID":      bundle.identityID,
		"identityName":    bundle.identityName,
		"authMethod":      bundle.authMethod,
		"environmentSlug": bundle.environmentSlug,
		"secretPath":      bundle.secretPath,
	}
	want := map[string]string{
		"kind":            "infisical",
		"projectID":       "proj-1",
		"identityID":      "ident-1",
		"identityName":    "Test Identity",
		"authMethod":      "Universal Auth",
		"environmentSlug": "dev",
		"secretPath":      "/team",
	}
	for field, got := range cases {
		if got != want[field] {
			t.Errorf("%s = %q, want %q", field, got, want[field])
		}
	}
}

func TestLoadOnboardConfig_AuthMethodDefaultsToUniversalAuthWhenAbsent(t *testing.T) {
	sandboxOnboardHome(t)
	writeOnboardWorkspace(t, "[vault.provider]\n"+
		"kind = \"infisical\"\n"+
		"project = \"proj-1\"\n"+
		"identity_id = \"ident-1\"\n"+
		"identity_name = \"Test Identity\"\n"+
		"env = \"dev\"\n")

	bundle, err := loadOnboardConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle.authMethod != "Universal Auth" {
		t.Errorf("authMethod = %q, want the generic default \"Universal Auth\"", bundle.authMethod)
	}
}

func TestLoadOnboardConfig_MissingPersonalOverlayFileIsEmptyOverrideNotError(t *testing.T) {
	sandboxOnboardHome(t)
	writeOnboardWorkspace(t, "[vault.provider]\n"+
		"kind = \"infisical\"\n"+
		"project = \"proj-1\"\n"+
		"identity_id = \"ident-1\"\n"+
		"identity_name = \"Test Identity\"\n"+
		"env = \"dev\"\n")
	// Deliberately no niwa.toml written under XDG_CONFIG_HOME/niwa/global.

	bundle, err := loadOnboardConfig()
	if err != nil {
		t.Fatalf("unexpected error (an unscaffolded personal overlay is an R22 precondition concern, not a load-time error): %v", err)
	}
	if bundle.globalOverride == nil {
		t.Fatal("globalOverride = nil, want a non-nil empty override")
	}
	if bundle.syncSpec.Kind != "" {
		t.Errorf("syncSpec.Kind = %q, want empty (no [global.vault.provider] declared)", bundle.syncSpec.Kind)
	}
}

func TestLoadOnboardConfig_PersonalOverlaySyncSpecResolvesFromGlobalVaultProvider(t *testing.T) {
	sandboxOnboardHome(t)
	writeOnboardWorkspace(t, "[vault.provider]\n"+
		"kind = \"infisical\"\n"+
		"project = \"proj-1\"\n"+
		"identity_id = \"ident-1\"\n"+
		"identity_name = \"Test Identity\"\n"+
		"env = \"dev\"\n")

	overlayDir, err := config.GlobalConfigDir()
	if err != nil {
		t.Fatalf("resolving overlay dir: %v", err)
	}
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatalf("creating overlay dir: %v", err)
	}
	overlayBody := "[global.vault.provider]\nkind = \"infisical\"\nproject = \"personal-proj\"\n"
	if err := os.WriteFile(filepath.Join(overlayDir, "niwa.toml"), []byte(overlayBody), 0o644); err != nil {
		t.Fatalf("writing personal overlay: %v", err)
	}

	bundle, err := loadOnboardConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle.syncSpec.Kind != "infisical" {
		t.Errorf("syncSpec.Kind = %q, want infisical", bundle.syncSpec.Kind)
	}
	if got, _ := bundle.syncSpec.Config["project"].(string); got != "personal-proj" {
		t.Errorf("syncSpec.Config[project] = %q, want personal-proj", got)
	}
}

func TestResolveOperatorBearer_EnvOverrideMissingIsClearGuidanceNotPanic(t *testing.T) {
	t.Setenv(operatorBearerEnvOverride, "")

	_, err := resolveOperatorBearer()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), operatorBearerEnvOverride) {
		t.Errorf("err = %q, want it to name %s", err.Error(), operatorBearerEnvOverride)
	}
}

func TestResolveOperatorBearer_EnvOverridePresentSucceeds(t *testing.T) {
	t.Setenv(operatorBearerEnvOverride, "a-test-token")

	bearer, err := resolveOperatorBearer()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bearer.IsEmpty() {
		t.Error("bearer is empty, want a populated secret.Value")
	}
}
