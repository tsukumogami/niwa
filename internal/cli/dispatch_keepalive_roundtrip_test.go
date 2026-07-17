package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestKeepAliveKey_EndToEnd_MaterializeReadBack drives the real path a
// downstream keep-alive decision travels: a [claude.settings].keepAliveOnDispatch
// value -> InstallWorkspaceRootSettings -> readInstanceSettings. A one-sided
// rename of the emit key or the read-back struct tag breaks this test, exactly
// like the remote-control mirror (TestRemoteControlKey_EndToEnd_MaterializeReadBack):
// a silent read-back-as-absent would let the host default override a downstream
// explicit "false".
func TestKeepAliveKey_EndToEnd_MaterializeReadBack(t *testing.T) {
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
						config.KeepAliveOnDispatchKey: config.MaybeSecret{Plain: val},
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
			if inst.KeepAliveOnDispatch == nil {
				t.Fatal("read back nil, want non-nil (emit key and read-back tag must agree)")
			}
			if *inst.KeepAliveOnDispatch != want {
				t.Fatalf("read back %v, want %v", *inst.KeepAliveOnDispatch, want)
			}
		})
	}
}
