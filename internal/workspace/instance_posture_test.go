package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

func plainSetting(v string) config.MaybeSecret { return config.MaybeSecret{Plain: v} }

func TestInstancePermissionsPosture(t *testing.T) {
	tests := []struct {
		name      string
		workspace config.SettingsConfig
		instance  config.SettingsConfig
		want      string
	}{
		{name: "absent key", want: ""},
		{
			name:      "unrecognized value",
			workspace: config.SettingsConfig{"permissions": plainSetting("bypassPermissions")},
			want:      "",
		},
		{
			name:      "bypass",
			workspace: config.SettingsConfig{"permissions": plainSetting("bypass")},
			want:      "bypass",
		},
		{
			name:      "ask",
			workspace: config.SettingsConfig{"permissions": plainSetting("ask")},
			want:      "ask",
		},
		{
			name:      "instance ask over workspace bypass",
			workspace: config.SettingsConfig{"permissions": plainSetting("bypass")},
			instance:  config.SettingsConfig{"permissions": plainSetting("ask")},
			want:      "ask",
		},
		{
			name:      "instance bypass over workspace ask",
			workspace: config.SettingsConfig{"permissions": plainSetting("ask")},
			instance:  config.SettingsConfig{"permissions": plainSetting("bypass")},
			want:      "bypass",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.WorkspaceConfig{
				Workspace: config.WorkspaceMeta{Name: "test"},
				Claude:    config.ClaudeConfig{Settings: tc.workspace},
			}
			if tc.instance != nil {
				cfg.Instance.Claude = &config.ClaudeOverride{Settings: tc.instance}
			}
			if got := instancePermissionsPosture(cfg); got != tc.want {
				t.Errorf("instancePermissionsPosture() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestInstancePermissionsPostureWorkspaceOverlay pins the workspace overlay's
// place in the precedence: the overlay only fills keys the workspace leaves
// unset, so it never outranks a workspace declaration.
func TestInstancePermissionsPostureWorkspaceOverlay(t *testing.T) {
	tests := []struct {
		name      string
		workspace config.SettingsConfig
		want      string
	}{
		{
			name:      "workspace ask wins over overlay bypass",
			workspace: config.SettingsConfig{"permissions": plainSetting("ask")},
			want:      "ask",
		},
		{
			name: "overlay bypass fills an undeclared workspace",
			want: "bypass",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := &config.WorkspaceConfig{
				Workspace: config.WorkspaceMeta{Name: "test"},
				Claude:    config.ClaudeConfig{Settings: tc.workspace},
			}
			overlay := &config.WorkspaceOverlay{
				Claude: config.OverlayClaudeConfig{
					Settings: config.SettingsConfig{"permissions": plainSetting("bypass")},
				},
			}
			merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
			if err != nil {
				t.Fatalf("MergeWorkspaceOverlay: %v", err)
			}
			if got := instancePermissionsPosture(merged); got != tc.want {
				t.Errorf("instancePermissionsPosture() = %q, want %q", got, tc.want)
			}
		})
	}
}

const postureWorkspaceName = "posture-ws"

const postureBaseTOML = `
[workspace]
name = "posture-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]
`

// postureFixture is a temporary workspace with one cloned-looking repo,
// ready for real Applier.Create and Applier.Apply runs.
type postureFixture struct {
	applier      *Applier
	niwaDir      string
	instanceRoot string
}

// newPostureFixture builds the workspace. A non-empty personalTOML becomes the
// personal overlay's niwa.toml.
func newPostureFixture(t *testing.T, personalTOML string) *postureFixture {
	t.Helper()
	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	instanceRoot := filepath.Join(tmpDir, postureWorkspaceName)
	if err := os.MkdirAll(filepath.Join(instanceRoot, "default", "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(&mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}},
		},
	})
	applier.Cloner = &Cloner{}

	if personalTOML != "" {
		globalDir := filepath.Join(tmpDir, "global-config")
		if err := os.MkdirAll(globalDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(globalDir, "niwa.toml"), []byte(personalTOML), 0o644); err != nil {
			t.Fatal(err)
		}
		applier.GlobalConfigDir = globalDir
	}

	return &postureFixture{applier: applier, niwaDir: niwaDir, instanceRoot: instanceRoot}
}

// writeConfig writes the base workspace config plus extraTOML and loads it.
func (f *postureFixture) writeConfig(t *testing.T, extraTOML string) *config.WorkspaceConfig {
	t.Helper()
	path := filepath.Join(f.niwaDir, "workspace.toml")
	if err := os.WriteFile(path, []byte(postureBaseTOML+extraTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := config.Load(path)
	if err != nil {
		t.Fatalf("loading workspace config: %v", err)
	}
	return result.Config
}

func (f *postureFixture) create(t *testing.T, cfg *config.WorkspaceConfig) {
	t.Helper()
	workspaceRoot := filepath.Dir(f.instanceRoot)
	if _, err := f.applier.Create(context.Background(), cfg, f.niwaDir, workspaceRoot, cfg.Workspace.Name); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func (f *postureFixture) recordedPosture(t *testing.T) string {
	t.Helper()
	state, err := LoadState(f.instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return state.ClaudePermissions
}

// TestCreateRecordsPermissionsPosture runs a real Create for each declaration
// and reads the posture back from the saved state file.
func TestCreateRecordsPermissionsPosture(t *testing.T) {
	tests := []struct {
		name      string
		workspace string
		personal  string
		want      string
	}{
		{
			name:      "workspace bypass",
			workspace: "\n[claude.settings]\npermissions = \"bypass\"\n",
			want:      "bypass",
		},
		{
			name:      "workspace ask",
			workspace: "\n[claude.settings]\npermissions = \"ask\"\n",
			want:      "ask",
		},
		{
			name: "undeclared",
			want: "",
		},
		{
			name: "instance ask over workspace bypass",
			workspace: "\n[claude.settings]\npermissions = \"bypass\"\n" +
				"\n[instance.claude.settings]\npermissions = \"ask\"\n",
			want: "ask",
		},
		{
			name: "instance bypass over workspace ask",
			workspace: "\n[claude.settings]\npermissions = \"ask\"\n" +
				"\n[instance.claude.settings]\npermissions = \"bypass\"\n",
			want: "bypass",
		},
		{
			// Per-repo overrides shape only that repo's settings document,
			// never the instance posture.
			name: "per-repo ask leaves workspace bypass",
			workspace: "\n[claude.settings]\npermissions = \"bypass\"\n" +
				"\n[repos.app.claude.settings]\npermissions = \"ask\"\n",
			want: "bypass",
		},
		{
			name:     "personal overlay bypass over undeclared workspace",
			personal: "[workspaces.posture-ws.claude.settings]\npermissions = \"bypass\"\n",
			want:     "bypass",
		},
		{
			name:      "personal overlay ask over workspace bypass",
			workspace: "\n[claude.settings]\npermissions = \"bypass\"\n",
			personal:  "[workspaces.posture-ws.claude.settings]\npermissions = \"ask\"\n",
			want:      "ask",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newPostureFixture(t, tc.personal)
			f.create(t, f.writeConfig(t, tc.workspace))
			if got := f.recordedPosture(t); got != tc.want {
				t.Errorf("ClaudePermissions = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCreateOmitsUndeclaredPostureKey checks that an undeclared posture leaves
// no claude_permissions key in the raw state file, so the file an older binary
// reads is unchanged.
func TestCreateOmitsUndeclaredPostureKey(t *testing.T) {
	f := newPostureFixture(t, "")
	f.create(t, f.writeConfig(t, ""))

	raw, err := os.ReadFile(filepath.Join(f.instanceRoot, ".niwa", StateFile))
	if err != nil {
		t.Fatalf("reading instance state: %v", err)
	}
	if strings.Contains(string(raw), "claude_permissions") {
		t.Errorf("instance.json carries a claude_permissions key for an undeclared posture:\n%s", raw)
	}
}

// TestApplyRecomputesPermissionsPosture checks that Apply records the posture
// of the current config rather than carrying the earlier state's value.
func TestApplyRecomputesPermissionsPosture(t *testing.T) {
	f := newPostureFixture(t, "")
	f.create(t, f.writeConfig(t, "\n[claude.settings]\npermissions = \"bypass\"\n"))
	if got := f.recordedPosture(t); got != "bypass" {
		t.Fatalf("after Create: ClaudePermissions = %q, want %q", got, "bypass")
	}

	cfg := f.writeConfig(t, "")
	if err := f.applier.Apply(context.Background(), cfg, f.niwaDir, f.instanceRoot); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := f.recordedPosture(t); got != "" {
		t.Errorf("after Apply with no declaration: ClaudePermissions = %q, want empty", got)
	}
}
