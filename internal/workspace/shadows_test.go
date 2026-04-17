package workspace

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/secret"
)

// TestShadowHasNoSecretValueField is the compile-time-style invariant
// that a Shadow may never carry a secret.Value. If this test fails,
// the diagnostic pipeline is one commit away from leaking plaintext
// into state.json and stderr. Fix the struct, not the test.
func TestShadowHasNoSecretValueField(t *testing.T) {
	var zero Shadow
	ty := reflect.TypeOf(zero)
	secretValueType := reflect.TypeOf(secret.Value{})
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		if f.Type == secretValueType {
			t.Fatalf("Shadow.%s is secret.Value; Shadow MUST NOT carry secret material", f.Name)
		}
	}
}

func TestDetectShadowsNilInputs(t *testing.T) {
	if got := DetectShadows(nil, nil); got != nil {
		t.Errorf("DetectShadows(nil, nil) = %v, want nil", got)
	}
	team := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws"}}
	if got := DetectShadows(team, nil); got != nil {
		t.Errorf("DetectShadows(team, nil) = %v, want nil", got)
	}
	overlay := &config.GlobalConfigOverride{}
	if got := DetectShadows(nil, overlay); got != nil {
		t.Errorf("DetectShadows(nil, overlay) = %v, want nil", got)
	}
}

func TestDetectShadowsEnvVars(t *testing.T) {
	team := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Env: config.EnvConfig{
			Vars: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"FOO": {Plain: "team"},
					"BAR": {Plain: "team-bar"},
				},
			},
		},
	}
	overlay := &config.GlobalConfigOverride{
		Global: config.GlobalOverride{
			Env: config.EnvConfig{
				Vars: config.EnvVarsTable{
					Values: map[string]config.MaybeSecret{
						"FOO":  {Plain: "overlay"},
						"ONLY": {Plain: "overlay-only"},
					},
				},
			},
		},
	}

	got := DetectShadows(team, overlay)
	if len(got) != 1 {
		t.Fatalf("want 1 shadow, got %d: %+v", len(got), got)
	}
	if got[0].Kind != "env-var" {
		t.Errorf("Kind = %q, want env-var", got[0].Kind)
	}
	if got[0].Name != "FOO" {
		t.Errorf("Name = %q, want FOO", got[0].Name)
	}
	if got[0].TeamSource != "workspace.toml" {
		t.Errorf("TeamSource = %q, want workspace.toml", got[0].TeamSource)
	}
	if got[0].PersonalSource != "niwa.toml" {
		t.Errorf("PersonalSource = %q, want niwa.toml", got[0].PersonalSource)
	}
	if got[0].Layer != "personal-overlay" {
		t.Errorf("Layer = %q, want personal-overlay", got[0].Layer)
	}
}

func TestDetectShadowsEnvSecrets(t *testing.T) {
	team := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"API_TOKEN": {Plain: "vault://API_TOKEN"},
				},
			},
		},
	}
	overlay := &config.GlobalConfigOverride{
		Global: config.GlobalOverride{
			Env: config.EnvConfig{
				Secrets: config.EnvVarsTable{
					Values: map[string]config.MaybeSecret{
						"API_TOKEN": {Plain: "vault://other/API_TOKEN"},
					},
				},
			},
		},
	}
	got := DetectShadows(team, overlay)
	if len(got) != 1 || got[0].Kind != "env-secret" || got[0].Name != "API_TOKEN" {
		t.Fatalf("want one env-secret API_TOKEN shadow, got %+v", got)
	}
}

func TestDetectShadowsClaudeEnv(t *testing.T) {
	team := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Claude: config.ClaudeConfig{
			Env: config.ClaudeEnvConfig{
				Vars: config.EnvVarsTable{
					Values: map[string]config.MaybeSecret{"LOG_LEVEL": {Plain: "debug"}},
				},
				Secrets: config.EnvVarsTable{
					Values: map[string]config.MaybeSecret{"GH_TOKEN": {Plain: "vault://GH_TOKEN"}},
				},
			},
		},
	}
	overlay := &config.GlobalConfigOverride{
		Global: config.GlobalOverride{
			Claude: &config.ClaudeOverride{
				Env: config.ClaudeEnvConfig{
					Vars: config.EnvVarsTable{
						Values: map[string]config.MaybeSecret{"LOG_LEVEL": {Plain: "trace"}},
					},
					Secrets: config.EnvVarsTable{
						Values: map[string]config.MaybeSecret{"GH_TOKEN": {Plain: "personal"}},
					},
				},
			},
		},
	}

	got := DetectShadows(team, overlay)
	if len(got) != 2 {
		t.Fatalf("want 2 shadows, got %d: %+v", len(got), got)
	}
	// Sorted by (Kind, Name): claude-env-secret < claude-env-var.
	if got[0].Kind != "claude-env-secret" || got[0].Name != "GH_TOKEN" {
		t.Errorf("shadow[0] = %+v, want claude-env-secret GH_TOKEN", got[0])
	}
	if got[1].Kind != "claude-env-var" || got[1].Name != "LOG_LEVEL" {
		t.Errorf("shadow[1] = %+v, want claude-env-var LOG_LEVEL", got[1])
	}
}

