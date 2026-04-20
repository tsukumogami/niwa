package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// setupTestWorkspace creates a temp workspace with config and an instance
// containing repos under group directories.
func setupTestWorkspace(t *testing.T) (workspaceRoot, instanceRoot string) {
	t.Helper()
	root := t.TempDir()

	// Create workspace config.
	niwaDir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte("[workspace]\nname = \"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create instance with repos.
	instance := filepath.Join(root, "test")
	if err := os.MkdirAll(filepath.Join(instance, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instance, ".niwa", "instance.json"), []byte(`{"schema_version":1,"instance_name":"test"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create group/repo dirs.
	for _, p := range []string{
		filepath.Join(instance, "public", "niwa"),
		filepath.Join(instance, "public", "tsuku"),
		filepath.Join(instance, "private", "tools"),
	} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	return root, instance
}

// setupGlobalConfig writes a global config.toml under a temp XDG_CONFIG_HOME.
func setupGlobalConfig(t *testing.T, content string) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configDir := filepath.Join(tmpDir, "niwa")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runGoTest calls runGo with the given flags/args and returns stdout, stderr, error.
// It resets the package-level flag vars and constructs a fresh cobra.Command.
func runGoTest(t *testing.T, args []string, wsFlag, repoFlag string) (stdout, stderr string, err error) {
	t.Helper()

	// Set package-level flag vars directly.
	goWorkspace = wsFlag
	goRepo = repoFlag
	t.Cleanup(func() {
		goWorkspace = ""
		goRepo = ""
	})

	// Suppress shell-init hint.
	t.Setenv("_NIWA_SHELL_INIT", "1")

	// Wire a temp response file so writeLandingPath has somewhere to write.
	// Read it back and return as stdout so callers don't need to change.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	responseFile := filepath.Join(tmp, "niwa-response")
	withResponseFile(t, responseFile)

	cmd := &cobra.Command{}
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	err = runGo(cmd, args)
	if err == nil {
		if data, readErr := os.ReadFile(responseFile); readErr == nil {
			outBuf.WriteString(strings.TrimRight(string(data), "\n"))
		}
	}
	return outBuf.String(), errBuf.String(), err
}

func TestGoNoArgs_InsideWorkspace(t *testing.T) {
	wsRoot, _ := setupTestWorkspace(t)
	setupGlobalConfig(t, "")

	origDir, _ := os.Getwd()
	if err := os.Chdir(filepath.Join(wsRoot, "test")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	stdout, stderr, err := runGoTest(t, nil, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := strings.TrimSpace(stdout)
	if got != wsRoot {
		t.Errorf("expected %q, got %q", wsRoot, got)
	}
	if !strings.Contains(stderr, "workspace root") {
		t.Errorf("expected stderr to mention 'workspace root', got: %q", stderr)
	}
}

func TestGoNoArgs_OutsideWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	setupGlobalConfig(t, "")

	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	_, _, err := runGoTest(t, nil, "", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not inside a workspace") {
		t.Errorf("expected 'not inside a workspace' in error, got: %v", err)
	}
}

func TestGoSingleArg_RepoInInstance(t *testing.T) {
	_, instance := setupTestWorkspace(t)
	setupGlobalConfig(t, "")

	origDir, _ := os.Getwd()
	if err := os.Chdir(instance); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	stdout, stderr, err := runGoTest(t, []string{"niwa"}, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := strings.TrimSpace(stdout)
	want := filepath.Join(instance, "public", "niwa")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
	if !strings.Contains(stderr, "repo \"niwa\"") {
		t.Errorf("expected stderr to mention repo, got: %q", stderr)
	}
}

func TestGoSingleArg_WorkspaceInRegistry(t *testing.T) {
	tmpDir := t.TempDir()
	wsRoot := filepath.Join(tmpDir, "myworkspace")
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	setupGlobalConfig(t, "[registry.myws]\nsource = \"/dev/null\"\nroot = \""+wsRoot+"\"\n")

	// Run from a directory outside any workspace/instance.
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	if err := os.Chdir(outsideDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	stdout, stderr, err := runGoTest(t, []string{"myws"}, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := strings.TrimSpace(stdout)
	if got != wsRoot {
		t.Errorf("expected %q, got %q", wsRoot, got)
	}
	if !strings.Contains(stderr, "workspace \"myws\"") {
		t.Errorf("expected stderr to mention workspace, got: %q", stderr)
	}
}

func TestGoSingleArg_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	setupGlobalConfig(t, "")

	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	_, _, err := runGoTest(t, []string{"../etc"}, "", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid target") {
		t.Errorf("expected 'invalid target' in error, got: %v", err)
	}

	_, _, err = runGoTest(t, []string{"foo/bar"}, "", "")
	if err == nil {
		t.Fatal("expected error for slash, got nil")
	}
	if !strings.Contains(err.Error(), "invalid target") {
		t.Errorf("expected 'invalid target' in error, got: %v", err)
	}
}

func TestGoWorkspaceFlag(t *testing.T) {
	tmpDir := t.TempDir()
	wsRoot := filepath.Join(tmpDir, "myworkspace")
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	setupGlobalConfig(t, "[registry.myws]\nsource = \"/dev/null\"\nroot = \""+wsRoot+"\"\n")

	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	stdout, _, err := runGoTest(t, nil, "myws", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := strings.TrimSpace(stdout)
	if got != wsRoot {
		t.Errorf("expected %q, got %q", wsRoot, got)
	}
}

func TestGoRepoFlag_InsideInstance(t *testing.T) {
	_, instance := setupTestWorkspace(t)
	setupGlobalConfig(t, "")

	origDir, _ := os.Getwd()
	if err := os.Chdir(instance); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	stdout, stderr, err := runGoTest(t, nil, "", "tools")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := strings.TrimSpace(stdout)
	want := filepath.Join(instance, "private", "tools")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
	if !strings.Contains(stderr, "repo \"tools\"") {
		t.Errorf("expected stderr to mention repo, got: %q", stderr)
	}
}

func TestGoRepoFlag_OutsideInstance(t *testing.T) {
	tmpDir := t.TempDir()
	setupGlobalConfig(t, "")

	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	_, _, err := runGoTest(t, nil, "", "niwa")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "-r requires being inside a workspace instance") {
		t.Errorf("expected '-r requires' in error, got: %v", err)
	}
}

func TestGoFlagConflict_PositionalPlusW(t *testing.T) {
	setupGlobalConfig(t, "")

	_, _, err := runGoTest(t, []string{"bar"}, "foo", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "cannot combine positional argument with -w flag") {
		t.Errorf("expected conflict error, got: %v", err)
	}
}

func TestGoFlagConflict_PositionalPlusR(t *testing.T) {
	setupGlobalConfig(t, "")

	_, _, err := runGoTest(t, []string{"bar"}, "", "foo")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "cannot combine positional argument with -r flag") {
		t.Errorf("expected conflict error, got: %v", err)
	}
}

func TestGoWorkspaceAndRepoFlags(t *testing.T) {
	wsRoot, _ := setupTestWorkspace(t)
	setupGlobalConfig(t, "[registry.test]\nsource = \"/dev/null\"\nroot = \""+wsRoot+"\"\n")

	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	stdout, stderr, err := runGoTest(t, nil, "test", "tsuku")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := strings.TrimSpace(stdout)
	want := filepath.Join(wsRoot, "test", "public", "tsuku")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
	if !strings.Contains(stderr, "repo \"tsuku\"") {
		t.Errorf("expected stderr to mention repo, got: %q", stderr)
	}
}

func TestGoSingleArg_BothRepoAndWorkspace(t *testing.T) {
	wsRoot, instance := setupTestWorkspace(t)
	// Register a workspace with the same name as a repo ("tsuku").
	otherRoot := filepath.Join(t.TempDir(), "other")
	if err := os.MkdirAll(otherRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = wsRoot
	setupGlobalConfig(t, "[registry.tsuku]\nsource = \"/dev/null\"\nroot = \""+otherRoot+"\"\n")

	origDir, _ := os.Getwd()
	if err := os.Chdir(instance); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	stdout, stderr, err := runGoTest(t, []string{"tsuku"}, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should prefer repo over workspace.
	got := strings.TrimSpace(stdout)
	want := filepath.Join(instance, "public", "tsuku")
	if got != want {
		t.Errorf("expected repo path %q, got %q", want, got)
	}
	if !strings.Contains(stderr, "also a workspace") {
		t.Errorf("expected 'also a workspace' hint in stderr, got: %q", stderr)
	}
}

func TestGoSingleArg_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	setupGlobalConfig(t, "")

	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	_, _, err := runGoTest(t, []string{"nonexistent"}, "", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}
