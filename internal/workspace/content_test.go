package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
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
	files, err := InstallWorkspaceContent(cfg, configDir, instanceRoot, agent.AgentClaude)
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

// setupWorkspaceContentFixture builds a minimal workspace config with a
// workspace, a group, and a repo content source, plus the on-disk content
// files. It returns the config, the config dir, and the instance root — the
// inputs the three installers share — so the agent-parameterized tests below can
// exercise the same fixture under both agents.
func setupWorkspaceContentFixture(t *testing.T) (cfg *config.WorkspaceConfig, configDir, instanceRoot string) {
	t.Helper()
	tmpDir := t.TempDir()
	configDir = filepath.Join(tmpDir, "config")
	contentDir := filepath.Join(configDir, "claude")
	if err := os.MkdirAll(filepath.Join(contentDir, "repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(contentDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ws.md", "# {workspace_name}\n\nWorkspace body\n")
	write("grp.md", "# group {group_name}\n\nGroup body\n")
	write(filepath.Join("repos", "myapp.md"), "# repo {repo_name}\n\nRepo body\n")

	cfg = &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "myws", ContentDir: "claude"},
		Content: config.ContentConfig{
			Workspace: config.ContentEntry{Source: "ws.md"},
			Groups:    map[string]config.ContentEntry{"public": {Source: "grp.md"}},
			Repos:     map[string]config.RepoContentEntry{"myapp": {Source: "repos/myapp.md"}},
		},
	}
	instanceRoot = filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(filepath.Join(instanceRoot, "public", "myapp"), 0o755); err != nil {
		t.Fatal(err)
	}
	return cfg, configDir, instanceRoot
}

// TestNiwaOwnedContentGoesToTheDeclaredAgentsOnly asserts what the instance
// root and the group directories receive. Claude gets a document at each; Codex
// gets neither, and for two different reasons the declaration table already
// records: an instance root is not a project root, so a session started there
// reads nothing at all, and a group directory is above the repository where the
// walk stops, so its layer travels composed into each repository's own document
// instead.
func TestNiwaOwnedContentGoesToTheDeclaredAgentsOnly(t *testing.T) {
	cfg, configDir, instanceRoot := setupWorkspaceContentFixture(t)

	wsFiles, err := InstallWorkspaceContent(cfg, configDir, instanceRoot, agent.AgentClaude)
	if err != nil {
		t.Fatalf("InstallWorkspaceContent: %v", err)
	}
	if len(wsFiles) != 1 || filepath.Base(wsFiles[0]) != "CLAUDE.md" {
		t.Fatalf("claude workspace files = %v, want one CLAUDE.md", wsFiles)
	}
	grpFiles, err := InstallGroupContent(cfg, configDir, instanceRoot, "public", agent.AgentClaude)
	if err != nil {
		t.Fatalf("InstallGroupContent: %v", err)
	}
	if len(grpFiles) != 1 || filepath.Base(grpFiles[0]) != "CLAUDE.md" {
		t.Fatalf("claude group files = %v, want one CLAUDE.md", grpFiles)
	}

	codexWS, err := InstallWorkspaceContent(cfg, configDir, instanceRoot, agent.AgentCodex)
	if err != nil {
		t.Fatalf("InstallWorkspaceContent: %v", err)
	}
	codexGrp, err := InstallGroupContent(cfg, configDir, instanceRoot, "public", agent.AgentCodex)
	if err != nil {
		t.Fatalf("InstallGroupContent: %v", err)
	}
	if len(codexWS) != 0 || len(codexGrp) != 0 {
		t.Fatalf("codex niwa-owned files = %v / %v, want none at either level", codexWS, codexGrp)
	}
	assertNotExist(t, filepath.Join(instanceRoot, "AGENTS.md"))
	assertNotExist(t, filepath.Join(instanceRoot, "public", "AGENTS.md"))
}

