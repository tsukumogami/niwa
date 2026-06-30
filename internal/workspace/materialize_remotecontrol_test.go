package workspace

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

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
				settings["remoteControlAtStartup"] = config.MaybeSecret{Plain: tc.value}
			}
			doc, err := buildSettingsDoc(BuildSettingsConfig{Settings: settings})
			if err != nil {
				t.Fatalf("buildSettingsDoc: %v", err)
			}
			got, ok := doc["remoteControlAtStartup"]
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
	settings := config.SettingsConfig{"remoteControlAtStartup": config.MaybeSecret{Plain: "maybe"}}
	if _, err := buildSettingsDoc(BuildSettingsConfig{Settings: settings}); err == nil {
		t.Fatal("expected an error for an unparseable remoteControlAtStartup value, got nil")
	}
}
