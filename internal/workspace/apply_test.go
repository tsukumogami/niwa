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
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/fake"
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

// setupOverlayDir creates a fake overlay directory with a workspace-overlay.toml
// and returns its path. Used by overlay integration tests to avoid running git.
func setupOverlayDir(t *testing.T, overlayTOML string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), []byte(overlayTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestApplyOverlayNoOverlay verifies that when NoOverlay=true in instance state,
// the overlay is skipped entirely (no cloneOrSync called, base config used).
// Scenario 15.
func TestApplyOverlayNoOverlay(t *testing.T) {
	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"all", "repo1"},
	})

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

	// Seed initial state with NoOverlay=true and OverlayURL set (state field
	// must be respected even if OverlayURL is present in state).
	initialState := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
		NoOverlay:      true,
		OverlayURL:     "testorg/test-ws-overlay",
	}
	if err := SaveState(instanceRoot, initialState); err != nil {
		t.Fatal(err)
	}

	cloneCallCount := 0
	cloneFn := func(url, dir string) (bool, error) {
		cloneCallCount++
		return false, nil
	}
	headFn := func(dir string) (string, error) {
		return "abc123", nil
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.cloneOrSync = cloneFn
	applier.headSHA = headFn

	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if cloneCallCount != 0 {
		t.Errorf("cloneOrSync should not be called when NoOverlay=true, but was called %d time(s)", cloneCallCount)
	}

	// State should preserve NoOverlay=true.
	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("loading state: %v", err)
	}
	if !state.NoOverlay {
		t.Error("NoOverlay should be preserved in state after apply")
	}
}

// TestApplyOverlaySyncFailure verifies that when OverlayURL is set in state and
// CloneOrSyncOverlay returns a sync failure (firstTime=false), apply returns
// a non-revealing error message. Scenario 16.
func TestApplyOverlaySyncFailure(t *testing.T) {
	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"all", "repo1"},
	})

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

	// Seed state with OverlayURL set.
	initialState := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
		OverlayURL:     "testorg/test-ws-overlay",
		OverlayCommit:  "abc123",
	}
	if err := SaveState(instanceRoot, initialState); err != nil {
		t.Fatal(err)
	}

	// Stub: sync failure (firstTime=false).
	cloneFn := func(url, dir string) (bool, error) {
		return false, fmt.Errorf("git pull failed: network error")
	}
	headFn := func(dir string) (string, error) {
		return "", fmt.Errorf("no HEAD")
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.cloneOrSync = cloneFn
	applier.headSHA = headFn

	err = applier.Apply(context.Background(), cfg, niwaDir, instanceRoot)
	if err == nil {
		t.Fatal("expected error on overlay sync failure")
	}
	if !strings.Contains(err.Error(), "workspace overlay sync failed") {
		t.Errorf("expected non-revealing error message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--no-overlay") {
		t.Errorf("expected --no-overlay hint in error message, got: %v", err)
	}
	// Error must NOT reveal internal git details.
	if strings.Contains(err.Error(), "git pull") || strings.Contains(err.Error(), "network error") {
		t.Errorf("error message should not reveal internal details, got: %v", err)
	}
}

// TestApplyOverlayReApply verifies the most common overlay path: OverlayURL is
// already in instance.json from a prior init or apply (branch 2 of step 0.5),
// the sync succeeds, the overlay config is merged into the workspace, and the
// pipeline completes without error. This is the path taken on every subsequent
// apply after the overlay has been discovered.
func TestApplyOverlayReApply(t *testing.T) {
	overlayTOML := `
[env.vars]
OVERLAY_ACTIVE = "true"
`
	overlayDir := setupOverlayDir(t, overlayTOML)

	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"all", "repo1"},
	})

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

	// State already has OverlayURL — simulates a re-apply after initial discovery.
	initialState := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
		OverlayURL:     "testorg/test-ws-overlay",
		OverlayCommit:  "deadbeef",
	}
	if err := SaveState(instanceRoot, initialState); err != nil {
		t.Fatal(err)
	}

	// Stub: sync succeeds (wasCloneAttempt=false, nil error); copies overlay TOML.
	cloneFn := func(url, dir string) (bool, error) {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			return false, err
		}
		data, err := os.ReadFile(filepath.Join(overlayDir, "workspace-overlay.toml"))
		if err != nil {
			return false, err
		}
		return false, os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), data, 0o644)
	}
	headFn := func(dir string) (string, error) { return "deadbeef", nil }

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.cloneOrSync = cloneFn
	applier.headSHA = headFn

	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("re-apply with overlay failed: %v", err)
	}

	// State OverlayURL must be preserved after re-apply.
	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("loading state after re-apply: %v", err)
	}
	if state.OverlayURL != "testorg/test-ws-overlay" {
		t.Errorf("OverlayURL = %q after re-apply, want testorg/test-ws-overlay", state.OverlayURL)
	}
}