// TestRepoContentComposesTheChainUnderCodex is the repository level: Claude gets
// its own document as before, and Codex gets one composed at the name that wins
// its first-match precedence, carrying the outer layers the same session would
// otherwise never see.
func TestRepoContentComposesTheChainUnderCodex(t *testing.T) {
	t.Run("claude writes CLAUDE.local.md", func(t *testing.T) {
		cfg, configDir, instanceRoot := setupWorkspaceContentFixture(t)
		result, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "myapp", agent.AgentClaude)
		if err != nil {
			t.Fatalf("InstallRepoContent: %v", err)
		}
		if len(result.WrittenFiles) != 1 || filepath.Base(result.WrittenFiles[0]) != "CLAUDE.local.md" {
			t.Fatalf("claude repo files = %v, want one CLAUDE.local.md", result.WrittenFiles)
		}
		if body := readFile(t, result.WrittenFiles[0]); strings.Contains(body, "Workspace") {
			t.Errorf("claude repo document folded in the workspace layer; it reads that document where it is written:\n%s", body)
		}
	})
	t.Run("codex composes the chain", func(t *testing.T) {
		cfg, configDir, instanceRoot := setupWorkspaceContentFixture(t)
		result, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "myapp", agent.AgentCodex)
		if err != nil {
			t.Fatalf("InstallRepoContent: %v", err)
		}
		if len(result.WrittenFiles) != 1 || filepath.Base(result.WrittenFiles[0]) != "AGENTS.override.md" {
			t.Fatalf("codex repo files = %v, want one AGENTS.override.md", result.WrittenFiles)
		}
		repoDir := filepath.Join(instanceRoot, "public", "myapp")
		assertNotExist(t, filepath.Join(repoDir, "CLAUDE.local.md"))
		// AGENTS.md is the repository's own slot; niwa never writes there.
		assertNotExist(t, filepath.Join(repoDir, "AGENTS.md"))

		body := readFile(t, result.WrittenFiles[0])
		if !strings.HasPrefix(body, "Generated by niwa") {
			t.Errorf("composed document does not open with the generation marker:\n%s", body)
		}
		for _, want := range []string{"Workspace", "Group", "myapp"} {
			if !strings.Contains(body, want) {
				t.Errorf("composed document is missing the %q layer:\n%s", want, body)
			}
		}
		if len(result.Excludes) != 1 || result.Excludes[0] != "AGENTS.override.md" {
			t.Errorf("composed document excludes = %v, want the repo-relative name", result.Excludes)
		}
	})
}

// TestComposedDocumentIsRefusedWhenTheNameIsOccupied is the conflict rule end to
// end: the file niwa did not write is left exactly as it was, nothing is
// written, the refusal is reported, and the path is carried out so the cleanup
// pass leaves it alone too.
func TestComposedDocumentIsRefusedWhenTheNameIsOccupied(t *testing.T) {
	cfg, configDir, instanceRoot := setupWorkspaceContentFixture(t)
	repoDir := filepath.Join(instanceRoot, "public", "myapp")
	target := filepath.Join(repoDir, "AGENTS.override.md")

	const committed = "# the repository's own override\n"
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "myapp", agent.AgentCodex)
	if err != nil {
		t.Fatalf("InstallRepoContent: %v", err)
	}
	if len(result.WrittenFiles) != 0 {
		t.Errorf("wrote %v over a file niwa did not write", result.WrittenFiles)
	}
	if got := readFile(t, target); got != committed {
		t.Errorf("the occupied file was modified: %q", got)
	}
	if len(result.Exempt) != 1 || result.Exempt[0] != target {
		t.Errorf("exempt = %v, want [%s]", result.Exempt, target)
	}
	if len(result.Warnings) != 1 {
		t.Errorf("warnings = %v, want one naming the refusal", result.Warnings)
	}
}

