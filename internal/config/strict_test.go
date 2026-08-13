package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveStrictSecretsPrecedence covers the full cross product of the two
// speakers. The rows that carry the rule are the ones where the flag is
// present and false: a bool flag cannot distinguish "absent" from "false" by
// value, so a precedence keyed on the value would make --strict-secrets=false
// a no-op against a strict workspace -- which is the one thing that flag form
// exists to do.
func TestResolveStrictSecretsPrecedence(t *testing.T) {
	tests := []struct {
		name        string
		setting     *bool
		flagChanged bool
		flagValue   bool
		want        bool
	}{
		{"neither speaks is tolerant", nil, false, false, false},
		{"setting true, no flag", boolPtr(true), false, false, true},
		{"setting false, no flag", boolPtr(false), false, false, false},
		{"flag true over unset setting", nil, true, true, true},
		{"flag false over unset setting", nil, true, false, false},
		{"flag true over setting false", boolPtr(false), true, true, true},
		{"flag false de-escalates setting true", boolPtr(true), true, false, false},
		{"flag true agrees with setting true", boolPtr(true), true, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveStrictSecrets(tt.setting, tt.flagChanged, tt.flagValue); got != tt.want {
				t.Errorf("ResolveStrictSecrets(%v, %v, %v) = %v, want %v",
					tt.setting, tt.flagChanged, tt.flagValue, got, tt.want)
			}
		})
	}
}

// TestStrictSecretsIsTriState pins that an explicitly-false setting is
// distinguishable from an absent one after a real TOML decode. A plain bool
// field would decode both to false and silently collapse the two rows above
// that depend on telling them apart.
func TestStrictSecretsIsTriState(t *testing.T) {
	unset := parseWorkspaceTOML(t, "[workspace]\nname = \"ws\"\n")
	if unset.Workspace.StrictSecrets != nil {
		t.Errorf("absent strict_secrets decoded to %v, want nil", *unset.Workspace.StrictSecrets)
	}

	off := parseWorkspaceTOML(t, "[workspace]\nname = \"ws\"\nstrict_secrets = false\n")
	if off.Workspace.StrictSecrets == nil {
		t.Fatal("explicit strict_secrets = false decoded to nil, which is indistinguishable from unset")
	}
	if *off.Workspace.StrictSecrets {
		t.Error("strict_secrets = false decoded to true")
	}

	on := parseWorkspaceTOML(t, "[workspace]\nname = \"ws\"\nstrict_secrets = true\n")
	if on.Workspace.StrictSecrets == nil || !*on.Workspace.StrictSecrets {
		t.Error("strict_secrets = true did not decode to true")
	}
}

// TestOverlayStrictSecretsIsATombstone: an overlay that sets strict mode
// decodes, warns, and carries the value nowhere anything reads. The
// no-effect half of R13 is asserted in internal/workspace, against the merge.
func TestOverlayStrictSecretsIsATombstone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace-overlay.toml")
	if err := os.WriteFile(path, []byte("[workspace]\nstrict_secrets = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	o, err := ParseOverlay(path)
	if err != nil {
		t.Fatalf("ParseOverlay: %v", err)
	}
	warnings := o.TombstoneWarnings()
	if len(warnings) != 1 {
		t.Fatalf("TombstoneWarnings() = %v, want exactly one warning", warnings)
	}
	if !strings.Contains(warnings[0], "strict_secrets") || !strings.Contains(warnings[0], "ignored") {
		t.Errorf("warning does not say strict_secrets is ignored: %q", warnings[0])
	}
}

// TestOverlayWithoutWorkspaceStanzaIsSilent: the tombstone must not turn every
// overlay into a warning-emitting one.
func TestOverlayWithoutWorkspaceStanzaIsSilent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace-overlay.toml")
	if err := os.WriteFile(path, []byte("[claude.settings]\nfoo = \"bar\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o, err := ParseOverlay(path)
	if err != nil {
		t.Fatalf("ParseOverlay: %v", err)
	}
	if w := o.TombstoneWarnings(); len(w) != 0 {
		t.Errorf("TombstoneWarnings() = %v, want none", w)
	}
}

func parseWorkspaceTOML(t *testing.T, body string) *WorkspaceConfig {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return result.Config
}