// TestApplyOverlayConventionDiscovery verifies that when no OverlayURL is in state
// and ConfigSourceURL is set, convention discovery derives the overlay URL, clones
// the overlay, and writes OverlayURL + OverlayCommit to state. Scenario 17.
func TestApplyOverlayConventionDiscovery(t *testing.T) {
	overlayTOML := `
[[sources]]
org = "overlayorg"
repos = ["extra-repo"]
`
	overlayDir := setupOverlayDir(t, overlayTOML)

	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"all", "repo1"},
		{"all", "extra-repo"},
	})

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
			"overlayorg": {
				{Name: "extra-repo", Visibility: "public", SSHURL: "git@github.com:overlayorg/extra-repo.git"},
			},
		},
	}

	// Seed state with no OverlayURL.
	initialState := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
	}
	if err := SaveState(instanceRoot, initialState); err != nil {
		t.Fatal(err)
	}

	const fakeHeadSHA = "deadbeef1234567890"
	// Stub: successful clone that points to our pre-created overlayDir.
	// The actual overlay dir used by config.OverlayDir will be a tmpdir;
	// we simulate success by copying workspace-overlay.toml into it.
	cloneFn := func(url, dir string) (bool, error) {
		// Simulate successful first-time clone.
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			return true, err
		}
		// Copy workspace-overlay.toml from our pre-made overlayDir.
		data, err := os.ReadFile(filepath.Join(overlayDir, "workspace-overlay.toml"))
		if err != nil {
			return true, err
		}
		return true, os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), data, 0o644)
	}
	headFn := func(dir string) (string, error) {
		return fakeHeadSHA, nil
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.cloneOrSync = cloneFn
	applier.headSHA = headFn
	// ConfigSourceURL must be a parseable GitHub URL for DeriveOverlayURL.
	applier.ConfigSourceURL = "https://github.com/testorg/test-ws"

	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply with convention discovery failed: %v", err)
	}

	// OverlayURL and OverlayCommit must be written to state.
	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("loading state after apply: %v", err)
	}
	if state.OverlayURL != "testorg/test-ws-overlay" {
		t.Errorf("OverlayURL = %q, want %q", state.OverlayURL, "testorg/test-ws-overlay")
	}
	if state.OverlayCommit != fakeHeadSHA {
		t.Errorf("OverlayCommit = %q, want %q", state.OverlayCommit, fakeHeadSHA)
	}
}

// TestApplyOverlayConventionDiscoveryFirstTimeFailure verifies that when convention
// discovery fails on first attempt (firstTime=true error), apply succeeds silently
// without writing OverlayURL to state. Scenario 15 (no-overlay variant).
func TestApplyOverlayConventionDiscoveryFirstTimeFailure(t *testing.T) {
	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"all", "repo1"},
	})

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

	initialState := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
	}
	if err := SaveState(instanceRoot, initialState); err != nil {
		t.Fatal(err)
	}

	// Stub: first-time clone failure.
	cloneFn := func(url, dir string) (bool, error) {
		return true, fmt.Errorf("repository not found")
	}
	headFn := func(dir string) (string, error) {
		return "", fmt.Errorf("no HEAD")
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.cloneOrSync = cloneFn
	applier.headSHA = headFn
	applier.ConfigSourceURL = "https://github.com/testorg/test-ws"

	// Apply should succeed silently.
	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply should succeed when convention discovery fails silently: %v", err)
	}

	// OverlayURL must NOT be written to state.
	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("loading state: %v", err)
	}
	if state.OverlayURL != "" {
		t.Errorf("OverlayURL should be empty after first-time failure, got: %q", state.OverlayURL)
	}
}

