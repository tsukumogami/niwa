package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureRestoreNotice(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := restoreNoticeOut
	restoreNoticeOut = &buf
	t.Cleanup(func() { restoreNoticeOut = old })
	return &buf
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDiscover_RestoresConfigDirLeftAsPrev: a refresh killed between the
// swap's two renames leaves <root>/.niwa.prev and no <root>/.niwa. Every
// command starts with Discover, so Discover is where the workspace has to come
// back; otherwise every command reports there is no workspace.
func TestDiscover_RestoresConfigDirLeftAsPrev(t *testing.T) {
	buf := captureRestoreNotice(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".niwa.prev", ConfigFile), "name = ws")
	writeFile(t, filepath.Join(root, ".niwa.prev", "notes.md"), "only copy")
	sub := filepath.Join(root, "inst-1")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	configPath, configDir, err := Discover(sub)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if configDir != filepath.Join(root, ConfigDir) || configPath != filepath.Join(configDir, ConfigFile) {
		t.Errorf("Discover = %s, %s", configPath, configDir)
	}
	if got, err := os.ReadFile(filepath.Join(configDir, "notes.md")); err != nil || string(got) != "only copy" {
		t.Errorf("restored notes.md = %q, %v", got, err)
	}
	if _, err := os.Lstat(configDir + PrevSuffix); !os.IsNotExist(err) {
		t.Errorf(".niwa.prev should be gone, got %v", err)
	}
	if !strings.Contains(buf.String(), "restored "+configDir) {
		t.Errorf("expected a notice, got %q", buf.String())
	}
}

// TestDiscover_FailedRestoreIsAnError: when .niwa.prev can't be put back,
// Discover says so instead of walking on, which would bind an enclosing
// workspace in place of this one.
func TestDiscover_FailedRestoreIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root renames inside a read-only directory")
	}
	captureRestoreNotice(t)
	outer := t.TempDir()
	writeFile(t, filepath.Join(outer, ".niwa", ConfigFile), "name = outer")
	inner := filepath.Join(outer, "inner")
	writeFile(t, filepath.Join(inner, ".niwa.prev", ConfigFile), "name = inner")
	if err := os.Chmod(inner, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(inner, 0o755) })

	_, configDir, err := Discover(inner)
	if err == nil {
		t.Fatalf("expected an error, got the workspace at %s", configDir)
	}
	if !strings.Contains(err.Error(), "could not be put back") || !strings.Contains(err.Error(), inner) {
		t.Errorf("error should name the directory it could not restore: %v", err)
	}
}

// TestDiscover_IgnoresPrevWithoutWorkspaceConfig: a .niwa.prev that holds no
// workspace.toml is not a workspace's config snapshot, so Discover leaves it
// alone and keeps walking.
func TestDiscover_IgnoresPrevWithoutWorkspaceConfig(t *testing.T) {
	buf := captureRestoreNotice(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".niwa.prev", "other.toml"), "x")

	if _, _, err := Discover(root); err == nil {
		t.Fatal("expected no workspace")
	}
	if _, err := os.Lstat(filepath.Join(root, ".niwa.prev", "other.toml")); err != nil {
		t.Errorf(".niwa.prev should be untouched: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("unexpected notice %q", buf.String())
	}
}

func TestRestoreInterruptedSwap(t *testing.T) {
	t.Run("restores a missing target", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "cfg")
		writeFile(t, filepath.Join(dir+PrevSuffix, "f"), "old")
		restored, err := RestoreInterruptedSwap(dir)
		if err != nil || !restored {
			t.Fatalf("restored=%v err=%v", restored, err)
		}
		if got, err := os.ReadFile(filepath.Join(dir, "f")); err != nil || string(got) != "old" {
			t.Errorf("f = %q, %v", got, err)
		}
	})
	t.Run("leaves an existing target and its leftover prev", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "cfg")
		writeFile(t, filepath.Join(dir, "f"), "new")
		writeFile(t, filepath.Join(dir+PrevSuffix, "f"), "old")
		restored, err := RestoreInterruptedSwap(dir)
		if err != nil || restored {
			t.Fatalf("restored=%v err=%v", restored, err)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, "f")); string(got) != "new" {
			t.Errorf("target changed: %q", got)
		}
		if _, err := os.Lstat(dir + PrevSuffix); err != nil {
			t.Errorf("prev should be left for the swap to clear: %v", err)
		}
	})
	t.Run("does not move a symlinked prev", func(t *testing.T) {
		base := t.TempDir()
		elsewhere := filepath.Join(base, "elsewhere")
		writeFile(t, filepath.Join(elsewhere, "f"), "x")
		dir := filepath.Join(base, "cfg")
		if err := os.Symlink(elsewhere, dir+PrevSuffix); err != nil {
			t.Fatal(err)
		}
		restored, err := RestoreInterruptedSwap(dir)
		if err != nil || restored {
			t.Fatalf("restored=%v err=%v", restored, err)
		}
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Errorf("target should still be missing: %v", err)
		}
	})
	t.Run("nothing to restore", func(t *testing.T) {
		restored, err := RestoreInterruptedSwap(filepath.Join(t.TempDir(), "cfg"))
		if err != nil || restored {
			t.Fatalf("restored=%v err=%v", restored, err)
		}
	})
}