func TestDetectShadowsFiles(t *testing.T) {
	team := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Files:     map[string]string{"src/a.md": "dest/a.md"},
	}
	overlay := &config.GlobalConfigOverride{
		Global: config.GlobalOverride{
			Files: map[string]string{"src/a.md": "dest/b.md"},
		},
	}
	got := DetectShadows(team, overlay)
	if len(got) != 1 || got[0].Kind != "files" || got[0].Name != "src/a.md" {
		t.Fatalf("want one files shadow on src/a.md, got %+v", got)
	}
}

func TestDetectShadowsSettings(t *testing.T) {
	team := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Claude: config.ClaudeConfig{
			Settings: config.SettingsConfig{
				"permissions": {Plain: "ask"},
			},
		},
	}
	overlay := &config.GlobalConfigOverride{
		Global: config.GlobalOverride{
			Claude: &config.ClaudeOverride{
				Settings: config.SettingsConfig{
					"permissions": {Plain: "bypass"},
				},
			},
		},
	}
	got := DetectShadows(team, overlay)
	if len(got) != 1 || got[0].Kind != "settings" || got[0].Name != "permissions" {
		t.Fatalf("want one settings shadow on permissions, got %+v", got)
	}
}

func TestDetectShadowsPerWorkspaceScope(t *testing.T) {
	team := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "my-ws"},
		Env: config.EnvConfig{
			Vars: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{"SCOPED": {Plain: "team"}},
			},
		},
	}

	// Overlay declares SCOPED only under [workspaces.other-ws]; since
	// team.Workspace.Name is "my-ws", the scoped entry MUST NOT be
	// treated as a shadow.
	overlay := &config.GlobalConfigOverride{
		Workspaces: map[string]config.GlobalOverride{
			"other-ws": {
				Env: config.EnvConfig{
					Vars: config.EnvVarsTable{
						Values: map[string]config.MaybeSecret{"SCOPED": {Plain: "overlay"}},
					},
				},
			},
		},
	}
	if got := DetectShadows(team, overlay); got != nil {
		t.Errorf("overlay scoped to other-ws must not shadow my-ws, got %+v", got)
	}

	// Now put the same entry under [workspaces.my-ws]: it MUST be
	// detected as a shadow.
	overlay = &config.GlobalConfigOverride{
		Workspaces: map[string]config.GlobalOverride{
			"my-ws": {
				Env: config.EnvConfig{
					Vars: config.EnvVarsTable{
						Values: map[string]config.MaybeSecret{"SCOPED": {Plain: "overlay"}},
					},
				},
			},
		},
	}
	got := DetectShadows(team, overlay)
	if len(got) != 1 || got[0].Name != "SCOPED" {
		t.Fatalf("workspaces.my-ws entry should shadow team SCOPED, got %+v", got)
	}
}

func TestDetectShadowsNeverEmitsSecretValues(t *testing.T) {
	// Build a team + overlay with a resolved secret in Secret (not
	// Plain) to confirm the detection code path never reaches into
	// MaybeSecret.Secret.
	resolved := secret.New([]byte("super-secret-bytes-not-in-output"), secret.Origin{
		ProviderName: "fake",
		Key:          "GH_TOKEN",
	})
	team := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"GH_TOKEN": {Secret: resolved},
				},
			},
		},
	}
	overlay := &config.GlobalConfigOverride{
		Global: config.GlobalOverride{
			Env: config.EnvConfig{
				Secrets: config.EnvVarsTable{
					Values: map[string]config.MaybeSecret{
						"GH_TOKEN": {Plain: "overlay-plaintext"},
					},
				},
			},
		},
	}
	got := DetectShadows(team, overlay)
	if len(got) != 1 {
		t.Fatalf("want one shadow, got %d", len(got))
	}
	// Defensive assertion: the emitted Shadow carries only the key
	// name, not any part of the resolved plaintext.
	for _, sh := range got {
		if sh.Name != "GH_TOKEN" {
			t.Errorf("Name = %q, want GH_TOKEN", sh.Name)
		}
		// Serialize the shadow to a string form and assert the
		// plaintext bytes are absent.
		rendered := sh.Kind + " " + sh.Name + " " + sh.TeamSource + " " + sh.PersonalSource + " " + sh.Layer
		if strings.Contains(rendered, "super-secret-bytes-not-in-output") {
			t.Errorf("Shadow rendered form leaked secret bytes: %s", rendered)
		}
	}
}
