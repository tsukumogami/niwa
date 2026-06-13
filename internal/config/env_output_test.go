package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

// decodeTargets decodes an env_output value from a TOML snippet via the real
// BurntSushi decoder, exercising OutputTargets.UnmarshalTOML.
func decodeTargets(t *testing.T, snippet string) OutputTargets {
	t.Helper()
	var doc struct {
		EnvOutput OutputTargets `toml:"env_output"`
	}
	if _, err := toml.Decode(snippet, &doc); err != nil {
		t.Fatalf("decode %q: %v", snippet, err)
	}
	return doc.EnvOutput
}

func TestOutputTargets_DecodeBareString(t *testing.T) {
	got := decodeTargets(t, `env_output = ".env.local"`)
	if len(got) != 1 || got[0].Path != ".env.local" || got[0].Format != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestOutputTargets_DecodeListOfStrings(t *testing.T) {
	got := decodeTargets(t, `env_output = [".env.local", "secrets.json"]`)
	if len(got) != 2 || got[0].Path != ".env.local" || got[1].Path != "secrets.json" {
		t.Fatalf("got %+v", got)
	}
}

func TestOutputTargets_DecodeListOfTables(t *testing.T) {
	got := decodeTargets(t, `env_output = [{ path = "secrets", format = "shell" }, { path = ".env" }]`)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Path != "secrets" || got[0].Format != FormatShell {
		t.Errorf("element 0 = %+v", got[0])
	}
	if got[1].Path != ".env" || got[1].Format != "" {
		t.Errorf("element 1 = %+v", got[1])
	}
}

func TestOutputTargets_DecodeRejectsBadFormat(t *testing.T) {
	var doc struct {
		EnvOutput OutputTargets `toml:"env_output"`
	}
	if _, err := toml.Decode(`env_output = [{ path = "x", format = "yaml" }]`, &doc); err == nil {
		t.Fatal("expected error for unknown format, got nil")
	}
}

func TestOutputTargets_DecodeRejectsTableMissingPath(t *testing.T) {
	var doc struct {
		EnvOutput OutputTargets `toml:"env_output"`
	}
	if _, err := toml.Decode(`env_output = [{ format = "json" }]`, &doc); err == nil {
		t.Fatal("expected error for table missing path, got nil")
	}
}

func TestInferFormat(t *testing.T) {
	cases := map[string]OutputFormat{
		".env":         FormatDotenv,
		".local.env":   FormatDotenv,
		".env.local":   FormatDotenv,
		"secrets":      FormatDotenv,
		"secrets.json": FormatJSON,
		"env.sh":       FormatShell,
		"a/b/c.JSON":   FormatJSON, // case-insensitive
	}
	for path, want := range cases {
		if got := InferFormat(path); got != want {
			t.Errorf("InferFormat(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestEffectiveEnvOutput_DefaultWhenUnset(t *testing.T) {
	got := EffectiveEnvOutput(nil, &WorkspaceConfig{}, "repo")
	if len(got) != 1 || got[0].Path != DefaultEnvOutputPath || got[0].Format != FormatDotenv {
		t.Fatalf("default mismatch: %+v", got)
	}
}

func TestEffectiveEnvOutput_Precedence(t *testing.T) {
	ws := &WorkspaceConfig{
		Workspace: WorkspaceMeta{EnvOutput: OutputTargets{{Path: ".env.local"}}},
		Repos: map[string]RepoOverride{
			"override": {EnvOutput: OutputTargets{{Path: "secrets.json"}}},
			"inherit":  {},
		},
	}
	global := OutputTargets{{Path: "global.env"}}

	// Per-repo wins.
	if got := EffectiveEnvOutput(global, ws, "override"); got[0].Path != "secrets.json" || got[0].Format != FormatJSON {
		t.Errorf("repo rung: %+v", got)
	}
	// Repo with no env_output inherits workspace.
	if got := EffectiveEnvOutput(global, ws, "inherit"); got[0].Path != ".env.local" || got[0].Format != FormatDotenv {
		t.Errorf("workspace rung: %+v", got)
	}
	// Workspace unset -> global.
	wsNoOutput := &WorkspaceConfig{Repos: map[string]RepoOverride{"r": {}}}
	if got := EffectiveEnvOutput(global, wsNoOutput, "r"); got[0].Path != "global.env" {
		t.Errorf("global rung: %+v", got)
	}
	// Nothing set anywhere -> default.
	if got := EffectiveEnvOutput(nil, wsNoOutput, "r"); got[0].Path != DefaultEnvOutputPath {
		t.Errorf("default fallthrough: %+v", got)
	}
}

func TestEffectiveEnvOutput_ExplicitFormatOverridesInference(t *testing.T) {
	ws := &WorkspaceConfig{
		Repos: map[string]RepoOverride{
			// .env would infer dotenv; explicit json wins.
			"r": {EnvOutput: OutputTargets{{Path: ".env", Format: FormatJSON}}},
		},
	}
	got := EffectiveEnvOutput(nil, ws, "r")
	if got[0].Format != FormatJSON {
		t.Errorf("explicit override ignored: %+v", got)
	}
}
