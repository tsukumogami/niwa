package config

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestDispatchPermissionModes_Order(t *testing.T) {
	want := []string{"default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"}
	if !reflect.DeepEqual(DispatchPermissionModes, want) {
		t.Fatalf("DispatchPermissionModes = %v, want %v", DispatchPermissionModes, want)
	}
}

func TestParseDispatchPermissionMode(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"default", "default", "default", false},
		{"acceptEdits", "acceptEdits", "acceptEdits", false},
		{"plan", "plan", "plan", false},
		{"auto", "auto", "auto", false},
		{"dontAsk", "dontAsk", "dontAsk", false},
		{"bypassPermissions", "bypassPermissions", "bypassPermissions", false},
		{"surrounding whitespace is trimmed", "  \tauto\n ", "auto", false},
		{"empty is unset", "", "", false},
		{"whitespace only is unset", " \t\n ", "", false},
		{"case must match", "Auto", "", true},
		{"all caps rejected", "BYPASSPERMISSIONS", "", true},
		{"lowercased camel rejected", "acceptedits", "", true},
		{"typo rejected", "atuo", "", true},
		{"workspace posture spelling rejected", "bypass", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDispatchPermissionMode(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDispatchPermissionMode(%q) = %q, nil; want error", tc.in, got)
				}
				if got != "" {
					t.Fatalf("ParseDispatchPermissionMode(%q) returned %q alongside an error; want \"\"", tc.in, got)
				}
				msg := err.Error()
				for _, part := range append([]string{"dispatch_permission_mode", `"` + strings.TrimSpace(tc.in) + `"`}, DispatchPermissionModes...) {
					if !strings.Contains(msg, part) {
						t.Fatalf("error %q does not mention %q", msg, part)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDispatchPermissionMode(%q): unexpected error %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseDispatchPermissionMode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseDispatchPermissionMode_ErrorText pins the exact message dispatch and
// the setter surface to the developer.
func TestParseDispatchPermissionMode_ErrorText(t *testing.T) {
	_, err := ParseDispatchPermissionMode("atuo")
	want := `invalid [global] dispatch_permission_mode "atuo"; accepted values are default, acceptEdits, plan, auto, dontAsk, bypassPermissions`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestGlobalConfig_DispatchPermissionMode(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"key absent", "[global]\nclone_protocol = \"ssh\"\n", "", false},
		{"no global section", "[registry]\n", "", false},
		{"empty string", "[global]\ndispatch_permission_mode = \"\"\n", "", false},
		{"whitespace only", "[global]\ndispatch_permission_mode = \"   \"\n", "", false},
		{"valid", "[global]\ndispatch_permission_mode = \"auto\"\n", "auto", false},
		{"valid with whitespace", "[global]\ndispatch_permission_mode = \" bypassPermissions \"\n", "bypassPermissions", false},
		// The field decodes raw; the accessor is where an invalid value fails.
		{"invalid", "[global]\ndispatch_permission_mode = \"Auto\"\n", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseGlobalConfig([]byte(tc.in))
			if err != nil {
				t.Fatalf("ParseGlobalConfig: %v", err)
			}
			got, err := cfg.DispatchPermissionMode()
			if (err != nil) != tc.wantErr {
				t.Fatalf("DispatchPermissionMode() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("DispatchPermissionMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGlobalConfig_DispatchPermissionMode_NilReceiver: a host config niwa could
// not load reads as unset, not as a panic.
func TestGlobalConfig_DispatchPermissionMode_NilReceiver(t *testing.T) {
	var g *GlobalConfig
	got, err := g.DispatchPermissionMode()
	if err != nil || got != "" {
		t.Fatalf("nil receiver DispatchPermissionMode() = %q, %v; want \"\", nil", got, err)
	}
}

func TestGlobalSettings_DispatchPermissionMode_Encode(t *testing.T) {
	encode := func(cfg *GlobalConfig) string {
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
			t.Fatalf("encode: %v", err)
		}
		return buf.String()
	}
	if out := encode(&GlobalConfig{}); strings.Contains(out, "dispatch_permission_mode") {
		t.Fatalf("unset field should be omitted, got:\n%s", out)
	}
	out := encode(&GlobalConfig{Global: GlobalSettings{DispatchPermissionMode: "plan"}})
	if !strings.Contains(out, `dispatch_permission_mode = "plan"`) {
		t.Fatalf("set field missing from encoding:\n%s", out)
	}
}
