package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

// mockGitHubClient is a test double that returns canned repos.
type mockGitHubClient struct {
	repos map[string][]github.Repo
}

func (m *mockGitHubClient) ListRepos(_ context.Context, org string) ([]github.Repo, error) {
	return m.repos[org], nil
}

func TestApplyIntegration(t *testing.T) {
	// Set up a temporary workspace with config and content.
	tmpDir := t.TempDir()

	niwaDir := filepath.Join(tmpDir, ".niwa")
	contentDir := filepath.Join(niwaDir, "claude")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configTOML := `
[workspace]
name = "test-ws"
content_dir = "claude"

[[sources]]
org = "testorg"

[groups.public]
visibility = "public"

[groups.private]
visibility = "private"

[content.workspace]
source = "workspace.md"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write content source with template variables.
	contentSource := "# {workspace_name} Workspace\n\nRoot: {workspace}\n"
	if err := os.WriteFile(filepath.Join(contentDir, "workspace.md"), []byte(contentSource), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "app", Visibility: "public", SSHURL: "git@github.com:testorg/app.git"},
				{Name: "secrets", Visibility: "private", SSHURL: "git@github.com:testorg/secrets.git"},
			},
		},
	}

	applier := NewApplier(mockClient)
	// Replace the cloner with a no-op for integration testing (we don't want
	// real git clones in tests).
	applier.Cloner = &Cloner{} // Clone will just skip because .git won't exist; we pre-create dirs.

	instanceRoot := filepath.Join(tmpDir, "test-ws")

	// Pre-create repo dirs with .git markers so the cloner skips them.
	for _, group := range []string{"public", "private"} {
		groupDir := filepath.Join(instanceRoot, group)
		if err := os.MkdirAll(groupDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, repo := range []struct{ group, name string }{
		{"public", "app"},
		{"private", "secrets"},
	} {
		repoDir := filepath.Join(instanceRoot, repo.group, repo.name, ".git")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Verify workspace CLAUDE.md was created with expanded variables.
	claudeMD, err := os.ReadFile(filepath.Join(instanceRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}

	content := string(claudeMD)
	if !strings.Contains(content, "# test-ws Workspace") {
		t.Errorf("CLAUDE.md missing workspace name expansion:\n%s", content)
	}
	if !strings.Contains(content, "Root: ") {
		t.Errorf("CLAUDE.md missing workspace path expansion:\n%s", content)
	}
	// The {workspace} variable should have been replaced with an absolute path.
	if strings.Contains(content, "{workspace}") {
		t.Errorf("CLAUDE.md still contains unexpanded {workspace} variable:\n%s", content)
	}
}
