package otelprecedence

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEffective(t *testing.T) {
	settingsDoc := `{"env":{"OTEL_RESOURCE_ATTRIBUTES":"from=settings-flag"}}`
	cases := []struct {
		name   string
		launch Launch
		want   map[Mode]string
	}{
		{
			name: "every layer set",
			launch: Launch{
				Argv:            []string{"--bg", "--settings", settingsDoc, "prompt"},
				Env:             []string{"PATH=/bin", Variable + "=from=launch-env"},
				UserSettings:    "from=user-settings",
				UserSettingsSet: true,
			},
			want: map[Mode]string{
				SettingsFirst:  "from=settings-flag",
				LaunchEnvFirst: "from=launch-env",
				NoLaunchEnv:    "from=settings-flag",
			},
		},
		{
			name: "no settings flag",
			launch: Launch{
				Env:             []string{Variable + "=from=launch-env"},
				UserSettings:    "from=user-settings",
				UserSettingsSet: true,
			},
			want: map[Mode]string{
				SettingsFirst:  "from=user-settings",
				LaunchEnvFirst: "from=launch-env",
				NoLaunchEnv:    "from=user-settings",
			},
		},
		{
			name: "launch environment only",
			launch: Launch{
				Env: []string{Variable + "=from=launch-env"},
			},
			want: map[Mode]string{
				SettingsFirst:  "from=launch-env",
				LaunchEnvFirst: "from=launch-env",
				NoLaunchEnv:    "",
			},
		},
		{
			name: "settings flag without the variable",
			launch: Launch{
				Argv: []string{"--settings", `{"permissions":{}}`},
				Env:  []string{Variable + "=from=launch-env"},
			},
			want: map[Mode]string{
				SettingsFirst:  "from=launch-env",
				LaunchEnvFirst: "from=launch-env",
				NoLaunchEnv:    "",
			},
		},
	}
	for _, c := range cases {
		for _, mode := range Modes {
			got, _ := c.launch.Effective(mode)
			if got != c.want[mode] {
				t.Errorf("%s, %s: got %q, want %q", c.name, mode, got, c.want[mode])
			}
		}
	}
}

func TestUserSettingsValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if _, ok := UserSettingsValue(path); ok {
		t.Error("a missing file must report unset")
	}
	if err := os.WriteFile(path, []byte(`{"env":{"OTEL_RESOURCE_ATTRIBUTES":"team.id=platform"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, ok := UserSettingsValue(path); !ok || v != "team.id=platform" {
		t.Errorf("got %q, %v", v, ok)
	}
	if err := os.WriteFile(path, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := UserSettingsValue(path); ok {
		t.Error("a document that isn't JSON must report unset")
	}
}

func TestEntries(t *testing.T) {
	got := Entries("a=1,b=2,a=3")
	if len(got) != 2 || got["a"] != "3" || got["b"] != "2" {
		t.Errorf("got %v", got)
	}
	if len(Entries("")) != 0 {
		t.Error("an empty value has no entries")
	}
}
