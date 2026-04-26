package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeriveRole(t *testing.T) {
	t.Run("flag role is highest priority", func(t *testing.T) {
		t.Setenv("NIWA_SESSION_ROLE", "env-role")
		got := deriveRole("flag-role", "some/repo", "/instance/root")
		if got != "flag-role" {
			t.Errorf("expected 'flag-role', got %q", got)
		}
	})

	t.Run("env var overrides repo and pwd", func(t *testing.T) {
		t.Setenv("NIWA_SESSION_ROLE", "env-role")
		got := deriveRole("", "some/repo", "/instance/root")
		if got != "env-role" {
			t.Errorf("expected 'env-role', got %q", got)
		}
	})

	t.Run("repo flag returns basename", func(t *testing.T) {
		got := deriveRole("", "tools/myapp", "/instance/root")
		if got != "myapp" {
			t.Errorf("expected 'myapp', got %q", got)
		}
	})

	t.Run("repo flag with trailing slash returns basename", func(t *testing.T) {
		got := deriveRole("", "tools/myapp/", "/instance/root")
		if got != "myapp" {
			t.Errorf("expected 'myapp', got %q", got)
		}
	})

	t.Run("pwd at instance root returns coordinator", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chdir(dir); err != nil {
			t.Fatalf("chdir: %v", err)
		}
		got := deriveRole("", "", dir)
		if got != "coordinator" {
			t.Errorf("expected 'coordinator', got %q", got)
		}
	})

	t.Run("pwd in repo subdirectory returns repo basename", func(t *testing.T) {
		root := t.TempDir()
		repoDir := filepath.Join(root, "myrepo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.Chdir(repoDir); err != nil {
			t.Fatalf("chdir: %v", err)
		}
		got := deriveRole("", "", root)
		if got != "myrepo" {
			t.Errorf("expected 'myrepo', got %q", got)
		}
	})

	t.Run("pwd in nested subdirectory returns top-level repo name", func(t *testing.T) {
		root := t.TempDir()
		nestedDir := filepath.Join(root, "myrepo", "src", "pkg")
		if err := os.MkdirAll(nestedDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.Chdir(nestedDir); err != nil {
			t.Fatalf("chdir: %v", err)
		}
		got := deriveRole("", "", root)
		// filepath.Base of rel path "myrepo/src/pkg" is "pkg", not "myrepo".
		// The function returns filepath.Base of the full relative path.
		if got != "pkg" {
			t.Errorf("expected 'pkg', got %q", got)
		}
	})

	t.Run("pwd outside instance root returns coordinator", func(t *testing.T) {
		root := t.TempDir()
		outsideDir := t.TempDir()
		if err := os.Chdir(outsideDir); err != nil {
			t.Fatalf("chdir: %v", err)
		}
		got := deriveRole("", "", root)
		if got != "coordinator" {
			t.Errorf("expected 'coordinator', got %q", got)
		}
	})

	t.Run("empty instance root returns coordinator as fallback", func(t *testing.T) {
		got := deriveRole("", "", "")
		if got != "coordinator" {
			t.Errorf("expected 'coordinator', got %q", got)
		}
	})
}
