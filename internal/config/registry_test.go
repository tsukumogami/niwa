package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleGlobalConfig = `
[global]
clone_protocol = "ssh"

[registry.my-workspace]
source = "/home/user/projects/my-workspace/.niwa/workspace.toml"
root = "/home/user/projects/my-workspace"

[registry.work]
source = "/home/user/work/.niwa/workspace.toml"
root = "/home/user/work"
`

func TestParseGlobalConfig(t *testing.T) {
	cfg, err := ParseGlobalConfig([]byte(sampleGlobalConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Global.CloneProtocol != "ssh" {
		t.Errorf("clone_protocol = %q, want %q", cfg.Global.CloneProtocol, "ssh")
	}

	if len(cfg.Registry) != 2 {
		t.Fatalf("registry count = %d, want 2", len(cfg.Registry))
	}

	ws := cfg.Registry["my-workspace"]
	if ws.Source != "/home/user/projects/my-workspace/.niwa/workspace.toml" {
		t.Errorf("registry[my-workspace].source = %q", ws.Source)
	}
	if ws.Root != "/home/user/projects/my-workspace" {
		t.Errorf("registry[my-workspace].root = %q", ws.Root)
	}
}

func TestParseGlobalConfigEmpty(t *testing.T) {
	cfg, err := ParseGlobalConfig([]byte(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Global.CloneProtocol != "" {
		t.Errorf("clone_protocol = %q, want empty", cfg.Global.CloneProtocol)
	}
	if cfg.Registry != nil {
		t.Errorf("registry should be nil for empty config, got %v", cfg.Registry)
	}
}

func TestCloneProtocolDefault(t *testing.T) {
	cfg := &GlobalConfig{}
	if got := cfg.CloneProtocol(); got != "ssh" {
		t.Errorf("CloneProtocol() = %q, want %q", got, "ssh")
	}
}

func TestCloneProtocolExplicit(t *testing.T) {
	cfg := &GlobalConfig{
		Global: GlobalSettings{CloneProtocol: "ssh"},
	}
	if got := cfg.CloneProtocol(); got != "ssh" {
		t.Errorf("CloneProtocol() = %q, want %q", got, "ssh")
	}
}

func TestLookupWorkspaceFound(t *testing.T) {
	cfg, err := ParseGlobalConfig([]byte(sampleGlobalConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry := cfg.LookupWorkspace("work")
	if entry == nil {
		t.Fatal("expected entry for 'work', got nil")
	}
	if entry.Root != "/home/user/work" {
		t.Errorf("root = %q, want %q", entry.Root, "/home/user/work")
	}
}

func TestLookupWorkspaceNotFound(t *testing.T) {
	cfg, err := ParseGlobalConfig([]byte(sampleGlobalConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry := cfg.LookupWorkspace("nonexistent")
	if entry != nil {
		t.Errorf("expected nil for nonexistent workspace, got %+v", entry)
	}
}

func TestLookupWorkspaceEmptyRegistry(t *testing.T) {
	cfg := &GlobalConfig{}
	entry := cfg.LookupWorkspace("anything")
	if entry != nil {
		t.Errorf("expected nil for empty registry, got %+v", entry)
	}
}

func TestSetRegistryEntry(t *testing.T) {
	cfg := &GlobalConfig{}
	cfg.SetRegistryEntry("new-ws", RegistryEntry{
		Source: "/path/to/workspace.toml",
		Root:   "/path/to",
	})

	if len(cfg.Registry) != 1 {
		t.Fatalf("registry count = %d, want 1", len(cfg.Registry))
	}

	entry := cfg.LookupWorkspace("new-ws")
	if entry == nil {
		t.Fatal("expected entry after SetRegistryEntry")
	}
	if entry.Source != "/path/to/workspace.toml" {
		t.Errorf("source = %q", entry.Source)
	}
}

func TestLoadGlobalConfigMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "nonexistent", "config.toml")

	cfg, err := LoadGlobalConfigFrom(path)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}

	// Should return empty defaults.
	if cfg.Global.CloneProtocol != "" {
		t.Errorf("clone_protocol = %q, want empty", cfg.Global.CloneProtocol)
	}
	if cfg.Registry != nil {
		t.Errorf("registry should be nil, got %v", cfg.Registry)
	}
}

func TestLoadGlobalConfigFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")

	if err := os.WriteFile(path, []byte(sampleGlobalConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadGlobalConfigFrom(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Global.CloneProtocol != "ssh" {
		t.Errorf("clone_protocol = %q, want %q", cfg.Global.CloneProtocol, "ssh")
	}
	if len(cfg.Registry) != 2 {
		t.Errorf("registry count = %d, want 2", len(cfg.Registry))
	}
}

func TestSaveAndLoadGlobalConfig(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "subdir", "config.toml")

	original := &GlobalConfig{
		Global: GlobalSettings{CloneProtocol: "ssh"},
		Registry: map[string]RegistryEntry{
			"test-ws": {
				Source: "/tmp/ws/.niwa/workspace.toml",
				Root:   "/tmp/ws",
			},
		},
	}

	if err := SaveGlobalConfigTo(path, original); err != nil {
		t.Fatalf("save error: %v", err)
	}

	loaded, err := LoadGlobalConfigFrom(path)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	if loaded.Global.CloneProtocol != "ssh" {
		t.Errorf("clone_protocol = %q, want %q", loaded.Global.CloneProtocol, "ssh")
	}
	entry := loaded.LookupWorkspace("test-ws")
	if entry == nil {
		t.Fatal("expected test-ws entry after round-trip")
	}
	if entry.Root != "/tmp/ws" {
		t.Errorf("root = %q, want %q", entry.Root, "/tmp/ws")
	}
}

func TestGlobalConfigPathRespectsXDG(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	path, err := GlobalConfigPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := filepath.Join(tmpDir, "niwa", "config.toml")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestGlobalConfigPathFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")

	path, err := GlobalConfigPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "niwa", "config.toml")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestGlobalConfigDirRespectsXDG(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	dir, err := GlobalConfigDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := filepath.Join(tmpDir, "niwa", "global")
	if dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
}

func TestSaveGlobalConfigToFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")

	cfg := &GlobalConfig{
		Global: GlobalSettings{CloneProtocol: "ssh"},
	}
	if err := SaveGlobalConfigTo(path, cfg); err != nil {
		t.Fatalf("save error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat error: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("file permissions = %o, want 0o600", perm)
	}
}

func TestGlobalConfigSourceRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")

	original := &GlobalConfig{
		GlobalConfig: GlobalConfigSource{Repo: "myorg/my-config"},
	}
	if err := SaveGlobalConfigTo(path, original); err != nil {
		t.Fatalf("save error: %v", err)
	}

	loaded, err := LoadGlobalConfigFrom(path)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if loaded.GlobalConfig.Repo != "myorg/my-config" {
		t.Errorf("GlobalConfig.Repo = %q, want myorg/my-config", loaded.GlobalConfig.Repo)
	}
}

func TestRegisteredNamesEmpty(t *testing.T) {
	var g *GlobalConfig
	if names := g.RegisteredNames(); len(names) != 0 {
		t.Errorf("nil receiver: got %v, want empty", names)
	}

	g = &GlobalConfig{}
	if names := g.RegisteredNames(); len(names) != 0 {
		t.Errorf("empty receiver: got %v, want empty", names)
	}
}

func TestRegisteredNamesSortedAndFiltered(t *testing.T) {
	g := &GlobalConfig{Registry: map[string]RegistryEntry{
		"beta":            {Source: "b.toml", Root: "/b"},
		"alpha":           {Source: "a.toml", Root: "/a"},
		"gamma":           {Source: "g.toml", Root: "/g"},
		"bad\tname":       {Source: "x.toml", Root: "/x"},
		"bidi\u202ename":  {Source: "y.toml", Root: "/y"},
	}}

	got := g.RegisteredNames()
	want := []string{"alpha", "beta", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestListRegisteredWorkspacesMissingConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	names, err := ListRegisteredWorkspaces()
	if err != nil {
		t.Fatalf("ListRegisteredWorkspaces: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected no names on missing config, got %v", names)
	}
}

func TestListRegisteredWorkspacesHappyPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	cfgDir := filepath.Join(home, "niwa")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := `[registry.alpha]
source = "/tmp/alpha/.niwa/workspace.toml"
root = "/tmp/alpha"

[registry.beta]
source = "/tmp/beta/.niwa/workspace.toml"
root = "/tmp/beta"
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	names, err := ListRegisteredWorkspaces()
	if err != nil {
		t.Fatalf("ListRegisteredWorkspaces: %v", err)
	}
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("got %v, want [alpha beta]", names)
	}
}