// TestApplyOverlaySHAMismatchWarning verifies that when the overlay HEAD SHA differs
// from the stored OverlayCommit, apply emits a warning to stderr but continues
// successfully. Scenario 18.
func TestApplyOverlaySHAMismatchWarning(t *testing.T) {
	overlayTOML := `
[[sources]]
org = "overlayorg"
repos = ["extra-repo"]
`
	overlayDir := setupOverlayDir(t, overlayTOML)

	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"all", "repo1"},
		{"all", "extra-repo"},
	})

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
			"overlayorg": {
				{Name: "extra-repo", Visibility: "public", SSHURL: "git@github.com:overlayorg/extra-repo.git"},
			},
		},
	}

	// Seed state with old OverlayCommit so the new HEAD triggers the warning.
	initialState := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
		OverlayURL:     "testorg/test-ws-overlay",
		OverlayCommit:  "abc123", // old SHA
	}
	if err := SaveState(instanceRoot, initialState); err != nil {
		t.Fatal(err)
	}

	cloneFn := func(url, dir string) (bool, error) {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			return false, err
		}
		data, err := os.ReadFile(filepath.Join(overlayDir, "workspace-overlay.toml"))
		if err != nil {
			return false, err
		}
		return false, os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), data, 0o644)
	}
	// headSHA returns a different SHA than OverlayCommit to trigger the warning.
	headFn := func(dir string) (string, error) {
		return "def456newsha", nil
	}

	// Capture stderr by redirecting os.Stderr through a pipe.
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("creating pipe: %v", pipeErr)
	}
	oldStderr := os.Stderr
	os.Stderr = w

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.cloneOrSync = cloneFn
	applier.headSHA = headFn

	applyErr := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot)

	// Restore stderr before checking results.
	os.Stderr = oldStderr
	w.Close()
	var stderrBuf strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			stderrBuf.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}
	r.Close()

	if applyErr != nil {
		t.Fatalf("apply with SHA mismatch should succeed, got: %v", applyErr)
	}

	stderrOutput := stderrBuf.String()
	const wantWarning = "workspace overlay has new commits since last apply"
	if !strings.Contains(stderrOutput, wantWarning) {
		t.Errorf("stderr missing SHA mismatch warning %q; got: %s", wantWarning, stderrOutput)
	}
}

