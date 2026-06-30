package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestRemoteControlKey_EndToEnd_MaterializeReadBack drives the real path a
// downstream override travels: a [claude.settings].remoteControlAtStartup value ->
// InstallWorkspaceRootSettings (the materializer the interactive, ephemeral, apply,
// and dispatch-provision paths all use) -> readInstanceSettings. A one-sided rename
// of the emit key or the read-back struct tag breaks this test, which is what closes
// the silent-override gap (read-back-as-absent would inject --settings and clobber a
// user's explicit "false").
func TestRemoteControlKey_EndToEnd_MaterializeReadBack(t *testing.T) {
	for _, want := range []bool{true, false} {
		val := "false"
		if want {
			val = "true"
		}
		t.Run(val, func(t *testing.T) {
			tmp := t.TempDir()
			configDir := filepath.Join(tmp, ".niwa")
			instanceRoot := filepath.Join(tmp, "instance")
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			cfg := &config.WorkspaceConfig{
				Workspace: config.WorkspaceMeta{Name: "ws"},
				Claude: config.ClaudeConfig{
					Settings: config.SettingsConfig{
						config.RemoteControlAtStartupKey: config.MaybeSecret{Plain: val},
					},
				},
			}
			if _, err := workspace.InstallWorkspaceRootSettings(cfg, configDir, instanceRoot, map[string]string{}); err != nil {
				t.Fatalf("InstallWorkspaceRootSettings: %v", err)
			}
			inst, err := readInstanceSettings(instanceRoot)
			if err != nil {
				t.Fatalf("readInstanceSettings: %v", err)
			}
			if inst.RemoteControlAtStartup == nil {
				t.Fatal("read back nil, want non-nil (emit key and read-back tag must agree)")
			}
			if *inst.RemoteControlAtStartup != want {
				t.Fatalf("read back %v, want %v", *inst.RemoteControlAtStartup, want)
			}
		})
	}
}

// TestInstanceSettings_TagMatchesKey pins the read-back struct tag -- a literal Go
// cannot const-ify -- to config.RemoteControlAtStartupKey, so a tag rename fails here.
func TestInstanceSettings_TagMatchesKey(t *testing.T) {
	v := true
	data, err := json.Marshal(instanceSettings{RemoteControlAtStartup: &v})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m[config.RemoteControlAtStartupKey]; !ok {
		t.Fatalf("instanceSettings JSON %s lacks key %q; the struct tag drifted from the const", data, config.RemoteControlAtStartupKey)
	}
}
