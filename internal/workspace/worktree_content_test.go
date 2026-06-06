package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// applyToWorktreeFixture builds a config dir with a repo content source, an
// instance root carrying a workspace-context.md, and an empty worktree dir.
// Returns (cfg, configDir, instanceRoot, worktreePath).
func applyToWorktreeFixture(t *testing.T) (*config.WorkspaceConfig, string, string, string) {
	t.Helper()
	tmpDir := t.TempDir()

	configDir := filepath.Join(tmpDir, "config")
	reposDir := filepath.Join(configDir, "claude", "repos")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := "# {repo_name}\n\nThis is the app repo content layer for group {group_name}.\n"
	if err := os.WriteFile(filepath.Join(reposDir, "app.md"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "myws", ContentDir: "claude"},
		Claude: config.ClaudeConfig{
			Content: config.ContentConfig{
				Repos: map[string]config.RepoContentEntry{
					"app": {Source: "repos/app.md"},
				},
			},
		},
	}

	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instanceRoot, workspaceContextFile), []byte("# workspace context\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	worktreePath := filepath.Join(instanceRoot, ".niwa", "worktrees", "app-abc123")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatal(err)
	}

	return cfg, configDir, instanceRoot, worktreePath
}

func TestApplyToWorktreeInstallsContentRulesAndLayer(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	written, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{})
	if err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("expected written files, got none")
	}

	// 1. Repo content: CLAUDE.local.md carries the repo content layer.
	localPath := filepath.Join(worktreePath, "CLAUDE.local.md")
	local, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("reading worktree CLAUDE.local.md: %v", err)
	}
	if !strings.Contains(string(local), "app repo content layer") {
		t.Errorf("repo content missing from worktree CLAUDE.local.md:\n%s", local)
	}

	// 2. Worktree rules import: absolute @import to the instance workspace-context.md.
	rules, err := os.ReadFile(filepath.Join(worktreePath, worktreeRulesFile))
	if err != nil {
		t.Fatalf("reading worktree rules import: %v", err)
	}
	absInstance, _ := filepath.Abs(instanceRoot)
	wantImport := "@" + filepath.Join(absInstance, workspaceContextFile)
	if !strings.Contains(string(rules), wantImport) {
		t.Errorf("rules import missing absolute workspace-context import %q:\n%s", wantImport, rules)
	}

	// 3. Purpose/branch layer appended to CLAUDE.local.md.
	if !strings.Contains(string(local), worktreeContextHeading) {
		t.Errorf("worktree context heading missing:\n%s", local)
	}
	if !strings.Contains(string(local), "ship-the-thing") {
		t.Errorf("purpose missing from worktree layer:\n%s", local)
	}
	if !strings.Contains(string(local), "branch-xyz") {
		t.Errorf("branch missing from worktree layer:\n%s", local)
	}
}

func TestApplyToWorktreeIsIdempotent(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("first ApplyToWorktree: %v", err)
	}
	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("second ApplyToWorktree: %v", err)
	}

	// The worktree-context section must appear exactly once after re-apply
	// (replaced, not duplicated).
	local, err := os.ReadFile(filepath.Join(worktreePath, "CLAUDE.local.md"))
	if err != nil {
		t.Fatalf("reading worktree CLAUDE.local.md: %v", err)
	}
	if n := strings.Count(string(local), worktreeContextHeading); n != 1 {
		t.Errorf("expected worktree context heading exactly once after re-apply, got %d:\n%s", n, local)
	}

	// The rules import must not gain duplicate @import lines.
	rules, err := os.ReadFile(filepath.Join(worktreePath, worktreeRulesFile))
	if err != nil {
		t.Fatalf("reading worktree rules import: %v", err)
	}
	absInstance, _ := filepath.Abs(instanceRoot)
	wantImport := "@" + filepath.Join(absInstance, workspaceContextFile)
	if n := strings.Count(string(rules), wantImport); n != 1 {
		t.Errorf("expected workspace-context import exactly once after re-apply, got %d:\n%s", n, rules)
	}
}

func TestFindRepoGroup(t *testing.T) {
	instanceRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(instanceRoot, "apps", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A .niwa dir at the instance root must be skipped, not treated as a group.
	if err := os.MkdirAll(filepath.Join(instanceRoot, ".niwa", "worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}

	group, err := FindRepoGroup(instanceRoot, "app")
	if err != nil {
		t.Fatalf("FindRepoGroup: %v", err)
	}
	if group != "apps" {
		t.Errorf("group = %q, want %q", group, "apps")
	}

	if _, err := FindRepoGroup(instanceRoot, "nonexistent"); err == nil {
		t.Error("expected error for missing repo, got nil")
	}
}
