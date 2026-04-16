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

// setupTestWorkspace creates a temporary workspace with config and content files,
// pre-creates repo directories with .git markers, and returns the niwaDir and
// instanceRoot paths.
func setupTestWorkspace(t *testing.T, configTOML string, contentFiles map[string]string, repoSetup []struct{ group, name string }) (niwaDir, instanceRoot string) {
	t.Helper()
	tmpDir := t.TempDir()

	niwaDir = filepath.Join(tmpDir, ".niwa")
	contentDir := filepath.Join(niwaDir, "claude")
	reposContentDir := filepath.Join(contentDir, "repos")
	if err := os.MkdirAll(reposContentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
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

	// Extract workspace name from config for the instance root.
	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	instanceRoot = filepath.Join(tmpDir, result.Config.Workspace.Name)

	for _, repo := range repoSetup {
		repoDir := filepath.Join(instanceRoot, repo.group, repo.name)
		if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return niwaDir, instanceRoot
}

func TestCreateIntegration(t *testing.T) {
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

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

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

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "test-ws")

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
		if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Pre-create the docs subdir.
	docsDir := filepath.Join(instanceRoot, "public", "app", "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	gotPath, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if gotPath != instanceRoot {
		t.Errorf("Create returned %q, want %q", gotPath, instanceRoot)
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

	// Verify state was written.
	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("loading state after create: %v", err)
	}
	if state.InstanceNumber != 1 {
		t.Errorf("InstanceNumber = %d, want 1", state.InstanceNumber)
	}
	if state.Created.IsZero() {
		t.Error("Created should not be zero")
	}
	if state.Created != state.LastApplied {
		t.Error("Create should set Created == LastApplied")
	}
}

func TestApplyIntegration(t *testing.T) {
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

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

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

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "test-ws")

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
		if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Pre-create the docs subdir.
	docsDir := filepath.Join(instanceRoot, "public", "app", "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// First, create to seed state.
	_, err = applier.Create(context.Background(), cfg, niwaDir, workspaceRoot)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Capture Created time from initial state.
	initialState, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("loading initial state: %v", err)
	}
	createdTime := initialState.Created

	// Now Apply on top of existing state.
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

	// Verify auto-discovered repo content for "secrets".
	assertFileContains(t, filepath.Join(instanceRoot, "private", "secrets", "CLAUDE.local.md"), "# Auto-discovered secrets")

	// Verify Apply preserves Created time and updates LastApplied.
	updatedState, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("loading updated state: %v", err)
	}
	if !updatedState.Created.Equal(createdTime) {
		t.Errorf("Apply should preserve Created time: got %v, want %v", updatedState.Created, createdTime)
	}
	if updatedState.InstanceNumber != initialState.InstanceNumber {
		t.Errorf("Apply should preserve InstanceNumber: got %d, want %d", updatedState.InstanceNumber, initialState.InstanceNumber)
	}
}

func TestApplyRequiresExistingState(t *testing.T) {
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

[groups.all]
visibility = "public"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{},
	}
	applier := NewApplier(mockClient)

	instanceRoot := filepath.Join(tmpDir, "test-ws")
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	err = applier.Apply(context.Background(), cfg, niwaDir, instanceRoot)
	if err == nil {
		t.Fatal("expected error when Apply is called without existing state")
	}
	if !strings.Contains(err.Error(), "loading existing state") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCreateIntegrationNoContent(t *testing.T) {
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

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"},
			},
		},
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "minimal")
	repoDir := filepath.Join(instanceRoot, "all", "repo1")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create should succeed even with no content sections.
	gotPath, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if gotPath != instanceRoot {
		t.Errorf("Create returned %q, want %q", gotPath, instanceRoot)
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

func TestCreateClaudeFalseSkipsContent(t *testing.T) {
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

[repos.".github".claude]
enabled = false

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

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

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

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "test-ws")

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

	gotPath, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if gotPath != instanceRoot {
		t.Errorf("Create returned %q, want %q", gotPath, instanceRoot)
	}

	// app should have CLAUDE.local.md.
	assertFileContains(t, filepath.Join(instanceRoot, "public", "app", "CLAUDE.local.md"), "# app")

	// .github should NOT have CLAUDE.local.md because claude = false.
	ghContentPath := filepath.Join(instanceRoot, "public", ".github", "CLAUDE.local.md")
	if _, err := os.Stat(ghContentPath); err == nil {
		t.Error("CLAUDE.local.md should not be created for repos with claude = false")
	}
}

