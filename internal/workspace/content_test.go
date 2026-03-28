package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func TestInstallWorkspaceContent(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	contentDir := filepath.Join(configDir, "claude")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	source := "# {workspace_name}\n\nPath: {workspace}\n"
	if err := os.WriteFile(filepath.Join(contentDir, "ws.md"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:       "myws",
			ContentDir: "claude",
		},
		Content: config.ContentConfig{
			Workspace: config.ContentEntry{Source: "ws.md"},
		},
	}

	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := InstallWorkspaceContent(cfg, configDir, instanceRoot); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(instanceRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "# myws") {
		t.Errorf("missing workspace_name expansion: %s", content)
	}
	if strings.Contains(content, "{workspace_name}") {
		t.Errorf("unexpanded variable: %s", content)
	}
	if strings.Contains(content, "{workspace}") {
		t.Errorf("unexpanded variable: %s", content)
	}
}

func TestInstallWorkspaceContentPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:       "test",
			ContentDir: ".",
		},
		Content: config.ContentConfig{
			Workspace: config.ContentEntry{Source: "../../etc/passwd"},
		},
	}

	err := InstallWorkspaceContent(cfg, configDir, filepath.Join(tmpDir, "instance"))
	if err == nil {
		t.Fatal("expected path traversal error, got nil")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("error = %q, want path escape message", err.Error())
	}
}

func TestInstallWorkspaceContentNoSource(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Content:   config.ContentConfig{},
	}

	// Should be a no-op, not an error.
	if err := InstallWorkspaceContent(cfg, "/tmp", "/tmp/instance"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExpandVars(t *testing.T) {
	input := "Hello {workspace_name}, root is {workspace}."
	vars := map[string]string{
		"{workspace_name}": "myws",
		"{workspace}":      "/home/user/myws",
	}

	got := expandVars(input, vars)
	want := "Hello myws, root is /home/user/myws."
	if got != want {
		t.Errorf("expandVars = %q, want %q", got, want)
	}
}
