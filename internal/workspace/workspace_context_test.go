package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// minimalCfg returns a *config.WorkspaceConfig suitable for calling
// InstallWorkspaceContext. The workspace name is set to "test-ws".
func minimalCfg() *config.WorkspaceConfig {
	return &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name: "test-ws",
		},
	}
}

// TestInstallWorkspaceContextWritesRulesFile verifies that InstallWorkspaceContext
// creates .claude/rules/workspace-imports.md with an absolute @import, and does
// not add any @import to CLAUDE.md.
func TestInstallWorkspaceContextWritesRulesFile(t *testing.T) {
	tmpDir := t.TempDir()
	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	paths, err := InstallWorkspaceContext(minimalCfg(), nil, instanceRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 returned paths, got %d: %v", len(paths), paths)
	}

	// Verify rules file contains absolute @import.
	rulesPath := filepath.Join(instanceRoot, workspaceRulesFile)
	data, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("reading rules file: %v", err)
	}
	contextPath := filepath.Join(instanceRoot, workspaceContextFile)
	wantImport := "@" + contextPath
	if !strings.Contains(string(data), wantImport) {
		t.Errorf("rules file missing absolute import %q:\n%s", wantImport, string(data))
	}

	// CLAUDE.md should not contain the old relative import.
	claudePath := filepath.Join(instanceRoot, "CLAUDE.md")
	if _, err := os.Stat(claudePath); err == nil {
		claudeData, _ := os.ReadFile(claudePath)
		if strings.Contains(string(claudeData), workspaceContextImport) {
			t.Errorf("CLAUDE.md should not contain %q:\n%s", workspaceContextImport, string(claudeData))
		}
	}
}

// TestInstallWorkspaceContextMigratesOldImport verifies that if CLAUDE.md has
// the old relative @workspace-context.md import, it is removed after install.
func TestInstallWorkspaceContextMigratesOldImport(t *testing.T) {
	tmpDir := t.TempDir()
	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	// Simulate old format.
	oldContent := "@workspace-context.md\n\n# Workspace\n"
	claudePath := filepath.Join(instanceRoot, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte(oldContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallWorkspaceContext(minimalCfg(), nil, instanceRoot); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Old import must be gone from CLAUDE.md.
	claudeData, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	if strings.Contains(string(claudeData), workspaceContextImport) {
		t.Errorf("old relative import still present in CLAUDE.md:\n%s", string(claudeData))
	}

	// Absolute import must be in rules file.
	rulesPath := filepath.Join(instanceRoot, workspaceRulesFile)
	rulesData, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("reading rules file: %v", err)
	}
	contextPath := filepath.Join(instanceRoot, workspaceContextFile)
	if !strings.Contains(string(rulesData), "@"+contextPath) {
		t.Errorf("rules file missing absolute import for workspace-context:\n%s", string(rulesData))
	}
}

// TestInstallWorkspaceContextIdempotent verifies that calling
// InstallWorkspaceContext twice produces exactly one @workspace-context line in
// the rules file.
func TestInstallWorkspaceContextIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallWorkspaceContext(minimalCfg(), nil, instanceRoot); err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if _, err := InstallWorkspaceContext(minimalCfg(), nil, instanceRoot); err != nil {
		t.Fatalf("second call error: %v", err)
	}

	rulesPath := filepath.Join(instanceRoot, workspaceRulesFile)
	data, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("reading rules file: %v", err)
	}
	contextPath := filepath.Join(instanceRoot, workspaceContextFile)
	count := strings.Count(string(data), "@"+contextPath)
	if count != 1 {
		t.Errorf("workspace-context import appears %d times in rules file, want 1:\n%s", count, string(data))
	}
}

