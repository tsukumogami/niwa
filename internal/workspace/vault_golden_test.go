package workspace

// Characterization of the post-merge fatality decisions for an unreachable
// Infisical provider, recorded before the vault-offline work changes them.
// The resolver-level fixtures live in internal/vault/resolve/testdata/golden;
// the required-key check and the strict-mode gate are unexported here, so
// their text is pinned from this package. Rewrite the fixtures only on
// purpose, with
//
//	go test ./internal/workspace/ -run GoldenVault -update

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/keyreport"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/infisical"
	"github.com/tsukumogami/niwa/internal/vault/resolve"
)

var updateVaultGolden = flag.Bool("update", false, "rewrite the golden fixtures under testdata/golden from the current code")

// goldenResponse403 is the export failure the resolver tolerates today as an
// unreachable provider.
const goldenResponse403 = "error: CallGetRawSecretsV3: Unsuccessful response. Please make sure your secret path, workspace and environment name are all correct\nResponse Code: 403\nMessage: You do not have permission to read secrets in this environment\n"

// goldenExportCommander answers every `infisical export` with a fixed outcome.
type goldenExportCommander struct {
	stdout   string
	stderr   string
	exitCode int
}

func (c goldenExportCommander) Run(_ context.Context, _ string, _ []string) ([]byte, []byte, int, error) {
	return []byte(c.stdout), []byte(c.stderr), c.exitCode, nil
}

// TestGoldenVaultPostMergeChecks runs the resolve-and-merge pipeline, the
// required-key check and the strict-mode gate in the order apply runs them,
// against the 403 stub, and pins what each says. The missing-key case is the
// one shortfall the required-key check refuses today, recorded for contrast.
func TestGoldenVaultPostMergeChecks(t *testing.T) {
	forbidden := goldenExportCommander{stderr: goldenResponse403, exitCode: 1}
	cases := []struct {
		name   string
		stub   goldenExportCommander
		strict bool
	}{
		{name: "required-key-response-403", stub: forbidden},
		{name: "strict-mode-response-403", stub: forbidden, strict: true},
		{name: "required-key-missing-key", stub: goldenExportCommander{stdout: "{}"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			reg := vault.NewRegistry()
			if err := reg.Register(infisical.NewFactory()); err != nil {
				t.Fatalf("register infisical factory: %v", err)
			}
			secrets := config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{"GOLDEN_KEY": {Plain: "vault://GOLDEN_KEY"}},
			}
			if !tc.strict {
				secrets.Required = map[string]string{"GOLDEN_KEY": "golden required key"}
			}
			cfg := &config.WorkspaceConfig{
				Workspace: config.WorkspaceMeta{Name: "golden"},
				Vault: &config.VaultRegistry{Provider: &config.VaultProviderConfig{
					Kind: "infisical",
					Config: map[string]any{
						"project":    "golden-team-project",
						"_commander": tc.stub,
					},
				}},
				Env: config.EnvConfig{Secrets: secrets},
			}

			teamBundle, err := resolve.BuildBundle(ctx, reg, cfg.Vault, "workspace.toml")
			if err != nil {
				t.Fatalf("BuildBundle team: %v", err)
			}
			defer teamBundle.CloseAll()
			personalBundle, err := resolve.BuildBundle(ctx, reg, nil, "niwa.toml")
			if err != nil {
				t.Fatalf("BuildBundle personal: %v", err)
			}
			defer personalBundle.CloseAll()

			keys := keyreport.New()
			var stderr bytes.Buffer
			effective, _, _, err := ResolveAndMergeEffectiveConfig(ctx, cfg, nil, teamBundle, personalBundle,
				EffectiveConfigOptions{Stderr: &stderr, Keys: keys})
			if err != nil {
				t.Fatalf("ResolveAndMergeEffectiveConfig: %v", err)
			}

			requiredErr := checkRequiredKeys(effective, &stderr)
			var strictErr error
			if tc.strict && requiredErr == nil {
				strictErr = strictShortfallError(keys)
			}

			var sb strings.Builder
			fmt.Fprintf(&sb, "scenario: %s\n", tc.name)
			fmt.Fprintf(&sb, "strict mode: %t\n", tc.strict)
			writeGoldenErr(&sb, "required-key check", requiredErr)
			writeGoldenErr(&sb, "strict-mode gate", strictErr)
			sb.WriteString("stderr:\n")
			if stderr.Len() == 0 {
				sb.WriteString("<empty>\n")
			} else {
				sb.WriteString(stderr.String())
			}
			sb.WriteString("key report:\n")
			if r := keyreport.RenderText(keys.Report()); r != "" {
				sb.WriteString(r)
			} else {
				sb.WriteString("<empty>\n")
			}
			checkVaultGolden(t, tc.name, sb.String())
		})
	}
}

func writeGoldenErr(sb *strings.Builder, label string, err error) {
	fmt.Fprintf(sb, "%s:\n", label)
	if err == nil {
		sb.WriteString("<nil>\n")
		return
	}
	sb.WriteString(err.Error())
	sb.WriteString("\n")
}

// checkVaultGolden compares got against testdata/golden/<name>.golden, or
// rewrites it under -update. The system temp directory is normalised so a
// fixture never records a per-run path.
func checkVaultGolden(t *testing.T, name, got string) {
	t.Helper()
	if tmp := os.TempDir(); tmp != "" && tmp != "/" {
		got = strings.ReplaceAll(got, tmp, "<TMP>")
	}
	path := filepath.Join("testdata", "golden", name+".golden")
	if *updateVaultGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing fixture %s; record it with -update", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("fixture %s differs from the current output\n--- want\n%s--- got\n%s", path, want, got)
	}
}
