package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func stripTOMLComments(s string) string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestScaffold_WithName(t *testing.T) {
	dir := t.TempDir()

	if err := Scaffold(dir, "my-project"); err != nil {
		t.Fatalf("Scaffold returned error: %v", err)
	}

	// Verify .niwa/ directory exists.
	niwaDir := filepath.Join(dir, StateDir)
	info, err := os.Stat(niwaDir)
	if err != nil {
		t.Fatalf(".niwa/ directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal(".niwa/ is not a directory")
	}

	// Verify workspace.toml exists and contains the name.
	configPath := filepath.Join(niwaDir, WorkspaceConfigFile)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("workspace.toml not created: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, `name = "my-project"`) {
		t.Errorf("expected name = \"my-project\" in workspace.toml, got:\n%s", content)
	}
	if !strings.Contains(content, `default_branch = "main"`) {
		t.Error("expected default_branch in workspace.toml")
	}
	if !strings.Contains(content, `content_dir = "claude"`) {
		t.Error("expected content_dir in workspace.toml")
	}

	// Verify commented sections are present.
	for _, section := range []string{"[[sources]]", "[groups.public]", "[repos.my-repo]", "[content.workspace]", "[claude.hooks]", "[claude.settings]", "[env]", "[channels]"} {
		if !strings.Contains(content, "# "+section) {
			t.Errorf("expected commented section %q in template", section)
		}
	}

	// Verify claude/ content directory exists.
	claudeDir := filepath.Join(niwaDir, "claude")
	info, err = os.Stat(claudeDir)
	if err != nil {
		t.Fatalf("claude/ directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("claude/ is not a directory")
	}
}

func TestScaffold_EmptyName(t *testing.T) {
	dir := t.TempDir()

	if err := Scaffold(dir, ""); err != nil {
		t.Fatalf("Scaffold returned error: %v", err)
	}

	configPath := filepath.Join(dir, StateDir, WorkspaceConfigFile)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("workspace.toml not created: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, `name = "workspace"`) {
		t.Errorf("expected default name = \"workspace\", got:\n%s", content)
	}
}

func TestScaffold_ValidTOMLWhenStripped(t *testing.T) {
	dir := t.TempDir()

	if err := Scaffold(dir, "test-project"); err != nil {
		t.Fatalf("Scaffold returned error: %v", err)
	}

	configPath := filepath.Join(dir, StateDir, WorkspaceConfigFile)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("workspace.toml not created: %v", err)
	}

	stripped := stripTOMLComments(string(data))

	var parsed map[string]any
	if _, err := toml.Decode(stripped, &parsed); err != nil {
		t.Fatalf("template is not valid TOML when comments stripped: %v\nStripped content:\n%s", err, stripped)
	}

	ws, ok := parsed["workspace"]
	if !ok {
		t.Fatal("expected [workspace] section in parsed TOML")
	}
	wsMap, ok := ws.(map[string]any)
	if !ok {
		t.Fatal("expected [workspace] to be a table")
	}
	if wsMap["name"] != "test-project" {
		t.Errorf("expected name = \"test-project\", got %v", wsMap["name"])
	}
	if wsMap["default_branch"] != "main" {
		t.Errorf("expected default_branch = \"main\", got %v", wsMap["default_branch"])
	}
	if wsMap["content_dir"] != "claude" {
		t.Errorf("expected content_dir = \"claude\", got %v", wsMap["content_dir"])
	}
}