// TestComposedDocumentIsRefreshedOnReapply is the other half of the same test:
// niwa's own prior document is recognized by its marker and rewritten in place,
// so an instance does not stop updating its own context after the first apply.
func TestComposedDocumentIsRefreshedOnReapply(t *testing.T) {
	cfg, configDir, instanceRoot := setupWorkspaceContentFixture(t)

	first, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "myapp", agent.AgentCodex)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	if len(first.WrittenFiles) != 1 {
		t.Fatalf("first install wrote %v, want one document", first.WrittenFiles)
	}
	want := readFile(t, first.WrittenFiles[0])

	if err := os.WriteFile(first.WrittenFiles[0], []byte(want+"\nstale edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "myapp", agent.AgentCodex)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if len(second.WrittenFiles) != 1 || len(second.Warnings) != 0 {
		t.Fatalf("re-apply wrote %v with warnings %v; niwa did not recognize its own document", second.WrittenFiles, second.Warnings)
	}
	if got := readFile(t, second.WrittenFiles[0]); got != want {
		t.Errorf("re-applied document = %q, want the freshly composed %q", got, want)
	}
}

// TestComposedDocumentInlinesTheCommittedContextFile covers the other side of
// the same directory slot: the repository commits the file niwa's name
// outranks, so its content is inlined rather than lost, and the committed file
// itself is not touched.
func TestComposedDocumentInlinesTheCommittedContextFile(t *testing.T) {
	cfg, configDir, instanceRoot := setupWorkspaceContentFixture(t)
	repoDir := filepath.Join(instanceRoot, "public", "myapp")
	committedPath := filepath.Join(repoDir, "AGENTS.md")

	const committed = "# committed repository context\n"
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(committedPath, []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "myapp", agent.AgentCodex)
	if err != nil {
		t.Fatalf("InstallRepoContent: %v", err)
	}
	if len(result.WrittenFiles) != 1 {
		t.Fatalf("repo files = %v, want one composed document", result.WrittenFiles)
	}
	if !strings.Contains(readFile(t, result.WrittenFiles[0]), "committed repository context") {
		t.Error("the committed context file was not inlined; displacing it would have lost it silently")
	}
	if got := readFile(t, committedPath); got != committed {
		t.Errorf("the committed file was modified: %q", got)
	}
}

// TestComposedDocumentRefusesToReadASymlinkedContextFile is the narrow security
// rule the inline read exists for: git reproduces committed symlinks verbatim,
// so a repository could point its context file at the developer's credentials
// and have niwa copy them into every session's instruction context.
func TestComposedDocumentRefusesToReadASymlinkedContextFile(t *testing.T) {
	cfg, configDir, instanceRoot := setupWorkspaceContentFixture(t)
	repoDir := filepath.Join(instanceRoot, "public", "myapp")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	secret := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(secret, []byte("token=hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(repoDir, "AGENTS.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	result, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "myapp", agent.AgentCodex)
	if err != nil {
		t.Fatalf("InstallRepoContent: %v", err)
	}
	if len(result.WrittenFiles) != 1 {
		t.Fatalf("repo files = %v, want the workspace layers written without the inline", result.WrittenFiles)
	}
	if body := readFile(t, result.WrittenFiles[0]); strings.Contains(body, "hunter2") {
		t.Fatalf("the symlink was read through:\n%s", body)
	}
	if len(result.Warnings) != 1 {
		t.Errorf("warnings = %v, want one naming the refused inline", result.Warnings)
	}
}

// TestContentTreesCoexist asserts both agents' documents live in one instance
// without either clobbering the other: every apply produces every agent's plan,
// so the two trees are always both current.
func TestContentTreesCoexist(t *testing.T) {
	cfg, configDir, instanceRoot := setupWorkspaceContentFixture(t)
	for _, ag := range agent.All() {
		if _, err := InstallWorkspaceContent(cfg, configDir, instanceRoot, ag); err != nil {
			t.Fatalf("workspace content for %s: %v", ag, err)
		}
		if _, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "myapp", ag); err != nil {
			t.Fatalf("repo content for %s: %v", ag, err)
		}
	}
	repoDir := filepath.Join(instanceRoot, "public", "myapp")
	for _, path := range []string{
		filepath.Join(instanceRoot, "CLAUDE.md"),
		filepath.Join(repoDir, "CLAUDE.local.md"),
		filepath.Join(repoDir, "AGENTS.override.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s missing after a two-agent apply: %v", path, err)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func assertNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected %s to not exist, stat err = %v", path, err)
	}
}

