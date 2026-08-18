package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// The acceptance criterion for the per-agent gates is bidirectional, so these
// two tests are written as a pair and must be read as one: a repository that
// turns Claude off still receives its full Codex delivery, and a repository
// that turns Codex off still receives its full Claude delivery. The shape they
// replace failed the first direction -- one Claude-named key in front of a loop
// over every agent -- and passed the second by accident, which is why asserting
// only one direction would not have caught it.

// gatedWorktreeFixture is applyToWorktreeFixture plus a resolvable plugin, so a
// gate's effect is visible on two deliveries rather than one: the orientation
// document and the delivered skills tree.
func gatedWorktreeFixture(t *testing.T) (*config.WorkspaceConfig, string, string, string) {
	t.Helper()
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	writeMarketplaceTree(t, filepath.Join(instanceRoot, ".niwa", "marketplaces", "tools"))
	plugins := []string{"shirabe@tools"}
	cfg.Claude.Plugins = &plugins
	cfg.Claude.Marketplaces = config.MarketplaceConfigs{{Source: "acme/tools"}}

	return cfg, configDir, instanceRoot, worktreePath
}

func mustExist(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("%s: %s is missing (%v)", why, path, err)
	}
}

func mustNotExist(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("%s: %s was written anyway", why, path)
	}
}

// TestClaudeDisabledLeavesCodexDeliveryIntact is the direction the prior wiring
// got wrong: claude.enabled = false took every Codex delivery down with it.
func TestClaudeDisabledLeavesCodexDeliveryIntact(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := gatedWorktreeFixture(t)
	cfg.Repos = map[string]config.RepoOverride{
		"app": {Claude: &config.ClaudeOverride{Enabled: boolPtr(false)}},
	}

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	const why = "claude.enabled = false must not reach Codex"
	mustExist(t, filepath.Join(worktreePath, "AGENTS.override.md"), why)
	mustExist(t, filepath.Join(worktreePath, ".codex", "skills", "shirabe"), why)

	doc, err := os.ReadFile(filepath.Join(worktreePath, "AGENTS.override.md"))
	if err != nil {
		t.Fatalf("reading the Codex document: %v", err)
	}
	for _, want := range []string{"app repo content layer", "Worktree Context"} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("%s: the Codex document is missing %q:\n%s", why, want, doc)
		}
	}

	mustNotExist(t, filepath.Join(worktreePath, "CLAUDE.local.md"), "claude.enabled = false must zero Claude's own delivery")
	mustNotExist(t, filepath.Join(worktreePath, ".claude", "settings.local.json"), "claude.enabled = false must zero Claude's own delivery")
}

// TestCodexDisabledLeavesClaudeDeliveryIntact is the mirror direction, and the
// one that keeps the new key from being wired the way the old one was.
func TestCodexDisabledLeavesClaudeDeliveryIntact(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := gatedWorktreeFixture(t)
	cfg.Repos = map[string]config.RepoOverride{
		"app": {Codex: &config.CodexOverride{Enabled: boolPtr(false)}},
	}

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	// Claude's skills arrive through its own plugin system rather than as a
	// delivered tree (see skillsLayouts), so the second delivery asserted here
	// is the settings document and the rules import -- the two writes the
	// Claude-side materialization is responsible for.
	const why = "codex.enabled = false must not reach Claude"
	mustExist(t, filepath.Join(worktreePath, "CLAUDE.local.md"), why)
	mustExist(t, filepath.Join(worktreePath, ".claude", "settings.local.json"), why)
	mustExist(t, filepath.Join(worktreePath, worktreeRulesFile), why)

	doc, err := os.ReadFile(filepath.Join(worktreePath, "CLAUDE.local.md"))
	if err != nil {
		t.Fatalf("reading the Claude document: %v", err)
	}
	for _, want := range []string{"app repo content layer", "Worktree Context"} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("%s: the Claude document is missing %q:\n%s", why, want, doc)
		}
	}

	mustNotExist(t, filepath.Join(worktreePath, "AGENTS.override.md"), "codex.enabled = false must zero Codex's own delivery")
	mustNotExist(t, filepath.Join(worktreePath, ".codex", "skills", "shirabe"), "codex.enabled = false must zero Codex's own delivery")
}

// TestWorkspaceLevelGatesApplyToBothAgents pins the workspace position of both
// keys on the same delivery path, since a gate that only worked per-repo would
// pass the two tests above and still leave [codex] enabled = false inert.
func TestWorkspaceLevelGatesApplyToBothAgents(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := gatedWorktreeFixture(t)
	cfg.Codex = config.CodexConfig{Enabled: boolPtr(false)}

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	mustExist(t, filepath.Join(worktreePath, "CLAUDE.local.md"), "the workspace-level codex gate must not reach Claude")
	mustNotExist(t, filepath.Join(worktreePath, "AGENTS.override.md"), "the workspace-level codex gate must zero Codex's delivery")
}
