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
	files, err := InstallWorkspaceContent(cfg, configDir, instanceRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 written file, got %d", len(files))
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

func TestInstallWorkspaceContentNoSource(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Content:   config.ContentConfig{},
	}

	// Should be a no-op, not an error.
	files, err := InstallWorkspaceContent(cfg, "/tmp", "/tmp/instance")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected no files, got %d", len(files))
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

func TestExpandVarsAllVariables(t *testing.T) {
	input := "ws={workspace} name={workspace_name} repo={repo_name} group={group_name}"
	vars := map[string]string{
		"{workspace}":      "/abs/path",
		"{workspace_name}": "myws",
		"{repo_name}":      "myrepo",
		"{group_name}":     "mygroup",
	}

	got := expandVars(input, vars)
	want := "ws=/abs/path name=myws repo=myrepo group=mygroup"
	if got != want {
		t.Errorf("expandVars = %q, want %q", got, want)
	}
}

func TestInstallGroupContent(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	contentDir := filepath.Join(configDir, "claude")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	source := "# {group_name} Group\n\nWorkspace: {workspace_name}\n"
	if err := os.WriteFile(filepath.Join(contentDir, "public.md"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:       "myws",
			ContentDir: "claude",
		},
		Content: config.ContentConfig{
			Groups: map[string]config.ContentEntry{
				"public": {Source: "public.md"},
			},
		},
	}

	instanceRoot := filepath.Join(tmpDir, "instance")
	groupDir := filepath.Join(instanceRoot, "public")
	if err := os.MkdirAll(groupDir, 0o755); err != nil {
		t.Fatal(err)
	}

	files, err := InstallGroupContent(cfg, configDir, instanceRoot, "public")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 written file, got %d", len(files))
	}

	// Group directory is non-git, so it gets CLAUDE.md (not .local).
	data, err := os.ReadFile(filepath.Join(groupDir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "# public Group") {
		t.Errorf("missing group_name expansion: %s", content)
	}
	if !strings.Contains(content, "Workspace: myws") {
		t.Errorf("missing workspace_name expansion: %s", content)
	}
	if strings.Contains(content, "{group_name}") {
		t.Errorf("unexpanded variable: %s", content)
	}
}

func TestInstallGroupContentNoEntry(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Content:   config.ContentConfig{},
	}

	// No group content entry -- should be a no-op.
	files, err := InstallGroupContent(cfg, "/tmp", "/tmp/instance", "public")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected no files, got %d", len(files))
	}
}

func TestInstallRepoContent(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	contentDir := filepath.Join(configDir, "claude")
	reposDir := filepath.Join(contentDir, "repos")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatal(err)
	}

	source := "# {repo_name}\n\nGroup: {group_name}\nWorkspace: {workspace_name}\nPath: {workspace}\n"
	if err := os.WriteFile(filepath.Join(reposDir, "myapp.md"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:       "myws",
			ContentDir: "claude",
		},
		Content: config.ContentConfig{
			Repos: map[string]config.RepoContentEntry{
				"myapp": {Source: "repos/myapp.md"},
			},
		},
	}

	instanceRoot := filepath.Join(tmpDir, "instance")
	repoDir := filepath.Join(instanceRoot, "public", "myapp")
	// Create .git dir to simulate a cloned repo with gitignore.
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Write gitignore with *.local* pattern.
	if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := InstallRepoContent(cfg, configDir, instanceRoot, "public", "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", result.Warnings)
	}
	if len(result.WrittenFiles) != 1 {
		t.Fatalf("expected 1 written file, got %d", len(result.WrittenFiles))
	}

	// Repo directory is a git directory, so it gets CLAUDE.local.md.
	data, err := os.ReadFile(filepath.Join(repoDir, "CLAUDE.local.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.local.md: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "# myapp") {
		t.Errorf("missing repo_name expansion: %s", content)
	}
	if !strings.Contains(content, "Group: public") {
		t.Errorf("missing group_name expansion: %s", content)
	}
	if !strings.Contains(content, "Workspace: myws") {
		t.Errorf("missing workspace_name expansion: %s", content)
	}
	if strings.Contains(content, "{repo_name}") {
		t.Errorf("unexpanded variable: %s", content)
	}
}

func TestInstallRepoContentSubdirs(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	contentDir := filepath.Join(configDir, "claude")
	reposDir := filepath.Join(contentDir, "repos")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatal(err)
	}

	repoSource := "# {repo_name} repo\n"
	subdirSource := "# {repo_name} website subdir\n"
	if err := os.WriteFile(filepath.Join(reposDir, "tsuku.md"), []byte(repoSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reposDir, "tsuku-website.md"), []byte(subdirSource), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:       "myws",
			ContentDir: "claude",
		},
		Content: config.ContentConfig{
			Repos: map[string]config.RepoContentEntry{
				"tsuku": {
					Source: "repos/tsuku.md",
					Subdirs: map[string]string{
						"website": "repos/tsuku-website.md",
					},
				},
			},
		},
	}

	instanceRoot := filepath.Join(tmpDir, "instance")
	repoDir := filepath.Join(instanceRoot, "public", "tsuku")
	websiteDir := filepath.Join(repoDir, "website")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(websiteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := InstallRepoContent(cfg, configDir, instanceRoot, "public", "tsuku")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", result.Warnings)
	}
	if len(result.WrittenFiles) != 2 {
		t.Fatalf("expected 2 written files, got %d", len(result.WrittenFiles))
	}

	// Verify repo-level CLAUDE.local.md.
	data, err := os.ReadFile(filepath.Join(repoDir, "CLAUDE.local.md"))
	if err != nil {
		t.Fatalf("reading repo CLAUDE.local.md: %v", err)
	}
	if !strings.Contains(string(data), "# tsuku repo") {
		t.Errorf("unexpected repo content: %s", data)
	}

	// Verify subdir-level CLAUDE.local.md.
	data, err = os.ReadFile(filepath.Join(websiteDir, "CLAUDE.local.md"))
	if err != nil {
		t.Fatalf("reading subdir CLAUDE.local.md: %v", err)
	}
	if !strings.Contains(string(data), "# tsuku website subdir") {
		t.Errorf("unexpected subdir content: %s", data)
	}
}