// TestApplyOverlayWithValidOverlay verifies that when an overlay is active, overlay
// repos/groups/content appear in the pipeline output. Scenario 19.
//
// The overlay merge now happens BEFORE discoverAllRepos, so the merged config
// (base + overlay) feeds into discovery. extra-repo from the overlay source must
// appear in the final state's Repos map — not just as a pre-existing directory.
func TestApplyOverlayWithValidOverlay(t *testing.T) {
	overlayTOML := `
[[sources]]
org = "overlayorg"
repos = ["extra-repo"]

[groups.overlay-group]
repos = ["extra-repo"]
`
	overlayDir := setupOverlayDir(t, overlayTOML)

	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`
	// Pre-create repo directories with .git markers so the Cloner skips actual git
	// operations. extra-repo lives under overlay-group (defined by the overlay),
	// not under "all" (the base group). Its presence in state.Repos confirms it
	// was discovered through the overlay source after the merge ran before
	// discoverAllRepos.
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"all", "repo1"},
		{"overlay-group", "extra-repo"},
	})

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
			"overlayorg": {
				{Name: "extra-repo", Visibility: "public", SSHURL: "git@github.com:overlayorg/extra-repo.git"},
			},
		},
	}

	// Seed state with OverlayURL already set (as if init discovered it).
	initialState := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
		OverlayURL:     "testorg/test-ws-overlay",
		OverlayCommit:  "abc123",
	}
	if err := SaveState(instanceRoot, initialState); err != nil {
		t.Fatal(err)
	}

	const fakeHeadSHA = "abc123" // same as OverlayCommit, so no warn-on-advance
	cloneFn := func(url, dir string) (bool, error) {
		// Simulate successful sync; copy workspace-overlay.toml into dir.
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			return false, err
		}
		data, err := os.ReadFile(filepath.Join(overlayDir, "workspace-overlay.toml"))
		if err != nil {
			return false, err
		}
		return false, os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), data, 0o644)
	}
	headFn := func(dir string) (string, error) {
		return fakeHeadSHA, nil
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.cloneOrSync = cloneFn
	applier.headSHA = headFn

	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply with overlay failed: %v", err)
	}

	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("loading state: %v", err)
	}

	// extra-repo must appear in the state Repos map, proving it was discovered
	// via the overlay source (overlayorg) after the overlay merge ran before
	// discoverAllRepos. A pre-seeded directory alone would not produce this entry.
	if _, ok := state.Repos["extra-repo"]; !ok {
		t.Error("extra-repo from overlay source should appear in state Repos map after apply")
	}

	// repo1 from the base source must also be present.
	if _, ok := state.Repos["repo1"]; !ok {
		t.Error("repo1 from base source should appear in state Repos map after apply")
	}

	// State should preserve the overlay fields.
	if state.OverlayURL != "testorg/test-ws-overlay" {
		t.Errorf("OverlayURL = %q, want %q", state.OverlayURL, "testorg/test-ws-overlay")
	}
}

// newFakeVaultRegistry returns a vault.Registry with the fake backend registered.
// Tests use this to inject known secrets without touching vault.DefaultRegistry.
func newFakeVaultRegistry(t *testing.T) *vault.Registry {
	t.Helper()
	reg := vault.NewRegistry()
	if err := reg.Register(fake.NewFactory()); err != nil {
		t.Fatalf("register fake vault factory: %v", err)
	}
	return reg
}

// TestApplyOverlayVaultProvider verifies R23: when workspace-overlay.toml declares
// [vault.provider], vault:// references in the overlay's [env.secrets] are resolved
// against that provider before the overlay is merged into the base config. The
// resolved secret values land in per-repo .local.env files alongside base env vars.
func TestApplyOverlayVaultProvider(t *testing.T) {
	// Overlay declares a fake vault provider and resolves a secret.
	overlayTOML := `
[vault.provider]
kind = "fake"

[vault.provider.values]
OVERLAY_SECRET = "resolved-from-overlay-vault"

[env.secrets]
OVERLAY_SECRET = "vault://OVERLAY_SECRET"
`
	overlayDir := setupOverlayDir(t, overlayTOML)

	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"

[env.secrets.required]
OVERLAY_SECRET = "Secret resolved by overlay vault"
`
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"all", "repo1"},
	})

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
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

	initialState := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
		OverlayURL:     "testorg/test-ws-overlay",
		OverlayCommit:  "deadbeef",
	}
	if err := SaveState(instanceRoot, initialState); err != nil {
		t.Fatal(err)
	}

	cloneFn := func(url, dir string) (bool, error) {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			return false, err
		}
		data, err := os.ReadFile(filepath.Join(overlayDir, "workspace-overlay.toml"))
		if err != nil {
			return false, err
		}
		return false, os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), data, 0o644)
	}

	applier := NewApplier(mockClient)
	applier.cloneOrSync = cloneFn
	applier.headSHA = func(string) (string, error) { return "deadbeef", nil }
	applier.vaultRegistry = newFakeVaultRegistry(t)

	if err := applier.Apply(context.Background(), result.Config, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply with overlay vault failed: %v", err)
	}

	// The resolved secret must appear in repo1's .local.env.
	envPath := filepath.Join(instanceRoot, "all", "repo1", ".local.env")
	assertFileContains(t, envPath, "OVERLAY_SECRET=")
	assertFileContains(t, envPath, "resolved-from-overlay-vault")
}

