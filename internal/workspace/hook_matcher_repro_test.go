package workspace

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// TestRunRepoMaterializers_BothEventsRetainMatcher mirrors the real production
// scenario: a config declares BOTH pre_tool_use AND post_tool_use hooks, each
// with matcher "Bash" AND each script present on disk under hooks/<event>/ (so
// both are declared AND auto-discovered). After materialization BOTH events must
// produce exactly ONE settings.local.json entry that keeps its declared matcher.
//
// The observed production defect was that pre_tool_use kept its matcher while
// post_tool_use lost it (fired on every tool call). This test asserts symmetry
// across events.
func TestRunRepoMaterializers_BothEventsRetainMatcher(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")

	// Both scripts live on disk under hooks/<event>/ so DiscoverHooks finds
	// them, AND both are declared in config with matcher "Bash".
	writeScript := func(event, name string) string {
		dir := filepath.Join(configDir, "hooks", event)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		rel := filepath.Join("hooks", event, name)
		if err := os.WriteFile(filepath.Join(configDir, rel), []byte("#!/bin/sh\necho hook\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return rel
	}
	preScript := writeScript("pre_tool_use", "gate-online.sh")
	postScript := writeScript("post_tool_use", "work-summary-capture.sh")

	repoDir := filepath.Join(tmpDir, "instance", "public", "repo1")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Claude: config.ClaudeConfig{
			Hooks: config.HooksConfig{
				"pre_tool_use": {{
					Matcher: "Bash",
					Scripts: []string{preScript},
				}},
				"post_tool_use": {{
					Matcher: "Bash",
					Scripts: []string{postScript},
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

	for _, event := range []string{"PreToolUse", "PostToolUse"} {
		entries := doc.Hooks[event]
		if len(entries) != 1 {
			t.Fatalf("%s should have exactly 1 entry (dedup), got %d: %s", event, len(entries), string(data))
		}
		if entries[0].Matcher != "Bash" {
			t.Errorf("%s entry matcher = %q, want %q (declared matcher must be retained): %s", event, entries[0].Matcher, "Bash", string(data))
		}
		if len(entries[0].Hooks) != 1 {
			t.Fatalf("%s entry should register 1 command, got %d", event, len(entries[0].Hooks))
		}
	}
}