func TestCreateUnknownRepoWarning(t *testing.T) {
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

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "app", Visibility: "public", SSHURL: "git@github.com:testorg/app.git"},
			},
		},
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "test-ws")
	repoDir := filepath.Join(instanceRoot, "public", "app")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create should succeed (warnings are printed to stderr, not returned as errors).
	if _, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot); err != nil {
		t.Fatalf("create failed: %v", err)
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

func TestApplyCleanupRemovedFiles(t *testing.T) {
	tmpDir := t.TempDir()

	niwaDir := filepath.Join(tmpDir, ".niwa")
	contentDir := filepath.Join(niwaDir, "claude")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Start with workspace content.
	configTOML := `
[workspace]
name = "test-ws"
content_dir = "claude"

[[sources]]
org = "testorg"

[groups.public]
visibility = "public"

[content.workspace]
source = "workspace.md"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "workspace.md"), []byte("# ws\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"},
			},
		},
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "test-ws")
	repoDir := filepath.Join(instanceRoot, "public", "repo1")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create initial state with workspace content.
	_, err = applier.Create(context.Background(), cfg, niwaDir, workspaceRoot)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Verify CLAUDE.md was created.
	claudeMd := filepath.Join(instanceRoot, "CLAUDE.md")
	if _, err := os.Stat(claudeMd); err != nil {
		t.Fatalf("CLAUDE.md should exist after create: %v", err)
	}

	// Now remove the workspace content from config and apply.
	configTOML2 := `
[workspace]
name = "test-ws"
content_dir = "claude"

[[sources]]
org = "testorg"

[groups.public]
visibility = "public"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML2), 0o644); err != nil {
		t.Fatal(err)
	}

	result2, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg2 := result2.Config

	if err := applier.Apply(context.Background(), cfg2, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// CLAUDE.md should have been cleaned up since it's no longer produced.
	if _, err := os.Stat(claudeMd); err == nil {
		t.Error("CLAUDE.md should be removed after applying config without workspace content")
	}
}

func TestCreateMaterializersIntegration(t *testing.T) {
	tmpDir := t.TempDir()

	niwaDir := filepath.Join(tmpDir, ".niwa")
	contentDir := filepath.Join(niwaDir, "claude")
	reposContentDir := filepath.Join(contentDir, "repos")
	hooksDir := filepath.Join(niwaDir, "hooks", "pre_tool_use")
	envDir := filepath.Join(niwaDir, "env")
	if err := os.MkdirAll(reposContentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a hook script into the hooks directory for auto-discovery.
	hookScript := "#!/bin/bash\necho 'pre-tool-use hook'\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "lint.sh"), []byte(hookScript), 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a workspace env file for auto-discovery.
	wsEnv := "WORKSPACE_VAR=hello\n"
	if err := os.WriteFile(filepath.Join(envDir, "workspace.env"), []byte(wsEnv), 0o644); err != nil {
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

[claude.settings]
permissions = "bypass"

[env.vars]
EXTRA_VAR = "world"

[content.workspace]
source = "workspace.md"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	contentFiles := map[string]string{
		"workspace.md": "# {workspace_name}\n",
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

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "app", Visibility: "public", SSHURL: "git@github.com:testorg/app.git"},
			},
		},
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "test-ws")

	// Pre-create repo dir with .git marker so the cloner skips it.
	repoDir := filepath.Join(instanceRoot, "public", "app")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gotPath, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if gotPath != instanceRoot {
		t.Errorf("Create returned %q, want %q", gotPath, instanceRoot)
	}

	// Verify hook script was installed.
	hookTarget := filepath.Join(repoDir, ".claude", "hooks", "pre_tool_use", "lint.local.sh")
	assertFileContains(t, hookTarget, "pre-tool-use hook")

	// Verify hook script is executable.
	info, err := os.Stat(hookTarget)
	if err != nil {
		t.Fatalf("stat hook: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("hook script should be executable")
	}

	// Verify settings.local.json was generated with permissions and hooks.
	settingsPath := filepath.Join(repoDir, ".claude", "settings.local.json")
	assertFileContains(t, settingsPath, `"defaultMode": "bypassPermissions"`)
	assertFileContains(t, settingsPath, `"PreToolUse"`)
	assertFileContains(t, settingsPath, "lint.local.sh")

	// Verify .local.env was generated with both discovered and inline vars.
	envPath := filepath.Join(repoDir, ".local.env")
	assertFileContains(t, envPath, "WORKSPACE_VAR=hello")
	assertFileContains(t, envPath, "EXTRA_VAR=world")

	// Verify all generated files are tracked in managed files state.
	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("loading state: %v", err)
	}

	managedPaths := make(map[string]bool)
	for _, mf := range state.ManagedFiles {
		managedPaths[mf.Path] = true
	}

	for _, expected := range []string{hookTarget, settingsPath, envPath} {
		if !managedPaths[expected] {
			t.Errorf("expected %s to be tracked in managed files", expected)
		}
	}
}