// TestApplyOverlayVaultSecretTiers verifies R24: base config declarations in
// [env.secrets.required] and [env.secrets.recommended] are enforced after the
// overlay vault provides values for the required keys.
func TestApplyOverlayVaultSecretTiers(t *testing.T) {
	// Overlay provides vault resolution for the required key only.
	overlayTOML := `
[vault.provider]
kind = "fake"

[vault.provider.values]
REQUIRED_KEY = "required-value"

[env.secrets]
REQUIRED_KEY = "vault://REQUIRED_KEY"
`
	overlayDir := setupOverlayDir(t, overlayTOML)

	// Base config declares REQUIRED_KEY as required and OPTIONAL_KEY as recommended.
	// OPTIONAL_KEY is not provided — apply should succeed with a warning.
	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"

[env.secrets.required]
REQUIRED_KEY = "Must be present - resolved by overlay vault"

[env.secrets.recommended]
RECOMMENDED_KEY = "Should be present - resolved by overlay vault"
`
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"all", "repo1"},
	})

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
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

	initialState := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
		OverlayURL:     "testorg/test-ws-overlay",
		OverlayCommit:  "deadbeef",
	}
	if err := SaveState(instanceRoot, initialState); err != nil {
		t.Fatal(err)
	}

	cloneFn := func(url, dir string) (bool, error) {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			return false, err
		}
		data, err := os.ReadFile(filepath.Join(overlayDir, "workspace-overlay.toml"))
		if err != nil {
			return false, err
		}
		return false, os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), data, 0o644)
	}

	applier := NewApplier(mockClient)
	applier.cloneOrSync = cloneFn
	applier.headSHA = func(string) (string, error) { return "deadbeef", nil }
	applier.vaultRegistry = newFakeVaultRegistry(t)

	// Apply succeeds: REQUIRED_KEY is resolved, RECOMMENDED_KEY is absent (warning only).
	if err := applier.Apply(context.Background(), result.Config, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply with secret tiers failed: %v", err)
	}

	// REQUIRED_KEY must appear in the env file (it was resolved by the overlay vault).
	envPath := filepath.Join(instanceRoot, "all", "repo1", ".local.env")
	assertFileContains(t, envPath, "REQUIRED_KEY=")

	// Now verify a missing required key fails the apply.
	// Use a new workspace with REQUIRED_KEY required but no overlay vault resolution.
	configTOMLMissing := `
[workspace]
name = "test-ws2"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"

[env.secrets.required]
MUST_HAVE = "This key is never provided"
`
	niwaDir2, instanceRoot2 := setupTestWorkspace(t, configTOMLMissing, nil, []struct{ group, name string }{
		{"all", "repo1"},
	})
	result2, err := config.Load(filepath.Join(niwaDir2, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config2: %v", err)
	}
	// Seed a minimal state so Apply can load it (no overlay set).
	if err := SaveState(instanceRoot2, &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws2",
		InstanceNumber: 1,
		Root:           instanceRoot2,
	}); err != nil {
		t.Fatal(err)
	}
	// No overlay — MUST_HAVE is never resolved.
	applier2 := NewApplier(mockClient)
	applier2.vaultRegistry = newFakeVaultRegistry(t)
	applyErr := applier2.Apply(context.Background(), result2.Config, niwaDir2, instanceRoot2)
	if applyErr == nil {
		t.Fatal("expected error for missing required secret, got nil")
	}
	if !strings.Contains(applyErr.Error(), "MUST_HAVE") {
		t.Errorf("error should name the missing key, got: %v", applyErr)
	}
}

// TestApplyMissingMarketplaceRepo verifies that when the base config declares a
// marketplace source referencing a repo that is not managed by the workspace,
// apply fails with a clear error identifying the repo name and the problem.
func TestApplyMissingMarketplaceRepo(t *testing.T) {
	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"

[claude]
marketplaces = ["repo:tools/.claude-plugin/marketplace.json"]
`
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"all", "repo1"},
	})

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
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

	if err := SaveState(instanceRoot, &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
	}); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(mockClient)
	applyErr := applier.Apply(context.Background(), result.Config, niwaDir, instanceRoot)
	if applyErr == nil {
		t.Fatal("expected error for marketplace referencing unmanaged repo, got nil")
	}
	if !strings.Contains(applyErr.Error(), "tools") {
		t.Errorf("error should identify repo name %q, got: %v", "tools", applyErr)
	}
	if !strings.Contains(applyErr.Error(), "not managed") {
		t.Errorf("error should say 'not managed by this workspace', got: %v", applyErr)
	}
}