// TestInstallOverlayClaudeContentPresent verifies that CLAUDE.overlay.md is
// copied to the instance root, and the rules file contains both workspace-context
// and overlay imports. CLAUDE.md must not contain @CLAUDE.overlay.md.
func TestInstallOverlayClaudeContentPresent(t *testing.T) {
	tmpDir := t.TempDir()
	overlayDir := filepath.Join(tmpDir, "overlay")
	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	overlayContent := "# Overlay Claude Content\n"
	if err := os.WriteFile(filepath.Join(overlayDir, "CLAUDE.overlay.md"), []byte(overlayContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate prior workspace-context install by creating the rules file.
	contextPath := filepath.Join(instanceRoot, workspaceContextFile)
	rulesPath := filepath.Join(instanceRoot, workspaceRulesFile)
	if err := writeWorkspaceRulesFile(rulesPath, contextPath); err != nil {
		t.Fatal(err)
	}

	path, err := InstallOverlayClaudeContent(overlayDir, instanceRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path when CLAUDE.overlay.md is present")
	}

	// Verify the file was copied.
	data, err := os.ReadFile(filepath.Join(instanceRoot, "CLAUDE.overlay.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.overlay.md: %v", err)
	}
	if string(data) != overlayContent {
		t.Errorf("CLAUDE.overlay.md content = %q, want %q", string(data), overlayContent)
	}

	// Rules file must contain both imports.
	rulesData, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("reading rules file: %v", err)
	}
	rulesContent := string(rulesData)
	overlayDestPath := filepath.Join(instanceRoot, overlayClaudeFile)
	if !strings.Contains(rulesContent, "@"+contextPath) {
		t.Errorf("rules file missing workspace-context import:\n%s", rulesContent)
	}
	if !strings.Contains(rulesContent, "@"+overlayDestPath) {
		t.Errorf("rules file missing overlay import:\n%s", rulesContent)
	}

	// CLAUDE.md must not contain the relative overlay import.
	claudePath := filepath.Join(instanceRoot, "CLAUDE.md")
	if _, err := os.Stat(claudePath); err == nil {
		claudeData, _ := os.ReadFile(claudePath)
		if strings.Contains(string(claudeData), overlayClaudeImport) {
			t.Errorf("CLAUDE.md should not contain %q:\n%s", overlayClaudeImport, string(claudeData))
		}
	}
}

// TestInstallOverlayClaudeContentAbsent verifies that when CLAUDE.overlay.md
// does not exist in the overlay clone, the function returns ("", nil) and the
// rules file is unchanged.
func TestInstallOverlayClaudeContentAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	overlayDir := filepath.Join(tmpDir, "overlay")
	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	// Simulate prior workspace-context install.
	contextPath := filepath.Join(instanceRoot, workspaceContextFile)
	rulesPath := filepath.Join(instanceRoot, workspaceRulesFile)
	if err := writeWorkspaceRulesFile(rulesPath, contextPath); err != nil {
		t.Fatal(err)
	}
	originalRules, _ := os.ReadFile(rulesPath)

	path, err := InstallOverlayClaudeContent(overlayDir, instanceRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path when CLAUDE.overlay.md is absent, got %q", path)
	}

	// Rules file should be unchanged.
	rulesData, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("reading rules file: %v", err)
	}
	if string(rulesData) != string(originalRules) {
		t.Errorf("rules file modified unexpectedly:\ngot  %q\nwant %q", string(rulesData), string(originalRules))
	}

	// CLAUDE.overlay.md should not exist in instance root.
	if _, err := os.Stat(filepath.Join(instanceRoot, "CLAUDE.overlay.md")); err == nil {
		t.Error("CLAUDE.overlay.md should not exist when absent from overlay")
	}
}

// TestInstallOverlayClaudeContentImportOrdering verifies that workspace-context
// import appears before overlay import in the rules file.
func TestInstallOverlayClaudeContentImportOrdering(t *testing.T) {
	tmpDir := t.TempDir()
	overlayDir := filepath.Join(tmpDir, "overlay")
	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(overlayDir, "CLAUDE.overlay.md"), []byte("# overlay\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Step 1: workspace-context install.
	if _, err := InstallWorkspaceContext(minimalCfg(), nil, instanceRoot); err != nil {
		t.Fatalf("InstallWorkspaceContext error: %v", err)
	}

	// Step 2: overlay install.
	if _, err := InstallOverlayClaudeContent(overlayDir, instanceRoot); err != nil {
		t.Fatalf("InstallOverlayClaudeContent error: %v", err)
	}

	// Step 3: global install.
	globalDir := filepath.Join(tmpDir, "global")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "CLAUDE.global.md"), []byte("# global\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallGlobalClaudeContent(globalDir, instanceRoot); err != nil {
		t.Fatalf("InstallGlobalClaudeContent error: %v", err)
	}

	rulesPath := filepath.Join(instanceRoot, workspaceRulesFile)
	rulesData, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("reading rules file: %v", err)
	}
	rulesContent := string(rulesData)

	contextPath := filepath.Join(instanceRoot, workspaceContextFile)
	overlayDestPath := filepath.Join(instanceRoot, overlayClaudeFile)

	wsIdx := strings.Index(rulesContent, "@"+contextPath)
	overlayIdx := strings.Index(rulesContent, "@"+overlayDestPath)

	if wsIdx < 0 || overlayIdx < 0 {
		t.Fatalf("missing expected imports in rules file:\n%s", rulesContent)
	}
	if wsIdx >= overlayIdx {
		t.Errorf("workspace-context import should appear before overlay import in rules file:\n%s", rulesContent)
	}
}

// TestInstallOverlayClaudeContentIdempotent verifies that calling
// InstallOverlayClaudeContent twice produces exactly one overlay import in the
// rules file.
func TestInstallOverlayClaudeContentIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	overlayDir := filepath.Join(tmpDir, "overlay")
	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(overlayDir, "CLAUDE.overlay.md"), []byte("# overlay\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate prior workspace-context install.
	contextPath := filepath.Join(instanceRoot, workspaceContextFile)
	rulesPath := filepath.Join(instanceRoot, workspaceRulesFile)
	if err := writeWorkspaceRulesFile(rulesPath, contextPath); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallOverlayClaudeContent(overlayDir, instanceRoot); err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if _, err := InstallOverlayClaudeContent(overlayDir, instanceRoot); err != nil {
		t.Fatalf("second call error: %v", err)
	}

	rulesData, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("reading rules file: %v", err)
	}
	overlayDestPath := filepath.Join(instanceRoot, overlayClaudeFile)
	count := strings.Count(string(rulesData), "@"+overlayDestPath)
	if count != 1 {
		t.Errorf("overlay import appears %d times in rules file, want 1:\n%s", count, string(rulesData))
	}
}