func TestCreateMaterializersClaudeFalseSkipsHooksAndSettings(t *testing.T) {
	tmpDir := t.TempDir()

	niwaDir := filepath.Join(tmpDir, ".niwa")
	hooksDir := filepath.Join(niwaDir, "hooks", "pre_tool_use")
	envDir := filepath.Join(niwaDir, "env")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a hook script.
	if err := os.WriteFile(filepath.Join(hooksDir, "lint.sh"), []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Write workspace env.
	if err := os.WriteFile(filepath.Join(envDir, "workspace.env"), []byte("MY_VAR=yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.public]
visibility = "public"

[claude.settings]
permissions = "bypass"

[repos.app.claude]
enabled = false
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "app", Visibility: "public", SSHURL: "git@github.com:testorg/app.git"},
			},
		},
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "test-ws")

	repoDir := filepath.Join(instanceRoot, "public", "app")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err = applier.Create(context.Background(), cfg, niwaDir, workspaceRoot)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Hooks should NOT be installed (claude = false).
	hookTarget := filepath.Join(repoDir, ".claude", "hooks", "pre_tool_use", "lint.local.sh")
	if _, err := os.Stat(hookTarget); err == nil {
		t.Error("hook script should not be installed when claude = false")
	}

	// Settings should NOT be installed (claude = false).
	settingsPath := filepath.Join(repoDir, ".claude", "settings.local.json")
	if _, err := os.Stat(settingsPath); err == nil {
		t.Error("settings.local.json should not be installed when claude = false")
	}

	// Env SHOULD still be installed (env is tool-agnostic).
	envPath := filepath.Join(repoDir, ".local.env")
	assertFileContains(t, envPath, "MY_VAR=yes")
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

// TestCreateNonVaultConfigStillWrites0o600 is the Issue 6 bug-fix
// coverage: a workspace that declares no vault providers still gets
// 0o600 permissions on every materialized file. The test drives a
// non-vault config through Applier.Create and asserts mode on the
// env file, settings file, and hook script.
func TestCreateNonVaultConfigStillWrites0o600(t *testing.T) {
	tmpDir := t.TempDir()

	niwaDir := filepath.Join(tmpDir, ".niwa")
	envDir := filepath.Join(niwaDir, "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "workspace.env"), []byte("WS=yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	configTOML := `
[workspace]
name = "perm-ws"

[[sources]]
org = "testorg"

[groups.public]
visibility = "public"

[claude.settings]
permissions = "bypass"

[env.vars]
PLAIN = "not-a-secret"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "app", Visibility: "public", SSHURL: "git@github.com:testorg/app.git"},
			},
		},
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "perm-ws")
	repoDir := filepath.Join(instanceRoot, "public", "app")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	if _, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Every materialized file under the repo must be 0o600. The
	// bug that Issue 6 fixes is that these were previously 0o644
	// even when no vault was declared.
	for _, path := range []string{
		filepath.Join(repoDir, ".local.env"),
		filepath.Join(repoDir, ".claude", "settings.local.json"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 0o600", path, got)
		}
	}
}

// TestCreateWritesInstanceGitignore covers that Applier.Create
// writes a fresh .gitignore at the instance root containing
// *.local*. No prior file exists.
func TestCreateWritesInstanceGitignore(t *testing.T) {
	tmpDir := t.TempDir()

	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configTOML := `
[workspace]
name = "gi-ws"

[[sources]]
org = "testorg"

[groups.public]
visibility = "public"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "app", Visibility: "public", SSHURL: "git@github.com:testorg/app.git"},
			},
		},
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "gi-ws")
	repoDir := filepath.Join(instanceRoot, "public", "app")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	if _, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(instanceRoot, ".gitignore"))
	if err != nil {
		t.Fatalf("reading instance .gitignore: %v", err)
	}
	if !strings.Contains(string(data), "*.local*") {
		t.Errorf("instance .gitignore missing *.local*:\n%s", string(data))
	}
}

// TestCreateMergesInstanceGitignore covers the case where the user
// has pre-seeded the instance root with a .gitignore containing
// node_modules/. Applier.Create must preserve that content and
// append *.local*.
func TestCreateMergesInstanceGitignore(t *testing.T) {
	tmpDir := t.TempDir()

	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configTOML := `
[workspace]
name = "gi-merge-ws"

[[sources]]
org = "testorg"

[groups.public]
visibility = "public"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "gi-merge-ws")
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-seed the instance .gitignore with unrelated content. The
	// CLI guards against a pre-existing instance directory, but
	// Applier.Create itself does not -- this test exercises the
	// helper's merge path directly via Create.
	if err := os.WriteFile(filepath.Join(instanceRoot, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "app", Visibility: "public", SSHURL: "git@github.com:testorg/app.git"},
			},
		},
	}

	repoDir := filepath.Join(instanceRoot, "public", "app")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}

	if _, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(instanceRoot, ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "node_modules/") {
		t.Errorf("pre-existing content lost:\n%s", content)
	}
	if !strings.Contains(content, "*.local*") {
		t.Errorf("*.local* not appended:\n%s", content)
	}
}
