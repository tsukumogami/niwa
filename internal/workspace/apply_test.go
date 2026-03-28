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
	reposContentDir := filepath.Join(contentDir, "repos")
	if err := os.MkdirAll(reposContentDir, 0o755); err != nil {
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

[content.groups.public]
source = "public.md"

[content.repos.app]
source = "repos/app.md"

  [content.repos.app.subdirs]
  docs = "repos/app-docs.md"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write content sources with template variables.
	contentFiles := map[string]string{
		"workspace.md":      "# {workspace_name} Workspace\n\nRoot: {workspace}\n",
		"public.md":         "# {group_name} Group\n\nWorkspace: {workspace_name}\n",
		"repos/app.md":      "# {repo_name}\n\nGroup: {group_name}\nWorkspace: {workspace_name}\n",
		"repos/app-docs.md": "# {repo_name} docs subdir\n",
		"repos/secrets.md":  "# Auto-discovered {repo_name}\n",
	}
	for name, body := range contentFiles {
		path := filepath.Join(contentDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
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
	applier.Cloner = &Cloner{}

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
		repoDir := filepath.Join(instanceRoot, repo.group, repo.name)
		if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		// Add .gitignore with *.local* to suppress warnings.
		if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Pre-create the docs subdir.
	docsDir := filepath.Join(instanceRoot, "public", "app", "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Verify workspace CLAUDE.md.
	assertFileContains(t, filepath.Join(instanceRoot, "CLAUDE.md"), "# test-ws Workspace")
	assertFileNotContains(t, filepath.Join(instanceRoot, "CLAUDE.md"), "{workspace}")

	// Verify group CLAUDE.md (non-git directory gets CLAUDE.md).
	assertFileContains(t, filepath.Join(instanceRoot, "public", "CLAUDE.md"), "# public Group")
	assertFileContains(t, filepath.Join(instanceRoot, "public", "CLAUDE.md"), "Workspace: test-ws")

	// Verify repo CLAUDE.local.md (git directory gets CLAUDE.local.md).
	assertFileContains(t, filepath.Join(instanceRoot, "public", "app", "CLAUDE.local.md"), "# app")
	assertFileContains(t, filepath.Join(instanceRoot, "public", "app", "CLAUDE.local.md"), "Group: public")

	// Verify subdir CLAUDE.local.md.
	assertFileContains(t, filepath.Join(docsDir, "CLAUDE.local.md"), "# app docs subdir")

	// Verify auto-discovered repo content for "secrets" (no explicit entry,
	// but repos/secrets.md exists in content_dir).
	assertFileContains(t, filepath.Join(instanceRoot, "private", "secrets", "CLAUDE.local.md"), "# Auto-discovered secrets")
}

func TestApplyIntegrationNoContent(t *testing.T) {
	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configTOML := `
[workspace]
name = "minimal"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"},
			},
		},
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	instanceRoot := filepath.Join(tmpDir, "minimal")
	repoDir := filepath.Join(instanceRoot, "all", "repo1")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Apply should succeed even with no content sections.
	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// No CLAUDE files should be created.
	if _, err := os.Stat(filepath.Join(instanceRoot, "CLAUDE.md")); err == nil {
		t.Error("workspace CLAUDE.md should not be created with no content config")
	}
}

func assertFileContains(t *testing.T, path, substr string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(data), substr) {
		t.Errorf("%s missing %q:\n%s", path, substr, data)
	}
}

func assertFileNotContains(t *testing.T, path, substr string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if strings.Contains(string(data), substr) {
		t.Errorf("%s unexpectedly contains %q:\n%s", path, substr, data)
	}
}
