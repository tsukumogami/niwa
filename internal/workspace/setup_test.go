package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func TestResolveSetupDirDefault(t *testing.T) {
	ws := &config.WorkspaceConfig{}
	if got := ResolveSetupDir(ws, "myrepo"); got != "scripts/setup" {
		t.Errorf("expected default scripts/setup, got %q", got)
	}
}

func TestResolveSetupDirWorkspaceOverride(t *testing.T) {
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:     "test",
			SetupDir: "custom/setup",
		},
	}
	if got := ResolveSetupDir(ws, "myrepo"); got != "custom/setup" {
		t.Errorf("expected custom/setup, got %q", got)
	}
}

func TestResolveSetupDirRepoOverride(t *testing.T) {
	override := "repo-specific/init"
	ws := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:     "test",
			SetupDir: "custom/setup",
		},
		Repos: map[string]config.RepoOverride{
			"myrepo": {SetupDir: &override},
		},
	}
	if got := ResolveSetupDir(ws, "myrepo"); got != "repo-specific/init" {
		t.Errorf("expected repo-specific/init, got %q", got)
	}
}

func TestResolveSetupDirRepoDisable(t *testing.T) {
	empty := ""
	ws := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"myrepo": {SetupDir: &empty},
		},
	}
	if got := ResolveSetupDir(ws, "myrepo"); got != "" {
		t.Errorf("expected empty (disabled), got %q", got)
	}
}

func TestRunSetupScriptsDisabled(t *testing.T) {
	result := RunSetupScripts("/tmp", "")
	if !result.Disabled {
		t.Error("expected disabled")
	}
}

func TestRunSetupScriptsMissingDir(t *testing.T) {
	tmpDir := t.TempDir()
	result := RunSetupScripts(tmpDir, "nonexistent")
	if !result.Skipped {
		t.Error("expected skipped for missing directory")
	}
}

func TestRunSetupScriptsEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "scripts", "setup"), 0o755)
	result := RunSetupScripts(tmpDir, "scripts/setup")
	if !result.Skipped {
		t.Error("expected skipped for empty directory")
	}
}

func TestRunSetupScriptsSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	setupDir := filepath.Join(tmpDir, "scripts", "setup")
	os.MkdirAll(setupDir, 0o755)

	// Create two executable scripts that write marker files.
	script1 := filepath.Join(setupDir, "01-first.sh")
	os.WriteFile(script1, []byte("#!/bin/sh\ntouch \"$PWD/.first-ran\"\n"), 0o755)

	script2 := filepath.Join(setupDir, "02-second.sh")
	os.WriteFile(script2, []byte("#!/bin/sh\ntouch \"$PWD/.second-ran\"\n"), 0o755)

	result := RunSetupScripts(tmpDir, "scripts/setup")

	if len(result.Scripts) != 2 {
		t.Fatalf("expected 2 scripts, got %d", len(result.Scripts))
	}
	if result.Scripts[0].Name != "01-first.sh" {
		t.Errorf("script[0] = %q, want 01-first.sh", result.Scripts[0].Name)
	}
	if result.Scripts[0].Error != nil {
		t.Errorf("script[0] error: %v", result.Scripts[0].Error)
	}
	if result.Scripts[1].Name != "02-second.sh" {
		t.Errorf("script[1] = %q, want 02-second.sh", result.Scripts[1].Name)
	}
	if result.Scripts[1].Error != nil {
		t.Errorf("script[1] error: %v", result.Scripts[1].Error)
	}

	// Verify scripts ran from the repo root (cwd).
	if _, err := os.Stat(filepath.Join(tmpDir, ".first-ran")); err != nil {
		t.Error("first script didn't run (marker file missing)")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, ".second-ran")); err != nil {
		t.Error("second script didn't run (marker file missing)")
	}
}

func TestRunSetupScriptsStopOnError(t *testing.T) {
	tmpDir := t.TempDir()
	setupDir := filepath.Join(tmpDir, "scripts", "setup")
	os.MkdirAll(setupDir, 0o755)

	script1 := filepath.Join(setupDir, "01-fail.sh")
	os.WriteFile(script1, []byte("#!/bin/sh\nexit 1\n"), 0o755)

	script2 := filepath.Join(setupDir, "02-never-runs.sh")
	os.WriteFile(script2, []byte("#!/bin/sh\ntouch \"$PWD/.should-not-exist\"\n"), 0o755)

	result := RunSetupScripts(tmpDir, "scripts/setup")

	if len(result.Scripts) != 1 {
		t.Fatalf("expected 1 script result (stopped on error), got %d", len(result.Scripts))
	}
	if result.Scripts[0].Error == nil {
		t.Error("expected error for failing script")
	}

	if _, err := os.Stat(filepath.Join(tmpDir, ".should-not-exist")); err == nil {
		t.Error("second script should not have run after first failed")
	}
}

func TestRunSetupScriptsNonExecutableWarning(t *testing.T) {
	tmpDir := t.TempDir()
	setupDir := filepath.Join(tmpDir, "scripts", "setup")
	os.MkdirAll(setupDir, 0o755)

	// Non-executable file.
	script1 := filepath.Join(setupDir, "01-noexec.sh")
	os.WriteFile(script1, []byte("#!/bin/sh\necho hello\n"), 0o644)

	// Executable file after it.
	script2 := filepath.Join(setupDir, "02-runs.sh")
	os.WriteFile(script2, []byte("#!/bin/sh\ntouch \"$PWD/.runs-after-noexec\"\n"), 0o755)

	result := RunSetupScripts(tmpDir, "scripts/setup")

	if len(result.Scripts) != 2 {
		t.Fatalf("expected 2 script results, got %d", len(result.Scripts))
	}
	if result.Scripts[0].Error == nil {
		t.Error("expected warning for non-executable script")
	}
	if result.Scripts[1].Error != nil {
		t.Errorf("second script should succeed: %v", result.Scripts[1].Error)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, ".runs-after-noexec")); err != nil {
		t.Error("executable script should still run after non-executable is skipped")
	}
}

func TestRunSetupScriptsLexicalOrder(t *testing.T) {
	tmpDir := t.TempDir()
	setupDir := filepath.Join(tmpDir, "scripts", "setup")
	os.MkdirAll(setupDir, 0o755)

	// Create scripts in reverse order to verify lexical sorting.
	os.WriteFile(filepath.Join(setupDir, "10-last.sh"), []byte("#!/bin/sh\ntrue\n"), 0o755)
	os.WriteFile(filepath.Join(setupDir, "01-first.sh"), []byte("#!/bin/sh\ntrue\n"), 0o755)
	os.WriteFile(filepath.Join(setupDir, "05-middle.sh"), []byte("#!/bin/sh\ntrue\n"), 0o755)

	result := RunSetupScripts(tmpDir, "scripts/setup")

	if len(result.Scripts) != 3 {
		t.Fatalf("expected 3 scripts, got %d", len(result.Scripts))
	}
	expected := []string{"01-first.sh", "05-middle.sh", "10-last.sh"}
	for i, want := range expected {
		if result.Scripts[i].Name != want {
			t.Errorf("script[%d] = %q, want %q", i, result.Scripts[i].Name, want)
		}
	}
}
