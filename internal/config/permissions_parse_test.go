package config

import (
	"strings"
	"testing"
)

// TestPermissionsPostureParsesAtEveryLevel pins that the two accepted
// postures keep parsing, unchanged, at every level a workspace or a developer
// can declare them. Existing declarations need no edit.
func TestPermissionsPostureParsesAtEveryLevel(t *testing.T) {
	for _, posture := range []string{"bypass", "ask"} {
		t.Run(posture, func(t *testing.T) {
			ws := `
[workspace]
name = "ws"

[claude.settings]
permissions = "` + posture + `"

[instance.claude.settings]
permissions = "` + posture + `"

[repos.app.claude.settings]
permissions = "` + posture + `"
`
			result, err := Parse([]byte(ws))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			cfg := result.Config
			if got := cfg.Claude.Settings["permissions"].Plain; got != posture {
				t.Errorf("[claude.settings] permissions = %q, want %q", got, posture)
			}
			if cfg.Instance.Claude == nil || cfg.Instance.Claude.Settings["permissions"].Plain != posture {
				t.Errorf("[instance.claude.settings] permissions not parsed as %q", posture)
			}
			repo, ok := cfg.Repos["app"]
			if !ok || repo.Claude == nil || repo.Claude.Settings["permissions"].Plain != posture {
				t.Errorf("[repos.app.claude.settings] permissions not parsed as %q", posture)
			}

			personal := `
[global.claude.settings]
permissions = "` + posture + `"

[workspaces.ws.claude.settings]
permissions = "` + posture + `"
`
			override, err := ParseGlobalConfigOverride([]byte(personal))
			if err != nil {
				t.Fatalf("ParseGlobalConfigOverride: %v", err)
			}
			if override.Global.Claude == nil || override.Global.Claude.Settings["permissions"].Plain != posture {
				t.Errorf("[global.claude.settings] permissions not parsed as %q", posture)
			}
			wsOverride, ok := override.Workspaces["ws"]
			if !ok || wsOverride.Claude == nil || wsOverride.Claude.Settings["permissions"].Plain != posture {
				t.Errorf("[workspaces.ws.claude.settings] permissions not parsed as %q", posture)
			}
		})
	}
}

// TestPermissionsPostureDeprecationWarning pins the notice that the workspace
// posture is deprecated for dispatch: one warning when either level declares
// permissions, naming the machine setting and the file it lives in, and none
// when neither does.
func TestPermissionsPostureDeprecationWarning(t *testing.T) {
	const marker = "permissions is deprecated for dispatch"
	for _, tc := range []struct {
		name      string
		body      string
		wantTable string
	}{
		{
			name:      "workspace level",
			body:      "[claude.settings]\npermissions = \"bypass\"\n",
			wantTable: "[claude.settings]",
		},
		{
			name:      "instance level",
			body:      "[instance.claude.settings]\npermissions = \"ask\"\n",
			wantTable: "[instance.claude.settings]",
		},
		{
			name:      "both levels",
			body:      "[claude.settings]\npermissions = \"ask\"\n\n[instance.claude.settings]\npermissions = \"bypass\"\n",
			wantTable: "[claude.settings]",
		},
		{
			name: "absent",
			body: "[claude.settings]\nremoteControlAtStartup = \"true\"\n",
		},
		{
			name: "instance claude block without permissions",
			body: "[instance.claude.settings]\nremoteControlAtStartup = \"true\"\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Parse([]byte("[workspace]\nname = \"ws\"\n\n" + tc.body))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			var hits []string
			for _, w := range result.Warnings {
				if strings.Contains(w, marker) {
					hits = append(hits, w)
				}
			}
			if tc.wantTable == "" {
				for _, w := range result.Warnings {
					if strings.Contains(w, marker) || strings.Contains(w, "dispatch_permission_mode") {
						t.Errorf("got a deprecation warning with no posture declared: %q", w)
					}
				}
				return
			}
			if len(hits) != 1 {
				t.Fatalf("got %d deprecation warnings, want exactly 1: %v", len(hits), result.Warnings)
			}
			w := hits[0]
			if !strings.HasPrefix(w, tc.wantTable+" ") {
				t.Errorf("warning %q does not name the table %s", w, tc.wantTable)
			}
			for _, want := range []string{"dispatch_permission_mode", "~/.config/niwa/config.toml", "niwa config set dispatch-permission-mode"} {
				if !strings.Contains(w, want) {
					t.Errorf("warning %q does not contain %q", w, want)
				}
			}
		})
	}
}
