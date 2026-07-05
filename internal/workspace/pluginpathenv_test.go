package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// fakePluginDir creates a plugin install directory containing a script at
// relPath and returns the install dir. It models the plugin cache location niwa
// resolves at provisioning time.
func fakePluginDir(t *testing.T, relPath string) string {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho render\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestResolvePluginPathEnv_Resolves(t *testing.T) {
	installDir := fakePluginDir(t, "scripts/render.sh")
	lookup := func(key string) (string, bool) {
		if key == "work-summary" || key == "work-summary@shirabe" {
			return installDir, true
		}
		return "", false
	}

	got := resolvePluginPathEnv([]config.PluginPathEnvBinding{
		{Name: "SHIRABE_WORK_SUMMARY", Plugin: "work-summary", Path: "scripts/render.sh"},
	}, lookup)

	want := filepath.Join(installDir, "scripts/render.sh")
	if got["SHIRABE_WORK_SUMMARY"] != want {
		t.Errorf("SHIRABE_WORK_SUMMARY = %q, want %q", got["SHIRABE_WORK_SUMMARY"], want)
	}
}

func TestResolvePluginPathEnv_MissingPluginIsFailSafe(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }
	got := resolvePluginPathEnv([]config.PluginPathEnvBinding{
		{Name: "SHIRABE_WORK_SUMMARY", Plugin: "work-summary", Path: "scripts/render.sh"},
	}, lookup)
	if got != nil {
		t.Errorf("unresolvable plugin should yield nil (fail-safe), got %v", got)
	}
}

func TestResolvePluginPathEnv_MissingFileIsFailSafe(t *testing.T) {
	installDir := t.TempDir() // no script written
	lookup := func(string) (string, bool) { return installDir, true }
	got := resolvePluginPathEnv([]config.PluginPathEnvBinding{
		{Name: "SHIRABE_WORK_SUMMARY", Plugin: "work-summary", Path: "scripts/render.sh"},
	}, lookup)
	if got != nil {
		t.Errorf("missing script should yield nil (fail-safe), got %v", got)
	}
}

func TestResolvePluginPathEnv_PathEscapeRejected(t *testing.T) {
	installDir := fakePluginDir(t, "scripts/render.sh")
	// Plant a sibling file outside the plugin dir that a "../" path would reach.
	outside := filepath.Join(filepath.Dir(installDir), "evil.sh")
	if err := os.WriteFile(outside, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	lookup := func(string) (string, bool) { return installDir, true }
	got := resolvePluginPathEnv([]config.PluginPathEnvBinding{
		{Name: "SHIRABE_WORK_SUMMARY", Plugin: "work-summary", Path: "../evil.sh"},
	}, lookup)
	if got != nil {
		t.Errorf("path escaping the plugin dir must be rejected (confinement), got %v", got)
	}
}

func TestResolvePluginPathEnv_NilLookup(t *testing.T) {
	got := resolvePluginPathEnv([]config.PluginPathEnvBinding{
		{Name: "X", Plugin: "p", Path: "s.sh"},
	}, nil)
	if got != nil {
		t.Errorf("nil lookup should yield nil, got %v", got)
	}
}

func TestPluginKeyMatches(t *testing.T) {
	cases := []struct {
		registryKey, declared string
		want                  bool
	}{
		{"work-summary@shirabe", "work-summary@shirabe", true}, // full key
		{"work-summary@shirabe", "work-summary", true},         // bare name
		{"work-summary@shirabe", "shirabe", false},             // marketplace, not name
		{"other@shirabe", "work-summary", false},
		{"work-summary", "work-summary", true}, // no marketplace qualifier
	}
	for _, c := range cases {
		if got := pluginKeyMatches(c.registryKey, c.declared); got != c.want {
			t.Errorf("pluginKeyMatches(%q, %q) = %v, want %v", c.registryKey, c.declared, got, c.want)
		}
	}
}

func TestInjectPluginPathEnv_WritesClaudeEnvVar(t *testing.T) {
	cfg := &config.WorkspaceConfig{}
	injectPluginPathEnv(cfg, map[string]string{"SHIRABE_WORK_SUMMARY": "/abs/render.sh"})
	got := cfg.Claude.Env.Vars.Values["SHIRABE_WORK_SUMMARY"]
	if got.Plain != "/abs/render.sh" {
		t.Errorf("injected claude env var = %q, want %q", got.Plain, "/abs/render.sh")
	}
}

func TestInjectPluginPathEnv_EmptyIsNoOp(t *testing.T) {
	cfg := &config.WorkspaceConfig{}
	injectPluginPathEnv(cfg, nil)
	if cfg.Claude.Env.Vars.Values != nil {
		t.Errorf("empty env should leave config untouched, got %v", cfg.Claude.Env.Vars.Values)
	}
}
