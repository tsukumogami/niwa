package workspace

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// TestRunRepoMaterializers_DedupsDeclaredAndDiscoveredHook is the Issue 3
// regression test. A hook script present in BOTH a declared
// [[claude.hooks.<event>]] config entry (with a matcher) AND auto-discovered
// under hooks/<event>/ must materialize exactly ONE settings.local.json entry
// that keeps the declared matcher — never two entries where the discovered copy
// loses its matcher and fires on every tool call.
func TestRunRepoMaterializers_DedupsDeclaredAndDiscoveredHook(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")

	// The script lives on disk under hooks/post_tool_use/ so DiscoverHooks
	// finds it, AND is declared in config with matcher "Bash".
	hookDir := filepath.Join(configDir, "hooks", "post_tool_use")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptRel := filepath.Join("hooks", "post_tool_use", "gate-online.sh")
	if err := os.WriteFile(filepath.Join(configDir, scriptRel), []byte("#!/bin/sh\necho gate\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "instance", "public", "repo1")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Claude: config.ClaudeConfig{
			Hooks: config.HooksConfig{
				"post_tool_use": {{
					Matcher: "Bash",
					Scripts: []string{scriptRel},
				}},
			},
		},
	}

	discovered, err := DiscoverHooks(configDir)
	if err != nil {
		t.Fatalf("DiscoverHooks: %v", err)
	}

	materializers := defaultRepoMaterializers(io.Discard)
	if _, _, err := runRepoMaterializers(materializers, repoMaterializeInputs{
		Cfg:             cfg,
		ConfigDir:       configDir,
		RepoName:        "repo1",
		RepoDir:         repoDir,
		DiscoveredHooks: discovered,
	}); err != nil {
		t.Fatalf("runRepoMaterializers: %v", err)
	}

	// Read back settings.local.json and inspect PostToolUse.
	data, err := os.ReadFile(filepath.Join(repoDir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("reading settings.local.json: %v", err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing settings.local.json: %v", err)
	}

	entries := doc.Hooks["PostToolUse"]
	if len(entries) != 1 {
		t.Fatalf("PostToolUse should have exactly 1 entry (dedup), got %d: %s", len(entries), string(data))
	}
	if entries[0].Matcher != "Bash" {
		t.Errorf("PostToolUse entry matcher = %q, want %q (declared matcher must be retained)", entries[0].Matcher, "Bash")
	}
	if len(entries[0].Hooks) != 1 {
		t.Fatalf("PostToolUse entry should register 1 command, got %d", len(entries[0].Hooks))
	}
}
