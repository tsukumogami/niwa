package workspace

import (
	"os"
	"os/exec"
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

// TestApplyToWorktreeInjectsPluginPathEnv proves a `niwa worktree`-created
// session gets the resolved [[claude.plugin_path_env]] binding (e.g.
// SHIRABE_WORK_SUMMARY) materialized into its settings.local.json, so the
// capture hook resolves the plugin script instead of no-oping. Injection mirrors
// the instance apply pipeline.
func TestApplyToWorktreeInjectsPluginPathEnv(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	cfg.Claude.PluginPathEnv = []config.PluginPathEnvBinding{
		{Name: "SHIRABE_WORK_SUMMARY", Plugin: "work-summary@shirabe", Path: "scripts/render.sh"},
	}
	installDir := fakePluginDir(t, "scripts/render.sh")
	wantPath := filepath.Join(installDir, "scripts/render.sh")

	opts := WorktreeApplyOptions{
		PluginInstallPath: func(key string) (string, bool) {
			if pluginKeyMatches("work-summary@shirabe", key) {
				return installDir, true
			}
			return "", false
		},
	}
	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", opts); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	env := readSettingsEnv(t, filepath.Join(worktreePath, ".claude", "settings.local.json"))
	if env["SHIRABE_WORK_SUMMARY"] != wantPath {
		t.Errorf("worktree settings SHIRABE_WORK_SUMMARY = %q, want %q", env["SHIRABE_WORK_SUMMARY"], wantPath)
	}
}

// TestApplyToWorktreeUnresolvablePluginPathEnvIsAbsent proves the fail-safe on
// the worktree path: an unresolvable binding injects no variable (the hook reads
// empty and no-ops) and the worktree apply still succeeds.
func TestApplyToWorktreeUnresolvablePluginPathEnvIsAbsent(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	cfg.Claude.PluginPathEnv = []config.PluginPathEnvBinding{
		{Name: "SHIRABE_WORK_SUMMARY", Plugin: "work-summary@shirabe", Path: "scripts/render.sh"},
	}
	opts := WorktreeApplyOptions{
		PluginInstallPath: func(string) (string, bool) { return "", false },
	}
	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", opts); err != nil {
		t.Fatalf("ApplyToWorktree must still succeed when the plugin is unresolvable: %v", err)
	}

	settingsPath := filepath.Join(worktreePath, ".claude", "settings.local.json")
	if _, err := os.Stat(settingsPath); err == nil {
		if _, present := readSettingsEnv(t, settingsPath)["SHIRABE_WORK_SUMMARY"]; present {
			t.Error("SHIRABE_WORK_SUMMARY must be absent when the plugin is unresolvable (fail-safe)")
		}
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

// TestApplyToWorktreeInstallsOverlayMergedContent pins the overlay path: a repo
// whose content entry carries an OverlaySource (as MergeWorkspaceOverlay sets
// for overlay-augmented repos) must succeed and have its overlay content
// appended to the worktree's CLAUDE.local.md when opts.OverlayDir is set. This
// is the regression guard for the create-time hard error: InstallRepoContentTo
// returns "...OverlaySource ... but overlayDir is empty" when the CLI fails to
// wire opts.OverlayDir, so a non-empty OverlayDir here is load-bearing.
func TestApplyToWorktreeInstallsOverlayMergedContent(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	// Stand up an overlay clone dir carrying the overlay content fragment.
	overlayDir := filepath.Join(t.TempDir(), "overlay")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const overlayMarker = "overlay-only content fragment for app"
	if err := os.WriteFile(filepath.Join(overlayDir, "app-overlay.md"), []byte("# overlay\n\n"+overlayMarker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate the post-merge config: the app entry has base Source plus the
	// OverlaySource that MergeWorkspaceOverlay populates from an overlay=
	// content entry.
	entry := cfg.Claude.Content.Repos["app"]
	entry.OverlaySource = "app-overlay.md"
	cfg.Claude.Content.Repos["app"] = entry

	written, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz",
		WorktreeApplyOptions{OverlayDir: overlayDir})
	if err != nil {
		t.Fatalf("ApplyToWorktree with overlay source: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("expected written files, got none")
	}

	local, err := os.ReadFile(filepath.Join(worktreePath, "CLAUDE.local.md"))
	if err != nil {
		t.Fatalf("reading worktree CLAUDE.local.md: %v", err)
	}
	// Base content and overlay-appended content must both be present.
	if !strings.Contains(string(local), "app repo content layer") {
		t.Errorf("base repo content missing from worktree CLAUDE.local.md:\n%s", local)
	}
	if !strings.Contains(string(local), overlayMarker) {
		t.Errorf("overlay-merged content missing from worktree CLAUDE.local.md:\n%s", local)
	}
}

// TestApplyToWorktreeOverlaySourceRequiresOverlayDir documents the failure mode
// the fix avoids: when a repo carries an OverlaySource but opts.OverlayDir is
// empty, InstallRepoContentTo hard-errors. This is exactly what `niwa worktree
// create` produced before the CLI resolved the overlay dir.
func TestApplyToWorktreeOverlaySourceRequiresOverlayDir(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	entry := cfg.Claude.Content.Repos["app"]
	entry.OverlaySource = "app-overlay.md"
	cfg.Claude.Content.Repos["app"] = entry

	_, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz",
		WorktreeApplyOptions{}) // OverlayDir left empty
	if err == nil {
		t.Fatal("expected an error when OverlaySource is set but OverlayDir is empty, got nil")
	}
	if !strings.Contains(err.Error(), "overlayDir is empty") {
		t.Errorf("expected overlayDir-empty error, got: %v", err)
	}
}

// TestApplyToWorktreeRendersConfiguredTemplate pins Stage-3: when
// [claude.content.worktree].source is set, the worktree-context section is
// rendered from that template with the worktree variables ({purpose}/{branch}/
// {repo_name}/{worktree_path}) expanded, replacing the default body.
func TestApplyToWorktreeRendersConfiguredTemplate(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	// Write the worktree template into the content dir and point the config at it.
	tmpl := "Repo {repo_name} on branch {branch}.\n\nFocus: {purpose}\nPath: {worktree_path}\nWorkspace: {workspace_name}\n"
	if err := os.WriteFile(filepath.Join(configDir, "claude", "worktree.md"), []byte(tmpl), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Claude.Content.Worktree = config.ContentEntry{Source: "worktree.md"}

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	local, err := os.ReadFile(filepath.Join(worktreePath, "CLAUDE.local.md"))
	if err != nil {
		t.Fatalf("reading worktree CLAUDE.local.md: %v", err)
	}
	body := string(local)

	if !strings.Contains(body, worktreeContextHeading) {
		t.Errorf("worktree context heading missing:\n%s", body)
	}
	if !strings.Contains(body, "Repo app on branch branch-xyz") {
		t.Errorf("repo_name/branch not expanded from template:\n%s", body)
	}
	if !strings.Contains(body, "Focus: ship-the-thing") {
		t.Errorf("purpose not expanded from template:\n%s", body)
	}
	if !strings.Contains(body, "Workspace: myws") {
		t.Errorf("workspace_name not expanded from template:\n%s", body)
	}
	absWorktree, _ := filepath.Abs(worktreePath)
	if !strings.Contains(body, "Path: "+absWorktree) {
		t.Errorf("worktree_path not expanded from template:\n%s", body)
	}
	// The default purpose/branch phrasing must NOT appear when a template is set.
	if strings.Contains(body, "This is a niwa worktree of repo") {
		t.Errorf("default layer body leaked despite configured template:\n%s", body)
	}
}

// TestApplyToWorktreeConfiguredTemplateIsIdempotent pins idempotency for the
// CONFIGURED template path (not just the default-layer path): re-applying with a
// [claude.content.worktree].source set must replace the worktree-context section
// in place, so the sentinel heading and the template body each appear exactly
// once after multiple applies.
func TestApplyToWorktreeConfiguredTemplateIsIdempotent(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	tmpl := "Repo {repo_name} on branch {branch}.\n\nFocus: {purpose}\n"
	if err := os.WriteFile(filepath.Join(configDir, "claude", "worktree.md"), []byte(tmpl), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Claude.Content.Worktree = config.ContentEntry{Source: "worktree.md"}

	for i := 0; i < 3; i++ {
		if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
			t.Fatalf("ApplyToWorktree iteration %d: %v", i, err)
		}
	}

	local, err := os.ReadFile(filepath.Join(worktreePath, "CLAUDE.local.md"))
	if err != nil {
		t.Fatalf("reading worktree CLAUDE.local.md: %v", err)
	}
	body := string(local)

	if n := strings.Count(body, worktreeContextHeading); n != 1 {
		t.Errorf("expected worktree context heading exactly once after 3 applies, got %d:\n%s", n, body)
	}
	// The rendered template body line must also appear exactly once (the section
	// is replaced, not stacked).
	if n := strings.Count(body, "Repo app on branch branch-xyz"); n != 1 {
		t.Errorf("expected rendered template body exactly once after 3 applies, got %d:\n%s", n, body)
	}
	if n := strings.Count(body, "Focus: ship-the-thing"); n != 1 {
		t.Errorf("expected rendered purpose line exactly once after 3 applies, got %d:\n%s", n, body)
	}
}

// TestApplyToWorktreeTemplateWritesNoTempFile guards the Fix-2 cleanup: the
// configured-template path renders in memory and must never write a transient
// .niwa-worktree-layer.tmp dotfile into the worktree.
func TestApplyToWorktreeTemplateWritesNoTempFile(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	tmpl := "Repo {repo_name} on branch {branch}.\n"
	if err := os.WriteFile(filepath.Join(configDir, "claude", "worktree.md"), []byte(tmpl), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Claude.Content.Worktree = config.ContentEntry{Source: "worktree.md"}

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	tmpPath := filepath.Join(worktreePath, ".niwa-worktree-layer.tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("expected no transient %s, but stat returned err=%v", tmpPath, err)
	}
}

// TestApplyToWorktreeUnsetTemplateUsesDefaultLayer is the regression guard for
// the additive contract: with no [claude.content.worktree] configured, the
// Stage-1 default purpose/branch layer is produced unchanged.
func TestApplyToWorktreeUnsetTemplateUsesDefaultLayer(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	// cfg.Claude.Content.Worktree is the zero ContentEntry (unset).

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	local, err := os.ReadFile(filepath.Join(worktreePath, "CLAUDE.local.md"))
	if err != nil {
		t.Fatalf("reading worktree CLAUDE.local.md: %v", err)
	}
	body := string(local)

	if !strings.Contains(body, `This is a niwa worktree of repo "app"`) {
		t.Errorf("default layer body missing when template unset:\n%s", body)
	}
	if !strings.Contains(body, "- Purpose: ship-the-thing") {
		t.Errorf("default purpose line missing:\n%s", body)
	}
	if !strings.Contains(body, "- Branch: branch-xyz") {
		t.Errorf("default branch line missing:\n%s", body)
	}
}

// TestApplyToWorktreeRunsWorktreeHook pins that a discovered worktree-event hook
// runs on create/apply: the script writes a marker file into the worktree, and
// the worktree context is available to it via the NIWA_WORKTREE_* environment.
func TestApplyToWorktreeRunsWorktreeHook(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	hooksDir := filepath.Join(configDir, "worktree-hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$NIWA_WORKTREE_PURPOSE\" > \"$NIWA_WORKTREE_PATH/hook-ran.txt\"\n"
	scriptPath := filepath.Join(hooksDir, "apply.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	marker, err := os.ReadFile(filepath.Join(worktreePath, "hook-ran.txt"))
	if err != nil {
		t.Fatalf("expected worktree hook to write marker file: %v", err)
	}
	if got := strings.TrimSpace(string(marker)); got != "ship-the-thing" {
		t.Errorf("hook env NIWA_WORKTREE_PURPOSE = %q, want %q", got, "ship-the-thing")
	}
}

// TestApplyToWorktreeNonExecutableHookSkipped confirms a non-executable hook is
// warned-and-skipped (not a hard failure), matching the setup-script policy.
func TestApplyToWorktreeNonExecutableHookSkipped(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	hooksDir := filepath.Join(configDir, "worktree-hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "apply.sh"), []byte("#!/bin/sh\ntrue\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree must not fail on a non-executable hook: %v", err)
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

// TestApplyToWorktreeInheritsCloneEnv pins the new contract (DESIGN decision
// A1): a worktree's env is INHERITED from the instance clone's already-
// materialized output file by byte-copy, with NO secret resolution. The test
// writes a clone .local.env carrying resolved plaintext (the shape the instance
// apply pipeline produced) and asserts the worktree's .local.env is byte-
// identical -- including that a value the clone resolved from vault:// arrives
// as plaintext and no literal vault:// URI leaks into the worktree.
func TestApplyToWorktreeInheritsCloneEnv(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	// The repo has env configured (a workspace env file is enough for the
	// "configured" predicate), so a present clone output is required to inherit.
	cfg.Env = config.EnvConfig{Files: []string{"workspace.env"}}
	if err := os.WriteFile(filepath.Join(configDir, "workspace.env"), []byte("PLACEHOLDER=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The instance clone of the repo lives at <instanceRoot>/<group>/<repo> and
	// already holds a materialized .local.env (resolved plaintext, no vault://).
	cloneRepoDir := filepath.Join(instanceRoot, "apps", "app")
	if err := os.MkdirAll(cloneRepoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const cloneEnv = "API_TOKEN=resolved-token-value-xxxxx\nPLACEHOLDER=1\n"
	if err := os.WriteFile(filepath.Join(cloneRepoDir, ".local.env"), []byte(cloneEnv), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(worktreePath, ".local.env"))
	if err != nil {
		t.Fatalf("reading worktree .local.env: %v", err)
	}
	if string(got) != cloneEnv {
		t.Errorf("worktree env not byte-identical to clone:\n got: %q\nwant: %q", got, cloneEnv)
	}
	if strings.Contains(string(got), "vault://") {
		t.Errorf("worktree .local.env must not contain literal vault:// URI:\n%s", got)
	}
}

// TestApplyToWorktreeLeavesGitStatusClean is the regression guard for the
// delegated-worktree teardown bug: a freshly created niwa worktree must read
// CLEAN to `git status --porcelain` after ApplyToWorktree, with no user
// changes. If any niwa-authored file (notably .claude/rules/worktree-imports.md)
// is left untracked, the non-force from-hook WorktreeRemove path treats the
// worktree as dirty and log-and-retains it, leaking an orphan on every clean
// agent teardown. The test builds a real git worktree (the production scaffold
// shape) and asserts both that niwa content is invisible AND that a genuine
// user file still shows (the exclude is scoped, not a blanket .claude/ ignore).
func TestApplyToWorktreeLeavesGitStatusClean(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	tmpDir := t.TempDir()

	// Config dir with a repo content source (same shape as the shared fixture).
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

	// Instance root carrying a workspace-context.md for the rules import target.
	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instanceRoot, workspaceContextFile), []byte("# workspace context\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Build a real git repo and add a worktree, mirroring how CreateSession
	// scaffolds a delegated worktree: a primary checkout with one commit, then a
	// linked worktree on a new branch. EnsureRepoExclude resolves the shared
	// common dir from the worktree, so coverage recorded here is what production
	// records.
	primary := filepath.Join(tmpDir, "primary")
	runGitWT(t, tmpDir, "init", primary)
	if err := os.WriteFile(filepath.Join(primary, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitWT(t, primary, "add", "README")
	runGitWT(t, primary, "commit", "-m", "init")

	worktreePath := filepath.Join(tmpDir, "wt")
	runGitWT(t, primary, "worktree", "add", worktreePath, "-b", "wtbranch")

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	// Sanity: the rules import file (the formerly-uncovered file) was written.
	if _, err := os.Stat(filepath.Join(worktreePath, worktreeRulesFile)); err != nil {
		t.Fatalf("expected %s to be written: %v", worktreeRulesFile, err)
	}

	if out := gitStatusPorcelainWT(t, worktreePath); out != "" {
		t.Errorf("freshly applied niwa worktree must read clean, got:\n%s", out)
	}

	// The exclude is scoped: a genuine user-authored file under .claude/ still
	// shows, proving we did not blanket-ignore the whole .claude/ tree. git
	// summarizes an untracked directory's contents as a single "?? .claude/"
	// entry, so a non-empty status referencing .claude proves the user file
	// surfaced (it would be empty if .claude/ were entirely ignored).
	userFile := filepath.Join(worktreePath, ".claude", "user-notes.md")
	if err := os.WriteFile(userFile, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := gitStatusPorcelainWT(t, worktreePath)
	if out == "" || !strings.Contains(out, ".claude") {
		t.Errorf("a genuine user .claude/ file must still show in status, got:\n%q", out)
	}
}

func gitStatusPorcelainWT(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status --porcelain: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func runGitWT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