func TestInstallWorkspaceContentNoSource(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Claude:    config.ClaudeConfig{Content: config.ContentConfig{}},
	}

	// Should be a no-op, not an error.
	files, err := InstallWorkspaceContent(cfg, "/tmp", "/tmp/instance", agent.AgentClaude)
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

	files, err := InstallGroupContent(cfg, configDir, instanceRoot, "public", agent.AgentClaude)
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
		Claude:    config.ClaudeConfig{Content: config.ContentConfig{}},
	}

	// No group content entry -- should be a no-op.
	files, err := InstallGroupContent(cfg, "/tmp", "/tmp/instance", "public", agent.AgentClaude)
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

	result, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "myapp", agent.AgentClaude)
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

	result, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "tsuku", agent.AgentClaude)
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

	result, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "myapp", agent.AgentClaude)
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
		Claude: config.ClaudeConfig{Content: config.ContentConfig{}},
	}

	instanceRoot := filepath.Join(tmpDir, "instance")
	repoDir := filepath.Join(instanceRoot, "public", "myapp")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// No auto-discovery file, no explicit entry -- should be a no-op.
	result, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "myapp", agent.AgentClaude)
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
		Claude: config.ClaudeConfig{Content: config.ContentConfig{}},
	}

	tmpDir := t.TempDir()
	instanceRoot := filepath.Join(tmpDir, "instance")
	repoDir := filepath.Join(instanceRoot, "public", "myapp")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Without content_dir, auto-discovery should not attempt anything.
	result, err := InstallRepoContent(cfg, tmpDir, "", instanceRoot, "public", "myapp", agent.AgentClaude)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", result.Warnings)
	}
}

func TestCheckContainmentAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	childDir := filepath.Join(tmpDir, "sub", "deep")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		target string
		parent string
	}{
		{"direct child", filepath.Join(tmpDir, "file.md"), tmpDir},
		{"nested child", filepath.Join(tmpDir, "sub", "deep", "file.md"), tmpDir},
		{"same dir", tmpDir, tmpDir},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkContainment(tt.target, tt.parent); err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

func TestCheckContainmentRejected(t *testing.T) {
	tmpDir := t.TempDir()
	parentDir := filepath.Join(tmpDir, "content")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		target string
		parent string
	}{
		{"traversal escape", filepath.Join(parentDir, "..", "secret"), parentDir},
		{"sibling directory", filepath.Join(tmpDir, "other", "file"), parentDir},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkContainment(tt.target, tt.parent)
			if err == nil {
				t.Error("expected containment error, got nil")
			}
			if !strings.Contains(err.Error(), "escapes") {
				t.Errorf("error should mention escaping, got: %v", err)
			}
		})
	}
}

func TestCheckContainmentSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	parentDir := filepath.Join(tmpDir, "content")
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a symlink inside content that points outside.
	symlinkPath := filepath.Join(parentDir, "escape-link")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Skipf("cannot create symlinks: %v", err)
	}

	target := filepath.Join(symlinkPath, "secret.md")
	err := checkContainment(target, parentDir)
	if err == nil {
		t.Error("expected containment error for symlink escape, got nil")
	}
}

func TestInstallContentFileContainment(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	contentDir := filepath.Join(configDir, "claude")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a secret file outside the content directory.
	secretPath := filepath.Join(configDir, "secret.md")
	if err := os.WriteFile(secretPath, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:       "test",
			ContentDir: "claude",
		},
		Content: config.ContentConfig{
			Workspace: config.ContentEntry{Source: "../secret.md"},
		},
	}

	instanceRoot := filepath.Join(tmpDir, "instance")
	_, err := InstallWorkspaceContent(cfg, configDir, instanceRoot, agent.AgentClaude)
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("error should mention escaping, got: %v", err)
	}
}

