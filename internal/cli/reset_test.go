package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResetCmd_HasForceFlag(t *testing.T) {
	flag := resetCmd.Flags().Lookup("force")
	if flag == nil {
		t.Fatal("expected --force flag to be registered")
	}
	if flag.DefValue != "false" {
		t.Errorf("expected default %q, got %q", "false", flag.DefValue)
	}
}

func TestResetCmd_AcceptsOptionalPositionalArg(t *testing.T) {
	if err := resetCmd.Args(resetCmd, []string{}); err != nil {
		t.Errorf("should accept zero args: %v", err)
	}
	if err := resetCmd.Args(resetCmd, []string{"my-instance"}); err != nil {
		t.Errorf("should accept one arg: %v", err)
	}
	if err := resetCmd.Args(resetCmd, []string{"a", "b"}); err == nil {
		t.Error("should reject two args")
	}
}

func TestIsClonedConfig_WithGitDir(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if !isClonedConfig(dir) {
		t.Error("expected cloned config when .git directory exists")
	}
}

func TestIsClonedConfig_WithoutGitDir(t *testing.T) {
	dir := t.TempDir()

	if isClonedConfig(dir) {
		t.Error("expected non-cloned config when no .git directory exists")
	}
}

func TestIsClonedConfig_NonexistentDir(t *testing.T) {
	if isClonedConfig("/nonexistent/path") {
		t.Error("expected non-cloned config for nonexistent path")
	}
}