// TestApplyOverlayMarketplacesAppend verifies R25: when workspace-overlay.toml
// declares [claude.marketplaces], overlay entries are appended to the base
// config's marketplace list. The merged list appears in the instance root
// settings.json. Marketplace sources that reference overlay-managed repos (via
// repo: prefix) resolve correctly when the overlay is accessible.
func TestApplyOverlayMarketplacesAppend(t *testing.T) {
	overlayTOML := `
[[sources]]
org = "toolsorg"
repos = ["tools"]

[groups.private]
repos = ["tools"]

[claude]
marketplaces = ["repo:tools/.claude-plugin/marketplace.json"]
`
	overlayDir := setupOverlayDir(t, overlayTOML)

	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.public]
visibility = "public"

[claude]
marketplaces = ["tsukumogami/shirabe"]
`
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"public", "repo1"},
		{"private", "tools"},
	})

	// Pre-create the marketplace file inside the tools repo directory so
	// ResolveMarketplaceSource finds it when building the settings doc.
	toolsDir := filepath.Join(instanceRoot, "private", "tools")
	marketplaceFile := filepath.Join(toolsDir, ".claude-plugin", "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(marketplaceFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marketplaceFile, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"},
			},
			"toolsorg": {
				{Name: "tools", Visibility: "private", SSHURL: "git@github.com:toolsorg/tools.git"},
			},
		},
	}

	if err := SaveState(instanceRoot, &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
		OverlayURL:     "testorg/test-ws-overlay",
		OverlayCommit:  "abc123",
	}); err != nil {
		t.Fatal(err)
	}

	cloneFn := func(url, dir string) (bool, error) {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			return false, err
		}
		data, err := os.ReadFile(filepath.Join(overlayDir, "workspace-overlay.toml"))
		if err != nil {
			return false, err
		}
		return false, os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), data, 0o644)
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.cloneOrSync = cloneFn
	applier.headSHA = func(string) (string, error) { return "abc123", nil }

	if err := applier.Apply(context.Background(), result.Config, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply with overlay marketplaces failed: %v", err)
	}

	// The instance root settings.json should contain both marketplace entries:
	// the base-config "tsukumogami/shirabe" and the overlay "tools" (from repo:).
	settingsPath := filepath.Join(instanceRoot, ".claude", "settings.json")
	assertFileContains(t, settingsPath, "shirabe")
	assertFileContains(t, settingsPath, "tools")

	// Apply without overlay (tools repo not in workspace) should fail because
	// a repo: marketplace ref for an unmanaged repo is a hard error. This verifies
	// that moving repo: refs to the overlay is the correct design.
	configTOMLBroken := `
[workspace]
name = "test-ws-broken"

[[sources]]
org = "testorg"

[groups.public]
visibility = "public"

[claude]
marketplaces = ["repo:tools/.claude-plugin/marketplace.json"]
`
	niwaDir2, instanceRoot2 := setupTestWorkspace(t, configTOMLBroken, nil, []struct{ group, name string }{
		{"public", "repo1"},
	})
	result2, err := config.Load(filepath.Join(niwaDir2, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading broken config: %v", err)
	}
	if err := SaveState(instanceRoot2, &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws-broken",
		InstanceNumber: 1,
		Root:           instanceRoot2,
	}); err != nil {
		t.Fatal(err)
	}
	applier2 := NewApplier(mockClient)
	if applyErr := applier2.Apply(context.Background(), result2.Config, niwaDir2, instanceRoot2); applyErr == nil {
		t.Error("expected error when repo: marketplace refs an unmanaged repo without overlay, got nil")
	}
}

// TestApplyOverlayGroupContentIsolation verifies that group content declared in
// the overlay is absent when no overlay is active, and present when it is.
//
// This covers the private.md pattern: the base config has a public group with
// public content; the overlay introduces a private group and its content file.
// Without the overlay, no private content is written. With the overlay, the
// private group CLAUDE.md is installed from the overlay's content directory.
func TestApplyOverlayGroupContentIsolation(t *testing.T) {
	const privateContent = "internal-only guidance"

	overlayTOML := `
[[sources]]
org = "privateorg"
repos = ["vision"]

[groups.private]
repos = ["vision"]

