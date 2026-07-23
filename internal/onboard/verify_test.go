package onboard

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func TestVerifyIndividual_SetupFailureIsExitVerification(t *testing.T) {
	// A nil GlobalOverride is a setup-level failure inside
	// workspace.CheckProviderAuth; VerifyIndividual must still map it
	// to ExitVerification with an "R11" prefix, not let a bare error
	// escape untyped.
	err := VerifyIndividual(context.Background(), VerifyIndividualParams{
		GlobalOverride: nil,
		Kind:           "infisical",
		Project:        "uuid-1",
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != ExitVerification {
		t.Errorf("Code = %d, want ExitVerification (%d)", ece.Code, ExitVerification)
	}
	if !strings.HasPrefix(ece.Msg, "R11") {
		t.Errorf("Msg = %q, want it prefixed with R11 (distinct from R9/R21)", ece.Msg)
	}
}

func TestVerifyIndividual_NoCredentialSyncProviderDeclaredIsExitVerification(t *testing.T) {
	override := &config.GlobalConfigOverride{} // no [global.vault.provider]
	err := VerifyIndividual(context.Background(), VerifyIndividualParams{
		GlobalOverride: override,
		Kind:           "infisical",
		Project:        "uuid-1",
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != ExitVerification {
		t.Errorf("Code = %d, want ExitVerification (%d)", ece.Code, ExitVerification)
	}
}

func TestSourceOrUnknown(t *testing.T) {
	if got := sourceOrUnknown(""); got != "unknown" {
		t.Errorf("sourceOrUnknown(\"\") = %q, want \"unknown\"", got)
	}
}

// fakeVerifyCommander implements infisical's unexported commander
// interface structurally (see workspace/doctor_test.go's
// fakeExportCommander for the same trick, duplicated here since that
// type lives in a different package's _test.go file and isn't
// importable).
type fakeVerifyCommander struct {
	stdout string
}

func (f *fakeVerifyCommander) Run(_ context.Context, _ string, _ []string) ([]byte, []byte, int, error) {
	return []byte(f.stdout), nil, 0, nil
}

func testVerifyGlobalOverride(project string, cmd *fakeVerifyCommander) *config.GlobalConfigOverride {
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

// TestVerifyIndividual_HappyPath drives AC-18 end to end at the
// onboard package boundary: a well-formed stored credential resolves
// through the real credential-sync read topology and VerifyIndividual
// reports success.
func TestVerifyIndividual_HappyPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cmd := &fakeVerifyCommander{stdout: `{"p-uuid-1": "version = \"1\"\nclient_id = \"cid\"\nclient_secret = \"csec\"\n"}`}

	err := VerifyIndividual(context.Background(), VerifyIndividualParams{
		GlobalOverride: testVerifyGlobalOverride("sync-project", cmd),
		Kind:           "infisical",
		Project:        "uuid-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestVerifyIndividual_MalformedBodyNamesPairSourceAndNature drives
// AC-18b: a malformed stored body produces an ExitVerification error
// naming the (kind, project) pair, its source, and the nature of the
// failure -- never a bare "verification failed" message.
// TestVerifyIndividual_SweepFailureOnOtherPairIsReported drives AC-18's
// "enumerated across the three vault-registry sources" requirement at
// the onboard package boundary: the just-stored target pair resolves
// cleanly, but a different pair declared in a swept registry has a
// malformed body, and VerifyIndividual must still fail -- R11-prefixed
// -- naming that other pair, not report a bare success.
func TestVerifyIndividual_SweepFailureOnOtherPairIsReported(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cmd := &fakeVerifyCommander{stdout: `{
		"p-uuid-1": "version = \"1\"\nclient_id = \"cid\"\nclient_secret = \"csec\"\n",
		"p-other-project": "not = [valid toml"
	}`}

	teamVault := &config.VaultRegistry{
		Provider: &config.VaultProviderConfig{
			Kind:   "infisical",
			Config: map[string]any{"project": "other-project"},
		},
	}

	err := VerifyIndividual(context.Background(), VerifyIndividualParams{
		GlobalOverride: testVerifyGlobalOverride("sync-project", cmd),
		TeamVault:      teamVault,
		Kind:           "infisical",
		Project:        "uuid-1",
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != ExitVerification {
		t.Errorf("Code = %d, want ExitVerification (%d)", ece.Code, ExitVerification)
	}
	for _, want := range []string{"R11", "other-project", "malformed"} {
		if !strings.Contains(ece.Msg, want) {
			t.Errorf("Msg = %q, want it to contain %q", ece.Msg, want)
		}
	}
}

// TestVerifyIndividual_SweepIgnoresUnrelatedCLISessionPair confirms the
// sweep's graceful semantics reach all the way through VerifyIndividual:
// an unrelated declared pair that simply has no stored credential at
// all (falls through to CLI-session) must not fail the check.
func TestVerifyIndividual_SweepIgnoresUnrelatedCLISessionPair(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cmd := &fakeVerifyCommander{stdout: `{"p-uuid-1": "version = \"1\"\nclient_id = \"cid\"\nclient_secret = \"csec\"\n"}`}

	teamVault := &config.VaultRegistry{
		Provider: &config.VaultProviderConfig{
			Kind:   "infisical",
			Config: map[string]any{"project": "other-project"},
		},
	}

	err := VerifyIndividual(context.Background(), VerifyIndividualParams{
		GlobalOverride: testVerifyGlobalOverride("sync-project", cmd),
		TeamVault:      teamVault,
		Kind:           "infisical",
		Project:        "uuid-1",
	})
	if err != nil {
		t.Fatalf("unexpected error -- an unrelated CLI-session-fallback pair must not fail R11: %v", err)
	}
}

func TestVerifyIndividual_MalformedBodyNamesPairSourceAndNature(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cmd := &fakeVerifyCommander{stdout: `{"p-uuid-1": "not = [valid toml"}`}

	err := VerifyIndividual(context.Background(), VerifyIndividualParams{
		GlobalOverride: testVerifyGlobalOverride("sync-project", cmd),
		Kind:           "infisical",
		Project:        "uuid-1",
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != ExitVerification {
		t.Errorf("Code = %d, want ExitVerification (%d)", ece.Code, ExitVerification)
	}
	for _, want := range []string{"R11", "infisical", "uuid-1", "malformed"} {
		if !strings.Contains(ece.Msg, want) {
			t.Errorf("Msg = %q, want it to contain %q", ece.Msg, want)
		}
	}
}
