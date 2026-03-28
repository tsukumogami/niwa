package workspace

import (
	"context"
	"fmt"
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

func TestDiscoverReposThresholdDefault(t *testing.T) {
	// An org returning more repos than DefaultMaxRepos should fail.
	repos := make([]github.Repo, DefaultMaxRepos+1)
	for i := range repos {
		repos[i] = github.Repo{Name: fmt.Sprintf("repo-%d", i), Visibility: "public"}
	}

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{"bigorg": repos},
	}

	applier := NewApplier(mockClient)
	_, err := applier.discoverAllRepos(context.Background(), []config.SourceConfig{
		{Org: "bigorg"},
	})
	if err == nil {
		t.Fatal("expected error when repo count exceeds default threshold")
	}
	if !strings.Contains(err.Error(), "exceeds the max_repos threshold") {
		t.Errorf("unexpected error message: %v", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("threshold of %d", DefaultMaxRepos)) {
		t.Errorf("error should mention default threshold %d: %v", DefaultMaxRepos, err)
	}
}

func TestDiscoverReposThresholdOverride(t *testing.T) {
	// Setting max_repos on the source should override the default.
	repos := make([]github.Repo, 15)
	for i := range repos {
		repos[i] = github.Repo{Name: fmt.Sprintf("repo-%d", i), Visibility: "public"}
	}

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{"bigorg": repos},
	}

	applier := NewApplier(mockClient)

	// Should fail with default (10).
	_, err := applier.discoverAllRepos(context.Background(), []config.SourceConfig{
		{Org: "bigorg"},
	})
	if err == nil {
		t.Fatal("expected error with default threshold")
	}

	// Should succeed with override to 20.
	result, err := applier.discoverAllRepos(context.Background(), []config.SourceConfig{
		{Org: "bigorg", MaxRepos: 20},
	})
	if err != nil {
		t.Fatalf("unexpected error with raised threshold: %v", err)
	}
	if len(result) != 15 {
		t.Errorf("expected 15 repos, got %d", len(result))
	}
}

func TestDiscoverReposExplicitList(t *testing.T) {
	// A source with explicit repos should skip the API entirely.
	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			// Even though the API would return these, they should be ignored.
			"myorg": {
				{Name: "api-should-not-be-called", Visibility: "public"},
			},
		},
	}

	applier := NewApplier(mockClient)
	result, err := applier.discoverAllRepos(context.Background(), []config.SourceConfig{
		{Org: "myorg", Repos: []string{"frontend", "backend"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(result))
	}
	if result[0].Name != "frontend" || result[1].Name != "backend" {
		t.Errorf("unexpected repo names: %v", result)
	}
	if result[0].SSHURL != "git@github.com:myorg/frontend.git" {
		t.Errorf("unexpected SSH URL: %s", result[0].SSHURL)
	}
}

func TestDiscoverReposMultiSourceMerge(t *testing.T) {
	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"org-a": {
				{Name: "alpha", Visibility: "public"},
			},
			"org-b": {
				{Name: "beta", Visibility: "private"},
			},
		},
	}

	applier := NewApplier(mockClient)
	result, err := applier.discoverAllRepos(context.Background(), []config.SourceConfig{
		{Org: "org-a"},
		{Org: "org-b"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 repos from two sources, got %d", len(result))
	}
	names := map[string]bool{}
	for _, r := range result {
		names[r.Name] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("expected alpha and beta, got %v", names)
	}
}

func TestDiscoverReposDuplicateAcrossSources(t *testing.T) {
	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"org-a": {
				{Name: "shared", Visibility: "public"},
			},
			"org-b": {
				{Name: "shared", Visibility: "private"},
			},
		},
	}

	applier := NewApplier(mockClient)
	_, err := applier.discoverAllRepos(context.Background(), []config.SourceConfig{
		{Org: "org-a"},
		{Org: "org-b"},
	})
	if err == nil {
		t.Fatal("expected error for duplicate repo names across sources")
	}
	if !strings.Contains(err.Error(), "duplicate repo name") {
		t.Errorf("unexpected error message: %v", err)
	}
	if !strings.Contains(err.Error(), "shared") {
		t.Errorf("error should mention the duplicate repo name: %v", err)
	}
}

func TestDiscoverReposExplicitListSkipsThreshold(t *testing.T) {
	// Explicit repos should not be subject to the max_repos threshold.
	names := make([]string, 20)
	for i := range names {
		names[i] = fmt.Sprintf("repo-%d", i)
	}

	mockClient := &mockGitHubClient{repos: map[string][]github.Repo{}}
	applier := NewApplier(mockClient)

	result, err := applier.discoverAllRepos(context.Background(), []config.SourceConfig{
		{Org: "myorg", Repos: names},
	})
	if err != nil {
		t.Fatalf("explicit list should not be subject to threshold: %v", err)
	}
	if len(result) != 20 {
		t.Errorf("expected 20 repos, got %d", len(result))
	}
}

func TestApplyClaudeFalseSkipsContent(t *testing.T) {
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

[repos.".github"]
claude = false

[content.workspace]
source = "workspace.md"

[content.repos.app]
source = "repos/app.md"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	contentFiles := map[string]string{
		"workspace.md": "# {workspace_name}\n",
		"repos/app.md": "# {repo_name}\n",
		// Auto-discoverable content for .github -- should be skipped.
		"repos/.github.md": "# Should not appear\n",
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
				{Name: ".github", Visibility: "public", SSHURL: "git@github.com:testorg/.github.git"},
			},
		},
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	instanceRoot := filepath.Join(tmpDir, "test-ws")

	// Pre-create repo dirs with .git markers so the cloner skips them.
	for _, repo := range []string{"app", ".github"} {
		repoDir := filepath.Join(instanceRoot, "public", repo)
		if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// app should have CLAUDE.local.md.
	assertFileContains(t, filepath.Join(instanceRoot, "public", "app", "CLAUDE.local.md"), "# app")

	// .github should NOT have CLAUDE.local.md because claude = false.
	ghContentPath := filepath.Join(instanceRoot, "public", ".github", "CLAUDE.local.md")
	if _, err := os.Stat(ghContentPath); err == nil {
		t.Error("CLAUDE.local.md should not be created for repos with claude = false")
	}
}

func TestApplyUnknownRepoWarning(t *testing.T) {
	tmpDir := t.TempDir()

	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.public]
visibility = "public"

[repos.nonexistent]
scope = "strategic"
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
				{Name: "app", Visibility: "public", SSHURL: "git@github.com:testorg/app.git"},
			},
		},
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	instanceRoot := filepath.Join(tmpDir, "test-ws")
	repoDir := filepath.Join(instanceRoot, "public", "app")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Apply should succeed (warnings are printed to stderr, not returned as errors).
	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
}

func TestApplyRepoURLOverride(t *testing.T) {
	// Verify that RepoCloneURL picks the override URL.
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"myrepo": {URL: "git@gitlab.com:custom/myrepo.git"},
		},
	}

	got := RepoCloneURL(ws, "myrepo", "git@github.com:org/myrepo.git", "https://github.com/org/myrepo.git")
	want := "git@gitlab.com:custom/myrepo.git"
	if got != want {
		t.Errorf("RepoCloneURL = %q, want %q", got, want)
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