func TestInstallRepoContentSubdirContainment(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	contentDir := filepath.Join(configDir, "claude")
	reposDir := filepath.Join(contentDir, "repos")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatal(err)
	}

	repoSource := "# repo\n"
	subdirSource := "# subdir\n"
	if err := os.WriteFile(filepath.Join(reposDir, "myrepo.md"), []byte(repoSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reposDir, "sub.md"), []byte(subdirSource), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:       "test",
			ContentDir: "claude",
		},
		Content: config.ContentConfig{
			Repos: map[string]config.RepoContentEntry{
				"myrepo": {
					Source: "repos/myrepo.md",
					Subdirs: map[string]string{
						"../../escape": "repos/sub.md",
					},
				},
			},
		},
	}

	instanceRoot := filepath.Join(tmpDir, "instance")
	repoDir := filepath.Join(instanceRoot, "public", "myrepo")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "myrepo", agent.AgentClaude)
	if err == nil {
		t.Fatal("expected error for subdirectory escape, got nil")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("error should mention escaping, got: %v", err)
	}
}

func TestExpandVarsUsesStringReplacement(t *testing.T) {
	// Verify that expandVars uses plain string replacement (strings.NewReplacer),
	// not text/template. Template syntax should pass through unchanged.
	input := "Hello {{.Name}}, {{ range .Items }}{{ . }}{{ end }}"
	vars := map[string]string{
		"{workspace_name}": "test",
	}

	got := expandVars(input, vars)
	// If text/template were used, this would either fail to parse or expand
	// the template directives. With plain replacement, the input passes through.
	if got != input {
		t.Errorf("expandVars modified template syntax: got %q, want %q", got, input)
	}
}

// TestInstallRepoContentOverlayAppend verifies that overlay content is appended
// to CLAUDE.local.md separated by a blank line.
func TestInstallRepoContentOverlayAppend(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	contentDir := filepath.Join(configDir, "claude")
	reposDir := filepath.Join(contentDir, "repos")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatal(err)
	}

	overlayDir := filepath.Join(tmpDir, "overlay")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}

	baseContent := "# myapp base content\n"
	overlayContent := "# overlay section\n"
	if err := os.WriteFile(filepath.Join(reposDir, "myapp.md"), []byte(baseContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlayDir, "myapp-overlay.md"), []byte(overlayContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:       "myws",
			ContentDir: "claude",
		},
		Content: config.ContentConfig{
			Repos: map[string]config.RepoContentEntry{
				"myapp": {
					Source:        "repos/myapp.md",
					OverlaySource: "myapp-overlay.md",
				},
			},
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

	result, err := InstallRepoContent(cfg, configDir, overlayDir, instanceRoot, "public", "myapp", agent.AgentClaude)
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

	content := string(data)
	if !strings.Contains(content, "# myapp base content") {
		t.Errorf("missing base content: %s", content)
	}
	if !strings.Contains(content, "# overlay section") {
		t.Errorf("missing overlay content: %s", content)
	}
	// Verify blank-line separation: base ends with \n, then \n separator, then overlay.
	if !strings.Contains(content, "# myapp base content\n\n# overlay section") {
		t.Errorf("content not blank-line separated: %q", content)
	}
}

// TestInstallRepoContentOverlayNoRegression verifies that when no OverlaySource
// is set, CLAUDE.local.md is written normally without modification.
func TestInstallRepoContentOverlayNoRegression(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	contentDir := filepath.Join(configDir, "claude")
	reposDir := filepath.Join(contentDir, "repos")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatal(err)
	}

	baseContent := "# myapp no overlay\n"
	if err := os.WriteFile(filepath.Join(reposDir, "myapp.md"), []byte(baseContent), 0o644); err != nil {
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
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.local*\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pass a non-empty overlayDir — it should be ignored when OverlaySource is empty.
	result, err := InstallRepoContent(cfg, configDir, "/any/overlay/dir", instanceRoot, "public", "myapp", agent.AgentClaude)
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
	if string(data) != baseContent {
		t.Errorf("content modified unexpectedly: got %q, want %q", string(data), baseContent)
	}
}

// TestInstallRepoContentOverlaySourceEmptyOverlayDir verifies that
// OverlaySource set with an empty overlayDir returns an error.
func TestInstallRepoContentOverlaySourceEmptyOverlayDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	contentDir := filepath.Join(configDir, "claude")
	reposDir := filepath.Join(contentDir, "repos")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reposDir, "myapp.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:       "myws",
			ContentDir: "claude",
		},
		Content: config.ContentConfig{
			Repos: map[string]config.RepoContentEntry{
				"myapp": {
					Source:        "repos/myapp.md",
					OverlaySource: "myapp-overlay.md",
				},
			},
		},
	}

	instanceRoot := filepath.Join(tmpDir, "instance")
	repoDir := filepath.Join(instanceRoot, "public", "myapp")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "myapp", agent.AgentClaude)
	if err == nil {
		t.Fatal("expected error when OverlaySource is set but overlayDir is empty")
	}
	if !strings.Contains(err.Error(), "overlayDir is empty") {
		t.Errorf("error should mention overlayDir: %v", err)
	}
}

