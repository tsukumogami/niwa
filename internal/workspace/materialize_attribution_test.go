package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func TestBuildSettingsDoc_Attribution(t *testing.T) {
	hidden := map[string]any{"commit": "", "pr": "", "sessionUrl": false}
	cases := []struct {
		name    string
		value   string // empty => key absent
		present bool
	}{
		{"false", "false", true},
		{"false padded", " false ", true},
		{"true", "true", false},
		{"absent", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := config.SettingsConfig{}
			if tc.value != "" {
				settings[config.AttributionKey] = config.MaybeSecret{Plain: tc.value}
			}
			doc, err := buildSettingsDoc(BuildSettingsConfig{Settings: settings})
			if err != nil {
				t.Fatalf("buildSettingsDoc: %v", err)
			}
			got, ok := doc[config.AttributionKey]
			if ok != tc.present {
				t.Fatalf("attribution present = %v, want %v", ok, tc.present)
			}
			if tc.present && !reflect.DeepEqual(got, hidden) {
				t.Fatalf("attribution = %#v, want %#v", got, hidden)
			}
		})
	}
}

func TestBuildSettingsDoc_Attribution_Invalid(t *testing.T) {
	settings := config.SettingsConfig{config.AttributionKey: config.MaybeSecret{Plain: "off"}}
	if _, err := buildSettingsDoc(BuildSettingsConfig{Settings: settings}); err == nil {
		t.Fatal("expected an error for an unparseable attribution value, got nil")
	}
}

// TestRootSettingsMaterializer_AttributionObjectForm pins what lands on disk:
// the object form, never a bare boolean, which older Claude Code versions
// reject along with the rest of the settings file.
func TestRootSettingsMaterializer_AttributionObjectForm(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, ".niwa")
	instanceRoot := filepath.Join(tmp, "instance")
	for _, d := range []string{configDir, instanceRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws"}}
	cfg.Claude.Settings = config.SettingsConfig{config.AttributionKey: config.MaybeSecret{Plain: "false"}}
	if _, err := (&RootSettingsMaterializer{}).Materialize(&MaterializeContext{Config: cfg, ConfigDir: configDir, RepoDir: instanceRoot, RepoIndex: map[string]string{}}); err != nil {
		t.Fatalf("RootSettingsMaterializer: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(instanceRoot, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(doc[config.AttributionKey], &got); err != nil {
		t.Fatalf("attribution is not an object: %s", doc[config.AttributionKey])
	}
	want := map[string]any{"commit": "", "pr": "", "sessionUrl": false}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attribution = %#v, want %#v", got, want)
	}
}