[claude.content.groups.private]
source = "claude/private.md"
`
	overlayDir := setupOverlayDir(t, overlayTOML)
	// Write the overlay's private.md content file.
	if err := os.MkdirAll(filepath.Join(overlayDir, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlayDir, "claude", "private.md"), []byte(privateContent), 0o644); err != nil {
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

[claude.content.groups.public]
source = "claude/public.md"
`
	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg":    {{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"}},
			"privateorg": {{Name: "vision", Visibility: "private", SSHURL: "git@github.com:privateorg/vision.git"}},
		},
	}

	// cloneFn simulates a git clone by copying the full overlay directory tree to dir.
	cloneFn := func(url, dir string) (bool, error) {
		return false, copyDirTree(overlayDir, dir)
	}

	// ---- Sub-test: no overlay ----
	// The private group CLAUDE.md must not be written.
	t.Run("no overlay", func(t *testing.T) {
		niwaDir, instanceRoot := setupTestWorkspace(t, configTOML,
			map[string]string{"claude/public.md": "public group context"},
			[]struct{ group, name string }{{"public", "repo1"}},
		)
		result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
		if err != nil {
			t.Fatalf("loading config: %v", err)
		}
		if err := SaveState(instanceRoot, &InstanceState{
			SchemaVersion:  SchemaVersion,
			InstanceName:   "test-ws",
			InstanceNumber: 1,
			Root:           instanceRoot,
		}); err != nil {
			t.Fatal(err)
		}

		applier := NewApplier(mockClient)
		if err := applier.Apply(context.Background(), result.Config, niwaDir, instanceRoot); err != nil {
			t.Fatalf("apply (no overlay) failed: %v", err)
		}

		// Private group CLAUDE.md must not exist anywhere under instanceRoot.
		privateCLAUDE := filepath.Join(instanceRoot, "private", "CLAUDE.md")
		if _, err := os.Stat(privateCLAUDE); err == nil {
			t.Errorf("private/CLAUDE.md should not exist without overlay, but it does")
		}

		// Public group CLAUDE.md must still be written.
		publicCLAUDE := filepath.Join(instanceRoot, "public", "CLAUDE.md")
		assertFileContains(t, publicCLAUDE, "public group context")
	})

	// ---- Sub-test: with overlay ----
	// The private group CLAUDE.md must be written from overlay content.
	t.Run("with overlay", func(t *testing.T) {
		niwaDir, instanceRoot := setupTestWorkspace(t, configTOML,
			map[string]string{"claude/public.md": "public group context"},
			[]struct{ group, name string }{
				{"public", "repo1"},
				{"private", "vision"},
			},
		)
		result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
		if err != nil {
			t.Fatalf("loading config: %v", err)
		}
		if err := SaveState(instanceRoot, &InstanceState{
			SchemaVersion:  SchemaVersion,
			InstanceName:   "test-ws",
			InstanceNumber: 1,
			Root:           instanceRoot,
			OverlayURL:     "testorg/test-ws-overlay",
			OverlayCommit:  "abc123",
		}); err != nil {
			t.Fatal(err)
		}

		applier := NewApplier(mockClient)
		applier.Cloner = &Cloner{}
		applier.cloneOrSync = cloneFn
		applier.headSHA = func(string) (string, error) { return "abc123", nil }

		if err := applier.Apply(context.Background(), result.Config, niwaDir, instanceRoot); err != nil {
			t.Fatalf("apply (with overlay) failed: %v", err)
		}

		// Private group CLAUDE.md must be written with overlay content.
		privateCLAUDE := filepath.Join(instanceRoot, "private", "CLAUDE.md")
		assertFileContains(t, privateCLAUDE, privateContent)

		// Public group CLAUDE.md must still be written.
		publicCLAUDE := filepath.Join(instanceRoot, "public", "CLAUDE.md")
		assertFileContains(t, publicCLAUDE, "public group context")
	})
}

