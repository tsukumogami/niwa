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
//
// responses maps a raw export-JSON-body substring to what that call
// should return, keyed loosely by whatever the test cares to
// distinguish -- most tests only ever need a single canned response
// (stdout), which is used when responses is nil.
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

	result, err := CheckProviderAuth(context.Background(), override, nil, nil, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Target.Err != nil {
		t.Fatalf("unexpected per-pair error: %v", result.Target.Err)
	}
	if result.Target.Source != SourceVault {
		t.Errorf("Source = %q, want %q", result.Target.Source, SourceVault)
	}
	if result.Target.Kind != "infisical" || result.Target.Project != "uuid-1" {
		t.Errorf("Kind/Project = %s/%s, want infisical/uuid-1", result.Target.Kind, result.Target.Project)
	}
	if len(result.OtherFailures) != 0 {
		t.Errorf("OtherFailures = %+v, want empty (no registries swept)", result.OtherFailures)
	}
}

func TestCheckProviderAuth_MalformedBody(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	cmd := &fakeExportCommander{stdout: `{"p-uuid-1": "not = [valid toml"}`}
	override := testGlobalOverride("sync-project", cmd)

	result, err := CheckProviderAuth(context.Background(), override, nil, nil, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Target.Err == nil {
		t.Fatal("want a per-pair error for a malformed body, got nil")
	}
	if !strings.Contains(result.Target.Err.Error(), "malformed") {
		t.Errorf("error = %q, want it to name the body as malformed", result.Target.Err.Error())
	}
}

func TestCheckProviderAuth_MissingField(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	// client_secret is absent.
	cmd := &fakeExportCommander{stdout: `{"p-uuid-1": "version = \"1\"\nclient_id = \"cid\"\n"}`}
	override := testGlobalOverride("sync-project", cmd)

	result, err := CheckProviderAuth(context.Background(), override, nil, nil, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Target.Err == nil {
		t.Fatal("want a per-pair error for a missing field, got nil")
	}
	if !strings.Contains(result.Target.Err.Error(), "client_secret") {
		t.Errorf("error = %q, want it to name the missing field client_secret", result.Target.Err.Error())
	}
}

func TestCheckProviderAuth_UnsupportedVersion(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	cmd := &fakeExportCommander{stdout: `{"p-uuid-1": "version = \"2\"\nclient_id = \"cid\"\nclient_secret = \"csec\"\n"}`}
	override := testGlobalOverride("sync-project", cmd)

	result, err := CheckProviderAuth(context.Background(), override, nil, nil, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Target.Err == nil {
		t.Fatal("want a per-pair error for an unsupported version, got nil")
	}
	if !strings.Contains(result.Target.Err.Error(), "unsupported schema version") {
		t.Errorf("error = %q, want it to name the unsupported schema version", result.Target.Err.Error())
	}
}

func TestCheckProviderAuth_MissingEntry(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	// The export succeeds but carries no entry for this key -- the
	// project has no secrets at this path, or a different pair.
	cmd := &fakeExportCommander{stdout: `{}`}
	override := testGlobalOverride("sync-project", cmd)

	result, err := CheckProviderAuth(context.Background(), override, nil, nil, "infisical", "uuid-missing")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Target.Err == nil {
		t.Fatal("want ErrProviderAuthMissingEntry, got nil")
	}
	if !errors.Is(result.Target.Err, ErrProviderAuthMissingEntry) {
		t.Errorf("err = %v, want it to wrap ErrProviderAuthMissingEntry", result.Target.Err)
	}
	if result.Target.Source != SourceCLISession {
		t.Errorf("Source = %q, want %q (no file, no vault entry)", result.Target.Source, SourceCLISession)
	}
}

func TestCheckProviderAuth_VaultUnreachable(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	cmd := &fakeExportCommander{stderr: "Error: 401 unauthorized", exitCode: 1}
	override := testGlobalOverride("sync-project", cmd)

	result, err := CheckProviderAuth(context.Background(), override, nil, nil, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Target.Err == nil {
		t.Fatal("want a per-pair vault-unreachable error, got nil")
	}
	if !errors.Is(result.Target.Err, vault.ErrProviderUnreachable) {
		t.Errorf("err = %v, want it to wrap vault.ErrProviderUnreachable", result.Target.Err)
	}
}

func TestCheckProviderAuth_NilGlobalOverride(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	_, err := CheckProviderAuth(context.Background(), nil, nil, nil, "infisical", "uuid-1")
	if err == nil {
		t.Fatal("want a setup-level error for a nil globalOverride, got nil")
	}
}

func TestCheckProviderAuth_NoSyncProviderDeclared(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	override := &config.GlobalConfigOverride{} // no [global.vault.provider]
	_, err := CheckProviderAuth(context.Background(), override, nil, nil, "infisical", "uuid-1")
	if err == nil {
		t.Fatal("want a setup-level error when no credential-sync provider is declared, got nil")
	}
}

func TestCheckProviderAuth_SelfReferentialGuardIsCallerError(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	cmd := &fakeExportCommander{stdout: `{}`}
	override := testGlobalOverride("sync-project", cmd)

	// (kind, project) matches the sync provider's own pair exactly.
	_, err := CheckProviderAuth(context.Background(), override, nil, nil, "infisical", "sync-project")
	if err == nil {
		t.Fatal("want a setup-level caller-bug error for the self-referential pair, got nil")
	}
}

// --- Three-registry sweep (AC-18's "across the three vault-registry
// sources") ---

func TestCheckProviderAuth_SweepIgnoresCLISessionFallthroughOnOtherPairs(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	// The commander's export response only ever carries the target
	// pair's entry -- "other-project" resolves via the pool to a plain
	// miss (SourceCLISession, no error), which the sweep must NOT
	// report as a failure: that is the normal, successful fallback
	// injectProviderTokens already tolerates for any pair it isn't
	// specifically responsible for proving.
	cmd := &fakeExportCommander{stdout: `{"p-uuid-1": "version = \"1\"\nclient_id = \"cid\"\nclient_secret = \"csec\"\n"}`}
	override := testGlobalOverride("sync-project", cmd)

	teamVault := &config.VaultRegistry{
		Provider: &config.VaultProviderConfig{
			Kind:   "infisical",
			Config: map[string]any{"project": "other-project"},
		},
	}

	result, err := CheckProviderAuth(context.Background(), override, teamVault, nil, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Target.Err != nil {
		t.Fatalf("unexpected target error: %v", result.Target.Err)
	}
	if len(result.OtherFailures) != 0 {
		t.Errorf("OtherFailures = %+v, want empty -- a CLI-session fallthrough on an unrelated pair is not a failure", result.OtherFailures)
	}
}

// TestCheckProviderAuth_SweepToleratesVaultUnreachableOnOtherPair
// guards the isSoftenable regression a round-2 justification/intent
// scrutiny pass caught: a swept (non-target) pair whose vault is
// merely unreachable (a transient condition, PRD R13.1) must be
// tolerated exactly as injectProviderTokens/isSoftenable tolerates it
// in production -- NOT reported as an OtherFailures entry. Reporting
// it would make the wizard-end check disagree with a real `niwa
// apply`, which would just log an aggregated warning and continue,
// for the exact scenario R11 exists to keep in sync.
func TestCheckProviderAuth_SweepToleratesVaultUnreachableOnOtherPair(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	// The commander fails (non-zero exit, auth-failure marker) for
	// every export call -- since ensureLoaded caches per effective
	// path and both the target and the swept pair share the same
	// path ("/niwa/provider-auth/infisical"), this single failing
	// response covers both keys' lookups.
	cmd := &fakeExportCommander{stderr: "Error: 401 unauthorized", exitCode: 1}
	override := testGlobalOverride("sync-project", cmd)

	teamVault := &config.VaultRegistry{
		Provider: &config.VaultProviderConfig{
			Kind:   "infisical",
			Config: map[string]any{"project": "other-project"},
		},
	}

	result, err := CheckProviderAuth(context.Background(), override, teamVault, nil, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	// The target pair itself DOES fail strictly (it must resolve) --
	// this test only asserts the SWEPT pair's vault-unreachable
	// condition is tolerated, not double-reported in OtherFailures.
	if result.Target.Err == nil {
		t.Fatal("want the target pair to fail strictly on vault-unreachable, got nil")
	}
	if len(result.OtherFailures) != 0 {
		t.Errorf("OtherFailures = %+v, want empty -- a vault-unreachable condition on an unrelated swept pair must be tolerated (PRD R13.1), not reported", result.OtherFailures)
	}
}

func TestCheckProviderAuth_SweepReportsMalformedBodyOnOtherPair(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	// Both the target ("uuid-1") and a second declared pair
	// ("other-project") have export entries; the second one is
	// malformed. The sweep must surface it as an OtherFailures entry
	// even though the target itself resolves cleanly.
	cmd := &fakeExportCommander{stdout: `{
		"p-uuid-1": "version = \"1\"\nclient_id = \"cid\"\nclient_secret = \"csec\"\n",
		"p-other-project": "not = [valid toml"
	}`}
	override := testGlobalOverride("sync-project", cmd)

	overlayVault := &config.VaultRegistry{
		Providers: map[string]config.VaultProviderConfig{
			"secondary": {
				Kind:   "infisical",
				Config: map[string]any{"project": "other-project"},
			},
		},
	}

	result, err := CheckProviderAuth(context.Background(), override, nil, overlayVault, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Target.Err != nil {
		t.Fatalf("unexpected target error: %v", result.Target.Err)
	}
	if len(result.OtherFailures) != 1 {
		t.Fatalf("OtherFailures = %+v, want exactly 1 entry", result.OtherFailures)
	}
	got := result.OtherFailures[0]
	if got.Kind != "infisical" || got.Project != "other-project" {
		t.Errorf("OtherFailures[0] Kind/Project = %s/%s, want infisical/other-project", got.Kind, got.Project)
	}
	if !strings.Contains(got.Err.Error(), "malformed") {
		t.Errorf("OtherFailures[0].Err = %q, want it to name the body as malformed", got.Err.Error())
	}
}

func TestCheckProviderAuth_SweepDedupesAcrossRegistriesAndExcludesSelfAndTarget(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	// "other-project" is declared in BOTH teamVault and overlayVault --
	// the sweep must look it up only once (Lookup is deterministic and
	// idempotent per-pair via the pool's own cache, but this asserts
	// the dedup doesn't produce duplicate OtherFailures entries either).
	// The sync provider's own pair ("sync-project") and the target pair
	// ("uuid-1") are also declared in one of the swept registries, and
	// must never appear in OtherFailures (self-exclusion / already
	// checked as Target).
	cmd := &fakeExportCommander{stdout: `{
		"p-uuid-1": "version = \"1\"\nclient_id = \"cid\"\nclient_secret = \"csec\"\n",
		"p-other-project": "not = [valid toml"
	}`}
	override := testGlobalOverride("sync-project", cmd)

	registryWith := func(project string) *config.VaultRegistry {
		return &config.VaultRegistry{
			Provider: &config.VaultProviderConfig{
				Kind:   "infisical",
				Config: map[string]any{"project": project},
			},
			Providers: map[string]config.VaultProviderConfig{
				"dup":    {Kind: "infisical", Config: map[string]any{"project": "other-project"}},
				"self":   {Kind: "infisical", Config: map[string]any{"project": "sync-project"}},
				"target": {Kind: "infisical", Config: map[string]any{"project": "uuid-1"}},
			},
		}
	}

	result, err := CheckProviderAuth(context.Background(), override, registryWith("other-project"), registryWith("other-project"), "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if len(result.OtherFailures) != 1 {
		t.Fatalf("OtherFailures = %+v, want exactly 1 deduplicated entry", result.OtherFailures)
	}
	if result.OtherFailures[0].Project != "other-project" {
		t.Errorf("OtherFailures[0].Project = %q, want other-project", result.OtherFailures[0].Project)
	}
}

func TestCheckProviderAuth_NilSweepRegistriesAreSafe(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	cmd := &fakeExportCommander{stdout: `{"p-uuid-1": "version = \"1\"\nclient_id = \"cid\"\nclient_secret = \"csec\"\n"}`}
	override := testGlobalOverride("sync-project", cmd)

	result, err := CheckProviderAuth(context.Background(), override, nil, nil, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Target.Err != nil {
		t.Fatalf("unexpected target error: %v", result.Target.Err)
	}
	if len(result.OtherFailures) != 0 {
		t.Errorf("OtherFailures = %+v, want empty when both sweep registries are nil", result.OtherFailures)
	}
}
