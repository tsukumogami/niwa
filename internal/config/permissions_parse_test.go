package config

import "testing"

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