// TestApplyOverlayPluginsIsolation verifies that overlay plugins are absent from
// settings.json when no overlay is active, and present when the overlay is active.
//
// This is the critical isolation test: a plugin that requires a private marketplace
// (like tsukumogami@tsukumogami which needs repo:tools/...) must not appear in the
// output when the overlay is not accessible.
func TestApplyOverlayPluginsIsolation(t *testing.T) {
	overlayTOML := `
[claude]
plugins = ["tsukumogami@tsukumogami"]
`
	overlayDir := setupOverlayDir(t, overlayTOML)

	configTOML := `
[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"

[claude]
plugins = ["shirabe@shirabe"]
`

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"},
			},
		},
	}

	// ---- Sub-test: no overlay ----
	// Settings.json must contain only the base plugin, not the overlay plugin.
	t.Run("no overlay", func(t *testing.T) {
		niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
			{"all", "repo1"},
		})
		result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
		if err != nil {
			t.Fatalf("loading config: %v", err)
		}
		if err := SaveState(instanceRoot, &InstanceState{
			SchemaVersion:  SchemaVersion,
			InstanceName:   "test-ws",
			InstanceNumber: 1,
			Root:           instanceRoot,
		}); err != nil {
			t.Fatal(err)
		}

		applier := NewApplier(mockClient)
		if err := applier.Apply(context.Background(), result.Config, niwaDir, instanceRoot); err != nil {
			t.Fatalf("apply (no overlay) failed: %v", err)
		}

		settingsPath := filepath.Join(instanceRoot, ".claude", "settings.json")
		assertFileContains(t, settingsPath, "shirabe@shirabe")
		assertFileNotContains(t, settingsPath, "tsukumogami@tsukumogami")
	})

	// ---- Sub-test: with overlay ----
	// Settings.json must contain both base and overlay plugins.
	t.Run("with overlay", func(t *testing.T) {
		niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
			{"all", "repo1"},
		})
		result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
		if err != nil {
			t.Fatalf("loading config: %v", err)
		}
		if err := SaveState(instanceRoot, &InstanceState{
			SchemaVersion:  SchemaVersion,
			InstanceName:   "test-ws",
			InstanceNumber: 1,
			Root:           instanceRoot,
			OverlayURL:     "testorg/test-ws-overlay",
			OverlayCommit:  "abc123",
		}); err != nil {
			t.Fatal(err)
		}

		cloneFn := func(url, dir string) (bool, error) {
			if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
				return false, err
			}
			data, err := os.ReadFile(filepath.Join(overlayDir, "workspace-overlay.toml"))
			if err != nil {
				return false, err
			}
			return false, os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), data, 0o644)
		}

		applier := NewApplier(mockClient)
		applier.Cloner = &Cloner{}
		applier.cloneOrSync = cloneFn
		applier.headSHA = func(string) (string, error) { return "abc123", nil }

		if err := applier.Apply(context.Background(), result.Config, niwaDir, instanceRoot); err != nil {
			t.Fatalf("apply (with overlay) failed: %v", err)
		}

		settingsPath := filepath.Join(instanceRoot, ".claude", "settings.json")
		assertFileContains(t, settingsPath, "shirabe@shirabe")
		assertFileContains(t, settingsPath, "tsukumogami@tsukumogami")
	})
}

// copyDirTree recursively copies all files from src to dst. Only regular files
// and directories are copied; symlinks are not followed. Used in tests to fully
// replicate an overlay directory tree into a cloneOrSync destination.
func copyDirTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
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

// TestApplyWritesInstanceGitignore ensures Applier.Apply runs the
// EnsureInstanceGitignore guard (previously only Applier.Create did).
// This covers the upgrade path where an instance was created before
// the guard was introduced and never had its .gitignore written.
func TestApplyWritesInstanceGitignore(t *testing.T) {
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
	repoDir := filepath.Join(instanceRoot, "all", "repo1")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Seed state via Create so Apply has something to load.
	if _, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Simulate the upgrade path: an instance created before the
	// .gitignore guard existed. Remove the file that Create wrote so
	// we can assert Apply puts it back.
	gitignorePath := filepath.Join(instanceRoot, ".gitignore")
	if err := os.Remove(gitignorePath); err != nil {
		t.Fatalf("removing pre-existing .gitignore: %v", err)
	}
	if _, err := os.Stat(gitignorePath); !os.IsNotExist(err) {
		t.Fatalf("expected .gitignore to be absent before Apply, stat err = %v", err)
	}

	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("reading .gitignore after apply: %v", err)
	}
	if !strings.Contains(string(data), "*.local*") {
		t.Errorf("Apply did not write *.local* to .gitignore, got:\n%s", string(data))
	}
}
