package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallOverlayClaudeContentPresent verifies that CLAUDE.overlay.md is
// copied to the instance root and @CLAUDE.overlay.md is injected into CLAUDE.md
// after @workspace-context.md.
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

	// Write a CLAUDE.md with @workspace-context.md already present.
	claudeContent := "@workspace-context.md\n\n# Workspace\n"
	if err := os.WriteFile(filepath.Join(instanceRoot, "CLAUDE.md"), []byte(claudeContent), 0o644); err != nil {
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

	// Verify the import was injected.
	claudeData, err := os.ReadFile(filepath.Join(instanceRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	claudeMd := string(claudeData)
	if !strings.Contains(claudeMd, "@CLAUDE.overlay.md") {
		t.Errorf("@CLAUDE.overlay.md import not injected: %s", claudeMd)
	}
	// Verify ordering: @workspace-context.md comes before @CLAUDE.overlay.md.
	wsIdx := strings.Index(claudeMd, "@workspace-context.md")
	overlayIdx := strings.Index(claudeMd, "@CLAUDE.overlay.md")
	if wsIdx >= overlayIdx {
		t.Errorf("@workspace-context.md should appear before @CLAUDE.overlay.md in CLAUDE.md:\n%s", claudeMd)
	}
}

// TestInstallOverlayClaudeContentAbsent verifies that when CLAUDE.overlay.md
// does not exist in the overlay clone, the function returns ("", nil) and
// CLAUDE.md is not modified.
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

	claudeContent := "@workspace-context.md\n\n# Workspace\n"
	if err := os.WriteFile(filepath.Join(instanceRoot, "CLAUDE.md"), []byte(claudeContent), 0o644); err != nil {
		t.Fatal(err)
	}

	path, err := InstallOverlayClaudeContent(overlayDir, instanceRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path when CLAUDE.overlay.md is absent, got %q", path)
	}

	// CLAUDE.md should be unchanged.
	data, err := os.ReadFile(filepath.Join(instanceRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	if string(data) != claudeContent {
		t.Errorf("CLAUDE.md modified unexpectedly: got %q", string(data))
	}

	// CLAUDE.overlay.md should not exist.
	if _, err := os.Stat(filepath.Join(instanceRoot, "CLAUDE.overlay.md")); err == nil {
		t.Error("CLAUDE.overlay.md should not exist when absent from overlay")
	}
}

// TestInstallOverlayClaudeContentImportOrdering verifies that when both
// @workspace-context.md and @CLAUDE.global.md are present in CLAUDE.md,
// @CLAUDE.overlay.md is injected between them.
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

	// Simulate CLAUDE.md after both workspace-context and global imports have been added.
	claudeContent := "@workspace-context.md\n\n@CLAUDE.global.md\n\n# Workspace content\n"
	if err := os.WriteFile(filepath.Join(instanceRoot, "CLAUDE.md"), []byte(claudeContent), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := InstallOverlayClaudeContent(overlayDir, instanceRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(instanceRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	claudeMd := string(data)

	wsIdx := strings.Index(claudeMd, "@workspace-context.md")
	overlayIdx := strings.Index(claudeMd, "@CLAUDE.overlay.md")
	globalIdx := strings.Index(claudeMd, "@CLAUDE.global.md")

	if wsIdx < 0 || overlayIdx < 0 || globalIdx < 0 {
		t.Fatalf("missing expected imports in CLAUDE.md:\n%s", claudeMd)
	}
	if !(wsIdx < overlayIdx && overlayIdx < globalIdx) {
		t.Errorf("import ordering incorrect: @workspace-context.md=%d, @CLAUDE.overlay.md=%d, @CLAUDE.global.md=%d\n%s",
			wsIdx, overlayIdx, globalIdx, claudeMd)
	}
}

// TestInstallOverlayClaudeContentIdempotent verifies that calling
// InstallOverlayClaudeContent twice does not duplicate the import.
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
	if err := os.WriteFile(filepath.Join(instanceRoot, "CLAUDE.md"), []byte("@workspace-context.md\n\n# Workspace\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallOverlayClaudeContent(overlayDir, instanceRoot); err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if _, err := InstallOverlayClaudeContent(overlayDir, instanceRoot); err != nil {
		t.Fatalf("second call error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(instanceRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	count := strings.Count(string(data), "@CLAUDE.overlay.md")
	if count != 1 {
		t.Errorf("@CLAUDE.overlay.md appears %d times, want 1:\n%s", count, string(data))
	}
}

// TestInstallGlobalClaudeContentOrderingWithOverlay verifies the three-way
// import ordering on first apply when overlay is active: workspace-context
// is injected first, then overlay inserts after it, then global inserts after
// overlay — producing @workspace-context.md → @CLAUDE.overlay.md → @CLAUDE.global.md.
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

	// Simulate state after workspace-context and overlay have been injected
	// (as produced by InstallWorkspaceContext then InstallOverlayClaudeContent
	// on a fresh apply).
	claudeContent := "@workspace-context.md\n\n@CLAUDE.overlay.md\n\n# Workspace\n"
	if err := os.WriteFile(filepath.Join(instanceRoot, "CLAUDE.md"), []byte(claudeContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallGlobalClaudeContent(globalDir, instanceRoot); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(instanceRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	claudeMd := string(data)

	wsIdx := strings.Index(claudeMd, "@workspace-context.md")
	overlayIdx := strings.Index(claudeMd, "@CLAUDE.overlay.md")
	globalIdx := strings.Index(claudeMd, "@CLAUDE.global.md")

	if wsIdx < 0 || overlayIdx < 0 || globalIdx < 0 {
		t.Fatalf("missing expected imports in CLAUDE.md:\n%s", claudeMd)
	}
	if !(wsIdx < overlayIdx && overlayIdx < globalIdx) {
		t.Errorf("import ordering incorrect: @workspace-context.md=%d, @CLAUDE.overlay.md=%d, @CLAUDE.global.md=%d\n%s",
			wsIdx, overlayIdx, globalIdx, claudeMd)
	}
}

// TestInstallGlobalClaudeContentOrderingWithoutOverlay verifies that when no
// overlay is active, global is inserted after @workspace-context.md (not before).
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

	// Simulate state after workspace-context has been injected (no overlay).
	claudeContent := "@workspace-context.md\n\n# Workspace\n"
	if err := os.WriteFile(filepath.Join(instanceRoot, "CLAUDE.md"), []byte(claudeContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallGlobalClaudeContent(globalDir, instanceRoot); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(instanceRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	claudeMd := string(data)

	wsIdx := strings.Index(claudeMd, "@workspace-context.md")
	globalIdx := strings.Index(claudeMd, "@CLAUDE.global.md")

	if wsIdx < 0 || globalIdx < 0 {
		t.Fatalf("missing expected imports in CLAUDE.md:\n%s", claudeMd)
	}
	if wsIdx >= globalIdx {
		t.Errorf("@workspace-context.md should appear before @CLAUDE.global.md in CLAUDE.md:\n%s", claudeMd)
	}
}

// TestEnsureImportAfterInCLAUDENoAnchor verifies that when the anchor line is
// absent, the import is prepended.
func TestEnsureImportAfterInCLAUDENoAnchor(t *testing.T) {
	tmpDir := t.TempDir()
	claudePath := filepath.Join(tmpDir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte("# content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ensureImportAfterInCLAUDE(claudePath, "@new-import.md", "@missing-anchor.md"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "@new-import.md") {
		t.Errorf("import not prepended: %s", string(data))
	}
}

// TestEnsureImportAfterInCLAUDENoCLAUDE verifies that when CLAUDE.md does not
// exist, the function returns nil without creating the file.
func TestEnsureImportAfterInCLAUDENoCLAUDE(t *testing.T) {
	tmpDir := t.TempDir()
	claudePath := filepath.Join(tmpDir, "NONEXISTENT.md")

	if err := ensureImportAfterInCLAUDE(claudePath, "@foo.md", "@anchor.md"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(claudePath); err == nil {
		t.Error("file should not have been created")
	}
}
