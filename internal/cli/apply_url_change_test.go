package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func TestNormalizeSourceURL_TreatsHTTPSandSSHasEqual(t *testing.T) {
	cases := []struct {
		a, b string
	}{
		{"https://github.com/org/repo.git", "git@github.com:org/repo.git"},
		{"https://github.com/org/repo", "org/repo"},
		{"git@github.com:org/repo.git", "org/repo"},
		{"github.com/org/repo", "org/repo"},
	}
	for _, tc := range cases {
		t.Run(tc.a+" vs "+tc.b, func(t *testing.T) {
			if normalizeSourceURL(tc.a) != normalizeSourceURL(tc.b) {
				t.Errorf("expected equal: %q vs %q", normalizeSourceURL(tc.a), normalizeSourceURL(tc.b))
			}
		})
	}
}

func TestNormalizeSourceURL_DistinguishesDifferentRepos(t *testing.T) {
	if normalizeSourceURL("org/repo-a") == normalizeSourceURL("org/repo-b") {
		t.Error("different repos should not normalize to the same value")
	}
}

func TestOnDiskSourceURL_PrefersMarkerOverGit(t *testing.T) {
	dir := t.TempDir()
	marker := `source_url = "tsukumogami/niwa:.niwa@main"
host = "github.com"
owner = "tsukumogami"
repo = "niwa"
subpath = ".niwa"
ref = "main"
resolved_commit = "abc"
fetched_at = 2026-04-23T10:00:00Z
fetch_mechanism = "github-tarball"
`
	if err := os.WriteFile(filepath.Join(dir, ".niwa-snapshot.toml"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	got := onDiskSourceURL(dir)
	if got != "tsukumogami/niwa:.niwa@main" {
		t.Errorf("expected marker URL, got %q", got)
	}
}

func TestOnDiskSourceURL_EmptyWhenNeither(t *testing.T) {
	dir := t.TempDir()
	if got := onDiskSourceURL(dir); got != "" {
		t.Errorf("expected empty for plain dir, got %q", got)
	}
}

func TestCheckConfigSourceURLChange_NoOpWhenURLsMatch(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "ws")
	configDir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Plant the marker matching the registered URL.
	marker := `source_url = "org/repo"
host = "github.com"
owner = "org"
repo = "repo"
subpath = ""
ref = ""
resolved_commit = "abc"
fetched_at = 2026-04-23T10:00:00Z
fetch_mechanism = "github-tarball"
`
	if err := os.WriteFile(filepath.Join(configDir, ".niwa-snapshot.toml"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	// Plant the registry entry.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	cfg := &config.GlobalConfig{
		Registry: map[string]config.RegistryEntry{
			"ws-name": {
				Source:    filepath.Join(configDir, "workspace.toml"),
				Root:      root,
				SourceURL: "org/repo",
			},
		},
	}
	if err := config.SaveGlobalConfig(cfg); err != nil {
		t.Fatal(err)
	}

	if err := checkConfigSourceURLChange(configDir, nil, false); err != nil {
		t.Errorf("expected no-op when URLs match, got %v", err)
	}
}

func TestCheckConfigSourceURLChange_RefusesWithoutForce(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "ws")
	configDir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Marker says one URL.
	marker := `source_url = "org/old-repo"
host = "github.com"
owner = "org"
repo = "old-repo"
subpath = ""
ref = ""
resolved_commit = "abc"
fetched_at = 2026-04-23T10:00:00Z
fetch_mechanism = "github-tarball"
`
	if err := os.WriteFile(filepath.Join(configDir, ".niwa-snapshot.toml"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	// Registry says another.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	cfg := &config.GlobalConfig{
		Registry: map[string]config.RegistryEntry{
			"ws-name": {
				Source:    filepath.Join(configDir, "workspace.toml"),
				Root:      root,
				SourceURL: "org/new-repo",
			},
		},
	}
	if err := config.SaveGlobalConfig(cfg); err != nil {
		t.Fatal(err)
	}

	err := checkConfigSourceURLChange(configDir, nil, false)
	if err == nil {
		t.Fatal("expected URL-change error")
	}
	if !strings.Contains(err.Error(), "source changed") {
		t.Errorf("error should mention source change: %v", err)
	}
	if !strings.Contains(err.Error(), "old-repo") || !strings.Contains(err.Error(), "new-repo") {
		t.Errorf("error should name both URLs: %v", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should suggest --force: %v", err)
	}
}

func TestCheckConfigSourceURLChange_ForcePassesThrough(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "ws")
	configDir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := `source_url = "org/old-repo"
host = "github.com"
owner = "org"
repo = "old-repo"
subpath = ""
ref = ""
resolved_commit = "abc"
fetched_at = 2026-04-23T10:00:00Z
fetch_mechanism = "github-tarball"
`
	if err := os.WriteFile(filepath.Join(configDir, ".niwa-snapshot.toml"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	cfg := &config.GlobalConfig{
		Registry: map[string]config.RegistryEntry{
			"ws-name": {
				Source:    filepath.Join(configDir, "workspace.toml"),
				Root:      root,
				SourceURL: "org/new-repo",
			},
		},
	}
	if err := config.SaveGlobalConfig(cfg); err != nil {
		t.Fatal(err)
	}

	if err := checkConfigSourceURLChange(configDir, nil, true); err != nil {
		t.Errorf("--force should pass through, got %v", err)
	}
}

func TestCheckConfigSourceURLChange_NoOpWhenNotRegistered(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "ws")
	configDir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	// No registry entry for this workspace.
	if err := config.SaveGlobalConfig(&config.GlobalConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := checkConfigSourceURLChange(configDir, nil, false); err != nil {
		t.Errorf("expected no-op for unregistered workspace, got %v", err)
	}
}
