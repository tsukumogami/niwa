package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// TestInstallWorkspaceRootSettings_NoRemoteControlByDefault is the AC2 guard: the
// materializer that the interactive, ephemeral SessionStart-hook, and `niwa apply`
// paths use never emits remoteControlAtStartup on its own. The host dispatch
// preference is not an input to materialization, so it cannot leak into a
// non-dispatch session -- only an explicit [claude.settings] value produces the key.
// A future regression wiring the host preference into the materializer would make
// this fail.
func TestInstallWorkspaceRootSettings_NoRemoteControlByDefault(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, ".niwa")
	instanceRoot := filepath.Join(tmp, "instance")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws"}}
	if _, err := InstallWorkspaceRootSettings(cfg, configDir, instanceRoot, map[string]string{}); err != nil {
		t.Fatalf("InstallWorkspaceRootSettings: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(instanceRoot, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if strings.Contains(string(data), config.RemoteControlAtStartupKey) {
		t.Fatalf("materialized settings.json must not contain %s by default:\n%s", config.RemoteControlAtStartupKey, data)
	}
}

func TestBuildSettingsDoc_RemoteControlAtStartup(t *testing.T) {
	cases := []struct {
		name    string
		value   string // empty => key absent
		present bool
		want    bool
	}{
		{"true", "true", true, true},
		{"false", "false", true, false},
		{"absent", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := config.SettingsConfig{}
			if tc.value != "" {
				settings[config.RemoteControlAtStartupKey] = config.MaybeSecret{Plain: tc.value}
			}
			doc, err := buildSettingsDoc(BuildSettingsConfig{Settings: settings})
			if err != nil {
				t.Fatalf("buildSettingsDoc: %v", err)
			}
			got, ok := doc[config.RemoteControlAtStartupKey]
			if ok != tc.present {
				t.Fatalf("remoteControlAtStartup present = %v, want %v", ok, tc.present)
			}
			if tc.present && got != tc.want {
				t.Fatalf("remoteControlAtStartup = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildSettingsDoc_RemoteControlAtStartup_Invalid(t *testing.T) {
	settings := config.SettingsConfig{config.RemoteControlAtStartupKey: config.MaybeSecret{Plain: "maybe"}}
	if _, err := buildSettingsDoc(BuildSettingsConfig{Settings: settings}); err == nil {
		t.Fatal("expected an error for an unparseable remoteControlAtStartup value, got nil")
	}
}