func TestInstallRepoContentAutoDiscovery(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	contentDir := filepath.Join(configDir, "claude")
	reposDir := filepath.Join(contentDir, "repos")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// No explicit content entry for "myapp", but file exists at convention path.
	source := "# Auto-discovered {repo_name}\n"
	if err := os.WriteFile(filepath.Join(reposDir, "myapp.md"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:       "myws",
			ContentDir: "claude",
		},
		Content: config.ContentConfig{
			// No explicit repos entries.
		},
	}

	instanceRoot := filepath.Join(tmpDir, "instance")
	repoDir := filepath.Join(instanceRoot, "public", "myapp")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := InstallRepoContent(cfg, configDir, instanceRoot, "public", "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", result.Warnings)
	}

	data, err := os.ReadFile(filepath.Join(repoDir, "CLAUDE.local.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.local.md: %v", err)
	}
	if !strings.Contains(string(data), "# Auto-discovered myapp") {
		t.Errorf("unexpected content: %s", data)
	}
}

func TestInstallRepoContentAutoDiscoveryNoFile(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	contentDir := filepath.Join(configDir, "claude")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:       "myws",
			ContentDir: "claude",
		},
		Content: config.ContentConfig{},
	}

	instanceRoot := filepath.Join(tmpDir, "instance")
	repoDir := filepath.Join(instanceRoot, "public", "myapp")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// No auto-discovery file, no explicit entry -- should be a no-op.
	result, err := InstallRepoContent(cfg, configDir, instanceRoot, "public", "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", result.Warnings)
	}

	// CLAUDE.local.md should NOT exist.
	if _, err := os.Stat(filepath.Join(repoDir, "CLAUDE.local.md")); err == nil {
		t.Error("CLAUDE.local.md should not exist when no source is available")
	}
}

func TestInstallRepoContentAutoDiscoveryNoContentDir(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name: "myws",
			// No ContentDir set.
		},
		Content: config.ContentConfig{},
	}

	tmpDir := t.TempDir()
	instanceRoot := filepath.Join(tmpDir, "instance")
	repoDir := filepath.Join(instanceRoot, "public", "myapp")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Without content_dir, auto-discovery should not attempt anything.
	result, err := InstallRepoContent(cfg, tmpDir, instanceRoot, "public", "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", result.Warnings)
	}
}

func TestCheckGitignoreMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	warnings := CheckGitignore(tmpDir, "testrepo")
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if !strings.Contains(warnings[0].Message, ".gitignore missing") {
		t.Errorf("unexpected warning message: %s", warnings[0].Message)
	}
}

func TestCheckGitignoreMissingPattern(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	warnings := CheckGitignore(tmpDir, "testrepo")
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if !strings.Contains(warnings[0].Message, "*.local*") {
		t.Errorf("unexpected warning message: %s", warnings[0].Message)
	}
}

func TestCheckGitignoreHasPattern(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("*.log\n*.local*\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	warnings := CheckGitignore(tmpDir, "testrepo")
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestCheckGitignoreWarningOnWrite(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	contentDir := filepath.Join(configDir, "claude")
	reposDir := filepath.Join(contentDir, "repos")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatal(err)
	}

	source := "# {repo_name}\n"
	if err := os.WriteFile(filepath.Join(reposDir, "myapp.md"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:       "myws",
			ContentDir: "claude",
		},
		Content: config.ContentConfig{
			Repos: map[string]config.RepoContentEntry{
				"myapp": {Source: "repos/myapp.md"},
			},
		},
	}

	instanceRoot := filepath.Join(tmpDir, "instance")
	repoDir := filepath.Join(instanceRoot, "public", "myapp")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// .gitignore exists but lacks *.local*.
	if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := InstallRepoContent(cfg, configDir, instanceRoot, "public", "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(result.Warnings))
	}
	if !strings.Contains(result.Warnings[0].Message, "*.local*") {
		t.Errorf("unexpected warning message: %s", result.Warnings[0].Message)
	}

	// File should still be written despite the warning.
	if _, err := os.Stat(filepath.Join(repoDir, "CLAUDE.local.md")); err != nil {
		t.Error("CLAUDE.local.md should be written even when gitignore warning is raised")
	}
}

func TestHasLocalPattern(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"exact match", "*.local*\n", true},
		{"among other lines", "*.log\n*.local*\nbuild/\n", true},
		{"with leading whitespace", "  *.local*  \n", true},
		{"no match", "*.log\nbuild/\n", false},
		{"empty file", "", false},
		{"partial match", "*.local\n", false},
		{"substring", "foo*.local*bar\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasLocalPattern(strings.NewReader(tt.content))
			if got != tt.want {
				t.Errorf("hasLocalPattern(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}
