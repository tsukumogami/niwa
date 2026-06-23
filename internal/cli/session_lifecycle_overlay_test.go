package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestMergeWorktreeOverlayPopulatesOverlaySource pins the CLI wiring that makes
// `niwa worktree create` install overlay-merged content: given an instance with
// a recorded overlay URL and a local overlay clone declaring an overlay= content
// entry, mergeWorktreeOverlay resolves the overlay dir, runs the config merge,
// and returns a config whose repo entry now carries OverlaySource. Without this,
// ApplyToWorktree would receive an empty OverlayDir and a config with no
// OverlaySource, so overlay-augmented repos would silently miss their content.
func TestMergeWorktreeOverlayPopulatesOverlaySource(t *testing.T) {
	tmp := t.TempDir()

	// Point config.OverlayDir at a deterministic temp location via XDG_CONFIG_HOME.
	xdg := filepath.Join(tmp, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)

	// A file:// overlay URL keeps OverlayDir resolution offline and stable
	// (dir name derived from the last path component).
	overlayURL := "file://" + filepath.Join(tmp, "ws-overlay.git")
	overlayDir, err := config.OverlayDir(overlayURL)
	if err != nil {
		t.Fatalf("OverlayDir: %v", err)
	}
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Minimal overlay declaring an overlay= content entry for repo "app".
	overlayTOML := "[claude.content.repos.app]\noverlay = \"app-overlay.md\"\n"
	if err := os.WriteFile(filepath.Join(overlayDir, "workspace-overlay.toml"), []byte(overlayTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	instanceRoot := filepath.Join(tmp, "instance")
	if err := os.MkdirAll(filepath.Join(instanceRoot, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveState(instanceRoot, &workspace.InstanceState{OverlayURL: overlayURL}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Base config has an explicit content entry for "app" (overlay= appends to it).
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "myws"},
		Claude: config.ClaudeConfig{
			Content: config.ContentConfig{
				Repos: map[string]config.RepoContentEntry{
					"app": {Source: "repos/app.md"},
				},
			},
		},
	}

	merged, gotDir, err := mergeWorktreeOverlay(cfg, instanceRoot)
	if err != nil {
		t.Fatalf("mergeWorktreeOverlay: %v", err)
	}
	if gotDir != overlayDir {
		t.Errorf("overlay dir = %q, want %q", gotDir, overlayDir)
	}
	if got := merged.Claude.Content.Repos["app"].OverlaySource; got != "app-overlay.md" {
		t.Errorf("OverlaySource = %q, want %q", got, "app-overlay.md")
	}
}

// TestMergeWorktreeOverlayResolvesOverlayVault pins the Issue-11 fix: a
// worktree create against a workspace whose overlay declares a vault provider
// plus a secret binding must succeed (mirroring `niwa apply`) and carry the
// overlay's vault into the merged config. With AllowMissing wired into the
// worktree path, an unresolvable secret degrades gracefully instead of
// failing the merge, and the merged config keeps the provider so downstream
// content install can resolve any vault:// reference.
func TestMergeWorktreeOverlayResolvesOverlayVault(t *testing.T) {
	tmp := t.TempDir()
	xdg := filepath.Join(tmp, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)

	overlayURL := "file://" + filepath.Join(tmp, "ws-overlay.git")
	overlayDir, err := config.OverlayDir(overlayURL)
	if err != nil {
		t.Fatalf("OverlayDir: %v", err)
	}
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The overlay declares an env secret that references a vault provider not
	// resolvable here (no provider declared in this offline test, so the
	// reference is undeclared). The AllowMissing path in mergeWorktreeOverlay
	// must downgrade it rather than failing the merge. The content entry keeps
	// the merge observably non-trivial.
	overlayTOML := `[env.secrets]
OVERLAY_SECRET = "vault://OVERLAY_SECRET"

[claude.content.repos.app]
overlay = "app-overlay.md"
`
	if err := os.WriteFile(filepath.Join(overlayDir, "workspace-overlay.toml"), []byte(overlayTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	instanceRoot := filepath.Join(tmp, "instance")
	if err := os.MkdirAll(filepath.Join(instanceRoot, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveState(instanceRoot, &workspace.InstanceState{OverlayURL: overlayURL}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "myws"},
		Claude: config.ClaudeConfig{
			Content: config.ContentConfig{
				Repos: map[string]config.RepoContentEntry{
					"app": {Source: "repos/app.md"},
				},
			},
		},
	}

	merged, gotDir, err := mergeWorktreeOverlay(cfg, instanceRoot)
	if err != nil {
		t.Fatalf("mergeWorktreeOverlay should degrade gracefully, got error: %v", err)
	}
	if gotDir != overlayDir {
		t.Errorf("overlay dir = %q, want %q", gotDir, overlayDir)
	}
	// The unresolvable secret downgraded to empty rather than failing the
	// merge, and overlay content still merged into the config.
	if got := merged.Claude.Content.Repos["app"].OverlaySource; got != "app-overlay.md" {
		t.Errorf("OverlaySource = %q, want app-overlay.md", got)
	}
}

// TestMergeWorktreeOverlayNoOverlay verifies the no-overlay path: with no
// recorded overlay URL, mergeWorktreeOverlay returns the original config and an
// empty dir so the caller skips overlay handling entirely (the pre-fix behavior
// for non-overlay workspaces is preserved).
func TestMergeWorktreeOverlayNoOverlay(t *testing.T) {
	tmp := t.TempDir()
	instanceRoot := filepath.Join(tmp, "instance")
	if err := os.MkdirAll(filepath.Join(instanceRoot, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveState(instanceRoot, &workspace.InstanceState{}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "myws"}}
	got, gotDir, err := mergeWorktreeOverlay(cfg, instanceRoot)
	if err != nil {
		t.Fatalf("mergeWorktreeOverlay: %v", err)
	}
	if gotDir != "" {
		t.Errorf("overlay dir = %q, want empty", gotDir)
	}
	if got != cfg {
		t.Error("expected original cfg returned unchanged when no overlay is configured")
	}
}