// TestInstallRepoContentOverlayOnlyNoBase verifies that when OverlaySource is
// set but Source is empty, the overlay content is written as CLAUDE.local.md.
func TestInstallRepoContentOverlayOnlyNoBase(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	overlayDir := filepath.Join(tmpDir, "overlay")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}

	overlayContent := "# overlay-only content\n"
	if err := os.WriteFile(filepath.Join(overlayDir, "myapp-overlay.md"), []byte(overlayContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:       "myws",
			ContentDir: "claude",
		},
		Content: config.ContentConfig{
			Repos: map[string]config.RepoContentEntry{
				"myapp": {
					// Source is intentionally empty; OverlaySource only.
					OverlaySource: "myapp-overlay.md",
				},
			},
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

	result, err := InstallRepoContent(cfg, configDir, overlayDir, instanceRoot, "public", "myapp", agent.AgentClaude)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", result.Warnings)
	}
	if len(result.WrittenFiles) != 1 {
		t.Fatalf("expected 1 written file, got %d", len(result.WrittenFiles))
	}

	data, err := os.ReadFile(filepath.Join(repoDir, "CLAUDE.local.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.local.md: %v", err)
	}
	if string(data) != overlayContent {
		t.Errorf("CLAUDE.local.md = %q, want %q", string(data), overlayContent)
	}
}

// TestInstallRepoContentOverlayOnlyNoBaseEmptyDir verifies that OverlaySource
// set without a base Source returns an error when overlayDir is empty.
func TestInstallRepoContentOverlayOnlyNoBaseEmptyDir(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "myws", ContentDir: "claude"},
		Content: config.ContentConfig{
			Repos: map[string]config.RepoContentEntry{
				"myapp": {OverlaySource: "myapp-overlay.md"},
			},
		},
	}

	tmpDir := t.TempDir()
	_, err := InstallRepoContent(cfg, tmpDir, "", filepath.Join(tmpDir, "instance"), "public", "myapp", agent.AgentClaude)
	if err == nil {
		t.Fatal("expected error when OverlaySource is set but overlayDir is empty")
	}
	if !strings.Contains(err.Error(), "overlayDir is empty") {
		t.Errorf("error should mention overlayDir: %v", err)
	}
}

// gitignore-pattern warnings were removed: niwa now self-guarantees
// invisibility via .git/info/exclude (see EnsureRepoExclude), so it no longer
// inspects or warns about the repo's committed .gitignore.
