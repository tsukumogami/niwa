package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func TestResolveMarketplaceSourceGitHub(t *testing.T) {
	result, err := ResolveMarketplaceSource("tsukumogami/shirabe", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "tsukumogami/shirabe" {
		t.Errorf("expected passthrough, got %q", result)
	}
}

func TestResolveMarketplaceSourceRepoRef(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "tools")
	pluginDir := filepath.Join(repoDir, ".claude-plugin")
	os.MkdirAll(pluginDir, 0o755)
	os.WriteFile(filepath.Join(pluginDir, "marketplace.json"), []byte("{}"), 0o644)

	repoIndex := map[string]string{"tools": repoDir}

	result, err := ResolveMarketplaceSource("repo:tools/.claude-plugin/marketplace.json", repoIndex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := filepath.Join(repoDir, ".claude-plugin", "marketplace.json")
	if result != expected {
		t.Errorf("result = %q, want %q", result, expected)
	}
}

func TestResolveMarketplaceSourceMalformed(t *testing.T) {
	_, err := ResolveMarketplaceSource("repo:tools", nil)
	if err == nil {
		t.Fatal("expected error for malformed ref")
	}
	if !strings.Contains(err.Error(), "expected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveMarketplaceSourceUnmanaged(t *testing.T) {
	repoIndex := map[string]string{"other": "/tmp/other"}

	_, err := ResolveMarketplaceSource("repo:tools/.claude-plugin/marketplace.json", repoIndex)
	if err == nil {
		t.Fatal("expected error for unmanaged repo")
	}
	if !strings.Contains(err.Error(), "not managed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveMarketplaceSourceNotCloned(t *testing.T) {
	repoIndex := map[string]string{"tools": "/nonexistent/tools"}

	_, err := ResolveMarketplaceSource("repo:tools/.claude-plugin/marketplace.json", repoIndex)
	if err == nil {
		t.Fatal("expected error for uncloned repo")
	}
	if !strings.Contains(err.Error(), "not been cloned") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveMarketplaceSourceFileNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "tools")
	os.MkdirAll(repoDir, 0o755)

	repoIndex := map[string]string{"tools": repoDir}

	_, err := ResolveMarketplaceSource("repo:tools/.claude-plugin/marketplace.json", repoIndex)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "file not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveMarketplaceSourcePathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "tools")
	os.MkdirAll(repoDir, 0o755)
	// Create a file outside the repo dir that traversal would reach.
	os.WriteFile(filepath.Join(tmpDir, "secret.json"), []byte("{}"), 0o644)

	repoIndex := map[string]string{"tools": repoDir}

	_, err := ResolveMarketplaceSource("repo:tools/../secret.json", repoIndex)
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "escapes repo directory") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Merge tests for plugins ---

func TestMergeOverridesPluginsInherit(t *testing.T) {
	plugins := []string{"a@m", "b@m"}
	ws := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Plugins: &plugins,
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if len(eff.Plugins) != 2 || eff.Plugins[0] != "a@m" || eff.Plugins[1] != "b@m" {
		t.Errorf("expected inherited [a@m b@m], got %v", eff.Plugins)
	}
}

func TestMergeOverridesPluginsReplace(t *testing.T) {
	wsPlugins := []string{"a@m", "b@m"}
	repoPlugins := []string{"c@m"}
	ws := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Plugins: &wsPlugins,
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeOverride{
					Plugins: &repoPlugins,
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if len(eff.Plugins) != 1 || eff.Plugins[0] != "c@m" {
		t.Errorf("expected replaced [c@m], got %v", eff.Plugins)
	}
}

func TestMergeOverridesPluginsDisable(t *testing.T) {
	wsPlugins := []string{"a@m", "b@m"}
	empty := []string{}
	ws := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Plugins: &wsPlugins,
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {
				Claude: &config.ClaudeOverride{
					Plugins: &empty,
				},
			},
		},
	}
	eff := MergeOverrides(ws, "myrepo")

	if len(eff.Plugins) != 0 {
		t.Errorf("expected empty (disabled), got %v", eff.Plugins)
	}
}

func TestMergeOverridesPluginsNilWorkspace(t *testing.T) {
	ws := &config.WorkspaceConfig{}
	eff := MergeOverrides(ws, "myrepo")

	if eff.Plugins != nil {
		t.Errorf("expected nil plugins, got %v", eff.Plugins)
	}
}
