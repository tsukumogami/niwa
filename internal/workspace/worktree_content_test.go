package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/resolve"
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

// TestApplyToWorktreeMaterializesVaultResolvedEnv pins the issue #162 fix from
// the worktree-wiring side: when applyContentToWorktree drives the apply path
// through the shared ResolveAndMergeEffectiveConfig helper, a vault://-backed
// env.secrets entry is resolved BEFORE ApplyToWorktree runs and the worktree
// .local.env carries the resolved plaintext rather than the literal vault://
// URI.
//
// The test drives the wiring directly (helper -> ApplyToWorktree) rather than
// going through the CLI so it exercises the workspace-package contract: any
// caller that runs the helper first must end up with plaintext in the worktree.
func TestApplyToWorktreeMaterializesVaultResolvedEnv(t *testing.T) {
	withFakeVaultBackend(t)

	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	const want = "resolved-token-value-xxxxx"
	cfg.Vault = helperFakeProviderRegistry(map[string]string{"API_TOKEN": want})
	cfg.Env = config.EnvConfig{
		Secrets: config.EnvVarsTable{
			Values: map[string]config.MaybeSecret{
				"API_TOKEN": {Plain: "vault://API_TOKEN"},
			},
		},
	}

	ctx := context.Background()
	teamBundle, err := resolve.BuildBundle(ctx, vault.DefaultRegistry, cfg.Vault, "test team")
	if err != nil {
		t.Fatalf("BuildBundle team: %v", err)
	}
	defer teamBundle.CloseAll()
	personalBundle, err := resolve.BuildBundle(ctx, vault.DefaultRegistry, nil, "test personal")
	if err != nil {
		t.Fatalf("BuildBundle personal: %v", err)
	}
	defer personalBundle.CloseAll()

	effective, _, _, err := ResolveAndMergeEffectiveConfig(
		ctx, cfg, nil, teamBundle, personalBundle,
		EffectiveConfigOptions{AllowMissingSecrets: true},
	)
	if err != nil {
		t.Fatalf("ResolveAndMergeEffectiveConfig: %v", err)
	}

	if _, err := ApplyToWorktree(effective, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	envPath := filepath.Join(worktreePath, ".local.env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("reading worktree .local.env: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "API_TOKEN="+want) {
		t.Errorf("worktree .local.env missing resolved value %q, got:\n%s", want, content)
	}
	if strings.Contains(content, "vault://") {
		t.Errorf("worktree .local.env must not contain literal vault:// URI:\n%s", content)
	}
}

// TestApplyToWorktreeMergesPersonalOverlayEnv pins the other half of the
// issue #162 fix: a personal global override declaring an env key reaches the
// worktree .local.env once the shared helper runs MergeGlobalOverride.
// Without this step the worktree path silently drops every overlay-only env
// key -- causing the GH_TOKEN promote failure observed during this worktree's
// own creation before the fix.
func TestApplyToWorktreeMergesPersonalOverlayEnv(t *testing.T) {
	withFakeVaultBackend(t)

	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	override := &config.GlobalConfigOverride{
		Global: config.GlobalOverride{
			Env: config.EnvConfig{
				Vars: config.EnvVarsTable{
					Values: map[string]config.MaybeSecret{
						"PERSONAL_KEY": {Plain: "personal-value"},
					},
				},
			},
		},
	}

	ctx := context.Background()
	teamBundle, err := resolve.BuildBundle(ctx, vault.DefaultRegistry, nil, "test team")
	if err != nil {
		t.Fatalf("BuildBundle team: %v", err)
	}
	defer teamBundle.CloseAll()
	personalBundle, err := resolve.BuildBundle(ctx, vault.DefaultRegistry, nil, "test personal")
	if err != nil {
		t.Fatalf("BuildBundle personal: %v", err)
	}
	defer personalBundle.CloseAll()

	effective, _, _, err := ResolveAndMergeEffectiveConfig(
		ctx, cfg, override, teamBundle, personalBundle,
		EffectiveConfigOptions{AllowMissingSecrets: true},
	)
	if err != nil {
		t.Fatalf("ResolveAndMergeEffectiveConfig: %v", err)
	}

	if _, err := ApplyToWorktree(effective, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	envPath := filepath.Join(worktreePath, ".local.env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("reading worktree .local.env: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "PERSONAL_KEY=personal-value") {
		t.Errorf("worktree .local.env missing personal-overlay key PERSONAL_KEY=personal-value, got:\n%s", content)
	}
}