// TestInstallOverlayClaudeContentMigratesOldImport verifies that the old
// relative @CLAUDE.overlay.md import is removed from CLAUDE.md after install.
func TestInstallOverlayClaudeContentMigratesOldImport(t *testing.T) {
	tmpDir := t.TempDir()
	overlayDir := filepath.Join(tmpDir, "overlay")
	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(overlayDir, "CLAUDE.overlay.md"), []byte("# overlay\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate old CLAUDE.md with relative import.
	oldContent := "@workspace-context.md\n\n@CLAUDE.overlay.md\n\n# Workspace\n"
	claudePath := filepath.Join(instanceRoot, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte(oldContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallOverlayClaudeContent(overlayDir, instanceRoot); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	claudeData, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	if strings.Contains(string(claudeData), overlayClaudeImport) {
		t.Errorf("old relative overlay import still present in CLAUDE.md:\n%s", string(claudeData))
	}
}

// TestInstallGlobalClaudeContentOrderingWithOverlay verifies that when
// workspace-context and overlay imports are already in the rules file, the
// global import is appended after them in the correct order.
func TestInstallGlobalClaudeContentOrderingWithOverlay(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(globalDir, "CLAUDE.global.md"), []byte("# global\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate state after workspace-context and overlay installs.
	contextPath := filepath.Join(instanceRoot, workspaceContextFile)
	overlayDestPath := filepath.Join(instanceRoot, overlayClaudeFile)
	rulesPath := filepath.Join(instanceRoot, workspaceRulesFile)
	if err := writeWorkspaceRulesFile(rulesPath, contextPath); err != nil {
		t.Fatal(err)
	}
	if err := appendToWorkspaceRulesFile(rulesPath, overlayDestPath); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallGlobalClaudeContent(globalDir, instanceRoot); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rulesData, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("reading rules file: %v", err)
	}
	rulesContent := string(rulesData)

	globalDestPath := filepath.Join(instanceRoot, globalClaudeFile)
	wsIdx := strings.Index(rulesContent, "@"+contextPath)
	overlayIdx := strings.Index(rulesContent, "@"+overlayDestPath)
	globalIdx := strings.Index(rulesContent, "@"+globalDestPath)

	if wsIdx < 0 || overlayIdx < 0 || globalIdx < 0 {
		t.Fatalf("missing expected imports in rules file:\n%s", rulesContent)
	}
	if !(wsIdx < overlayIdx && overlayIdx < globalIdx) {
		t.Errorf("import ordering incorrect: workspace=%d, overlay=%d, global=%d\n%s",
			wsIdx, overlayIdx, globalIdx, rulesContent)
	}
}

// TestInstallGlobalClaudeContentOrderingWithoutOverlay verifies that when only
// the workspace-context import is in the rules file, the global import is
// appended after it.
func TestInstallGlobalClaudeContentOrderingWithoutOverlay(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(globalDir, "CLAUDE.global.md"), []byte("# global\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate state after workspace-context install only (no overlay).
	contextPath := filepath.Join(instanceRoot, workspaceContextFile)
	rulesPath := filepath.Join(instanceRoot, workspaceRulesFile)
	if err := writeWorkspaceRulesFile(rulesPath, contextPath); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallGlobalClaudeContent(globalDir, instanceRoot); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rulesData, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("reading rules file: %v", err)
	}
	rulesContent := string(rulesData)

	globalDestPath := filepath.Join(instanceRoot, globalClaudeFile)
	wsIdx := strings.Index(rulesContent, "@"+contextPath)
	globalIdx := strings.Index(rulesContent, "@"+globalDestPath)

	if wsIdx < 0 || globalIdx < 0 {
		t.Fatalf("missing expected imports in rules file:\n%s", rulesContent)
	}
	if wsIdx >= globalIdx {
		t.Errorf("workspace-context import should appear before global import:\n%s", rulesContent)
	}
}

// TestInstallGlobalClaudeContentMigratesOldImport verifies that the old
// relative @CLAUDE.global.md import is removed from CLAUDE.md after install.
func TestInstallGlobalClaudeContentMigratesOldImport(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(globalDir, "CLAUDE.global.md"), []byte("# global\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate old CLAUDE.md with relative global import.
	oldContent := "@workspace-context.md\n\n@CLAUDE.global.md\n\n# Workspace\n"
	claudePath := filepath.Join(instanceRoot, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte(oldContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Also need rules file to exist for appendToWorkspaceRulesFile to work.
	contextPath := filepath.Join(instanceRoot, workspaceContextFile)
	rulesPath := filepath.Join(instanceRoot, workspaceRulesFile)
	if err := writeWorkspaceRulesFile(rulesPath, contextPath); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallGlobalClaudeContent(globalDir, instanceRoot); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	claudeData, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	if strings.Contains(string(claudeData), globalClaudeImport) {
		t.Errorf("old relative global import still present in CLAUDE.md:\n%s", string(claudeData))
	}
}
