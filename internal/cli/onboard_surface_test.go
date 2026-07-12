package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// onboardSurfaceFiles names every source file that makes up the
// `niwa onboard` command surface -- the cobra command declaration
// (flags, Use, Short, Long) and the config-sourcing wiring that feeds
// it. AC-23 requires none of these ever carry a baked-in org-,
// workspace-, or project-specific identifier: every such value MUST
// be read from the team workspace config or the personal-overlay
// config at runtime instead.
var onboardSurfaceFiles = []string{
	"onboard.go",
	"onboard_config.go",
}

// uuidLiteralPattern matches a bare UUID -- the shape every Infisical
// project id and identity id takes (see docs/guides/machine-identity-
// vault-sync.md's own example project-uuid). A UUID literal appearing
// directly in the command surface source (as opposed to a variable
// read from config.VaultProviderConfig.Config) would mean someone
// baked in a specific project/identity id rather than sourcing it.
var uuidLiteralPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// TestOnboardCommandSurface_NoUUIDLiteral is the AC-23 source grep:
// no project-id/identity-id-shaped literal (a UUID) appears anywhere
// in the command surface source. Every real project/identity id this
// surface handles is read out of config.VaultProviderConfig.Config at
// runtime (loadOnboardConfig), never typed as a Go string literal.
func TestOnboardCommandSurface_NoUUIDLiteral(t *testing.T) {
	for _, path := range onboardSurfaceFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if m := uuidLiteralPattern.Find(data); m != nil {
			t.Errorf("%s contains a UUID-shaped literal %q -- project/identity ids must be config-sourced (R14/AC-23), never a baked-in constant", path, m)
		}
	}
}

// TestOnboardCommandSurface_EveryFlagIsBoolean pins the flag-level
// half of AC-23 structurally rather than by string grep: today every
// `niwa onboard` flag is a boolean toggle (--team, --individual,
// --same-login, --split-login, --json, --accept-api-url), so there is
// no string/int flag surface that could carry a hardcoded org-,
// workspace-, or project-specific default value in the first place.
// A future flag addition that fails this test is a deliberate signal
// to re-examine whether its default value (if any) is generic --
// this test should gain an explicit, reasoned exemption at that point,
// not be silently loosened.
func TestOnboardCommandSurface_EveryFlagIsBoolean(t *testing.T) {
	onboardCmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Value.Type() != "bool" {
			t.Errorf("onboard flag --%s is type %q, want \"bool\" -- a non-boolean flag risks carrying a baked-in default identifier (R14/AC-23); if a string/int flag is genuinely needed, its default must stay empty/zero and this test updated with a deliberate, reasoned exemption", f.Name, f.Value.Type())
		}
	})
}

// TestOnboardCommandSurface_HelpTextHasNoDenylistedIdentifier greps
// the command's own Long/Short help text and every flag's usage
// string for a small denylist of identifier-shaped substrings that
// have shown up in this design's own worked examples (the docs guide's
// sample project uuid, and the sample org/workspace names used
// throughout the PRD/DESIGN's illustrative prose) -- a regression
// where one of those leaked from documentation into the actual
// command text would be exactly the AC-23 violation this issue guards
// against.
func TestOnboardCommandSurface_HelpTextHasNoDenylistedIdentifier(t *testing.T) {
	denylist := []string{
		"550e8400-e29b-41d4-a716-446655440000", // docs/guides/machine-identity-vault-sync.md's example project uuid
		"team-a",                               // the guide's example org label
	}
	texts := []string{onboardCmd.Short, onboardCmd.Long}
	onboardCmd.Flags().VisitAll(func(f *pflag.Flag) {
		texts = append(texts, f.Usage)
	})
	for _, text := range texts {
		for _, bad := range denylist {
			if strings.Contains(text, bad) {
				t.Errorf("command surface text %q contains denylisted identifier %q (R14/AC-23)", text, bad)
			}
		}
	}
}
