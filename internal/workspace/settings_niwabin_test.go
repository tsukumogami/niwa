package workspace

import (
	"reflect"
	"testing"
)

func TestBuildSettingsDocCarriesNiwaBin(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resolved map[string]string
		niwaBin  string
		want     map[string]any
	}{
		{"alone", nil, "/opt/niwa/bin/niwa", map[string]any{"NIWA_BIN": "/opt/niwa/bin/niwa"}},
		{"beside the declared variables", map[string]string{"FOO": "bar"}, "/opt/niwa/bin/niwa",
			map[string]any{"FOO": "bar", "NIWA_BIN": "/opt/niwa/bin/niwa"}},
		{"over a declared NIWA_BIN", map[string]string{"NIWA_BIN": "/somewhere/else"}, "/opt/niwa/bin/niwa",
			map[string]any{"NIWA_BIN": "/opt/niwa/bin/niwa"}},
		{"no path, declared variables only", map[string]string{"FOO": "bar"}, "", map[string]any{"FOO": "bar"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := buildSettingsDoc(BuildSettingsConfig{ResolvedEnvVars: tc.resolved, NiwaBin: tc.niwaBin, BaseDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if got := doc["env"]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("env = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildSettingsDocWithoutNiwaBinOrEnvHasNoEnvBlock(t *testing.T) {
	doc, err := buildSettingsDoc(BuildSettingsConfig{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["env"]; ok {
		t.Errorf("an env block with nothing to carry: %v", doc["env"])
	}
}
