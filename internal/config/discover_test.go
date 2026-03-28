package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscover(t *testing.T) {
	// Create a temp workspace structure:
	// tmpdir/
	//   .niwa/
	//     workspace.toml
	//   instance/
	//     public/
	//       somerepo/
	tmpDir := t.TempDir()

	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(niwaDir, "workspace.toml")
	if err := os.WriteFile(configFile, []byte("[workspace]\nname = \"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deepDir := filepath.Join(tmpDir, "instance", "public", "somerepo")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Discover from deep nested directory should find the config.
	path, dir, err := Discover(deepDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantPath := configFile
	if path != wantPath {
		t.Errorf("configPath = %q, want %q", path, wantPath)
	}

	wantDir := niwaDir
	if dir != wantDir {
		t.Errorf("configDir = %q, want %q", dir, wantDir)
	}
}

func TestDiscoverFromConfigDir(t *testing.T) {
	tmpDir := t.TempDir()

	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte("[workspace]\nname = \"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Discover from the workspace root itself.
	path, _, err := Discover(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != filepath.Join(niwaDir, "workspace.toml") {
		t.Errorf("unexpected path: %s", path)
	}
}

func TestDiscoverNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	_, _, err := Discover(tmpDir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
