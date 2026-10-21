package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/tsukumogami/niwa/internal/authority"
)

func workspaceTOMLWithAuthority(value string) string {
	return fmt.Sprintf("[workspace]\nname = \"ws\"\nauthority_level = %q\n", value)
}

func TestWorkspaceAuthorityLevel_Parse(t *testing.T) {
	strict := authority.Strict
	supervised := authority.Supervised
	cases := []struct {
		name string
		toml string
		want *authority.Level
	}{
		{"key absent", "[workspace]\nname = \"ws\"\n", nil},
		{"empty is unset", workspaceTOMLWithAuthority(""), nil},
		{"whitespace only is unset", workspaceTOMLWithAuthority("   "), nil},
		{"strict", workspaceTOMLWithAuthority("strict"), &strict},
		{"surrounding whitespace is trimmed", workspaceTOMLWithAuthority(" strict "), &strict},
		{"supervised", workspaceTOMLWithAuthority("supervised"), &supervised},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Parse([]byte(tc.toml))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			for _, w := range res.Warnings {
				if strings.Contains(w, "authority_level") {
					t.Errorf("authority_level reported as unknown: %q", w)
				}
			}
			got, err := res.Config.AuthorityLevel()
			if err != nil {
				t.Fatalf("AuthorityLevel: %v", err)
			}
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("AuthorityLevel() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestWorkspaceAuthorityLevel_InvalidFailsTheLoad holds that an invalid value
// stops the load with the typed error, rather than leaving the workspace at
// the widest level, and that the message is enough to fix the file.
func TestWorkspaceAuthorityLevel_InvalidFailsTheLoad(t *testing.T) {
	for _, value := range []string{"Strict", "STRICT", "loose", "bypass"} {
		t.Run(value, func(t *testing.T) {
			res, err := Parse([]byte(workspaceTOMLWithAuthority(value)))
			if err == nil {
				t.Fatalf("Parse accepted authority_level = %q: %+v", value, res.Config.Workspace)
			}
			var ie *InvalidAuthorityLevelError
			if !errors.As(err, &ie) {
				t.Fatalf("error %T (%v) is not an *InvalidAuthorityLevelError", err, err)
			}
			if ie.Table != "workspace" || ie.Value != value {
				t.Errorf("InvalidAuthorityLevelError = %+v, want table workspace value %q", ie, value)
			}
			msg := err.Error()
			for _, part := range append([]string{"authority_level", `"` + value + `"`}, authority.Names()...) {
				if !strings.Contains(msg, part) {
					t.Errorf("error %q does not mention %q", msg, part)
				}
			}
		})
	}
}

// TestWorkspaceAuthorityLevel_LoadFromFile goes through Load, the path apply,
// dispatch and `niwa authority show` take, and checks errors.As still matches
// once the error has been wrapped by a caller.
func TestWorkspaceAuthorityLevel_LoadFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.toml")
	if err := os.WriteFile(path, []byte(workspaceTOMLWithAuthority("Strict")), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	wrapped := fmt.Errorf("loading %s: %w", path, err)
	var ie *InvalidAuthorityLevelError
	if !errors.As(wrapped, &ie) {
		t.Fatalf("wrapped load error %v does not match *InvalidAuthorityLevelError", wrapped)
	}
}

func TestInvalidAuthorityLevelError_Text(t *testing.T) {
	err := &InvalidAuthorityLevelError{Table: "workspace", Value: "Strict"}
	want := `invalid [workspace] authority_level "Strict"; accepted values are autonomous, supervised, strict`
	if err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestWorkspaceConfig_AuthorityLevel_NilAndInMemory(t *testing.T) {
	var c *WorkspaceConfig
	if got, err := c.AuthorityLevel(); got != nil || err != nil {
		t.Fatalf("nil receiver AuthorityLevel() = %v, %v; want nil, nil", got, err)
	}
	// A config built in memory never went through validate(), so the
	// accessor is where a bad value has to fail.
	c = &WorkspaceConfig{Workspace: WorkspaceMeta{Name: "ws", AuthorityLevel: "Supervised"}}
	var ie *InvalidAuthorityLevelError
	if _, err := c.AuthorityLevel(); !errors.As(err, &ie) {
		t.Fatalf("in-memory invalid value: error = %v, want *InvalidAuthorityLevelError", err)
	}
}

func TestGlobalConfig_AuthorityLevel(t *testing.T) {
	strict := authority.Strict
	autonomous := authority.Autonomous
	cases := []struct {
		name    string
		in      string
		want    *authority.Level
		wantErr bool
	}{
		{"key absent", "[global]\nclone_protocol = \"ssh\"\n", nil, false},
		{"no global section", "[registry]\n", nil, false},
		{"empty string", "[global]\nauthority_level = \"\"\n", nil, false},
		{"whitespace only", "[global]\nauthority_level = \"   \"\n", nil, false},
		{"valid", "[global]\nauthority_level = \"strict\"\n", &strict, false},
		{"valid with whitespace", "[global]\nauthority_level = \" autonomous \"\n", &autonomous, false},
		// The field decodes raw; the accessor is where an invalid value fails.
		{"invalid case", "[global]\nauthority_level = \"STRICT\"\n", nil, true},
		{"invalid word", "[global]\nauthority_level = \"loose\"\n", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseGlobalConfig([]byte(tc.in))
			if err != nil {
				t.Fatalf("ParseGlobalConfig: %v", err)
			}
			got, err := cfg.AuthorityLevel()
			if tc.wantErr {
				var ie *InvalidAuthorityLevelError
				if !errors.As(err, &ie) {
					t.Fatalf("AuthorityLevel() error = %v, want *InvalidAuthorityLevelError", err)
				}
				if ie.Table != "global" {
					t.Errorf("error names table %q, want global", ie.Table)
				}
				if !strings.Contains(err.Error(), "[global] authority_level") {
					t.Errorf("error %q does not name [global] authority_level", err)
				}
				if got != nil {
					t.Errorf("AuthorityLevel() returned %v alongside an error", *got)
				}
				return
			}
			if err != nil {
				t.Fatalf("AuthorityLevel(): unexpected error %v", err)
			}
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("AuthorityLevel() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestGlobalConfig_AuthorityLevel_NilReceiver: a host config niwa could not
// load reads as unset, not as a panic.
func TestGlobalConfig_AuthorityLevel_NilReceiver(t *testing.T) {
	var g *GlobalConfig
	if got, err := g.AuthorityLevel(); got != nil || err != nil {
		t.Fatalf("nil receiver AuthorityLevel() = %v, %v; want nil, nil", got, err)
	}
}

func TestGlobalSettings_AuthorityLevel_Encode(t *testing.T) {
	encode := func(cfg *GlobalConfig) string {
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
			t.Fatalf("encode: %v", err)
		}
		return buf.String()
	}
	if out := encode(&GlobalConfig{}); strings.Contains(out, "authority_level") {
		t.Fatalf("unset field should be omitted, got:\n%s", out)
	}
	out := encode(&GlobalConfig{Global: GlobalSettings{AuthorityLevel: "strict"}})
	if !strings.Contains(out, `authority_level = "strict"`) {
		t.Fatalf("set field missing from encoding:\n%s", out)
	}
}
