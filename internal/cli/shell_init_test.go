package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellInitBash_ValidSyntax(t *testing.T) {
	var out bytes.Buffer
	shellInitBashCmd.SetOut(&out)

	if err := shellInitBashCmd.RunE(shellInitBashCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()
	for _, want := range []string{
		"_NIWA_SHELL_INIT=1",
		"niwa()",
		"create|go)",
		"command niwa",
		"mktemp",
		`NIWA_RESPONSE_FILE="$__niwa_tmp"`,
		"builtin cd",
		"rm -f",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("bash output missing %q", want)
		}
	}
}

func TestShellInitZsh_ValidSyntax(t *testing.T) {
	var out bytes.Buffer
	shellInitZshCmd.SetOut(&out)

	if err := shellInitZshCmd.RunE(shellInitZshCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()
	for _, want := range []string{
		"_NIWA_SHELL_INIT=1",
		"niwa()",
		"create|go)",
		"command niwa",
		"mktemp",
		`NIWA_RESPONSE_FILE="$__niwa_tmp"`,
		"builtin cd",
		"rm -f",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("zsh output missing %q", want)
		}
	}
}

// TestShellWrapperTemplate_ProtocolStructure verifies the wrapper implements
// the temp-file protocol described in the design doc: mktemp with fallback,
// NIWA_RESPONSE_FILE export, exit-code preservation, and file cleanup.
func TestShellWrapperTemplate_ProtocolStructure(t *testing.T) {
	tmpl := shellWrapperTemplate

	// mktemp failure falls back to running niwa without navigation (not a hard error).
	if !strings.Contains(tmpl, `__niwa_tmp=$(mktemp) || { command niwa "$@"; return $?; }`) {
		t.Error("wrapper missing mktemp-failure fallback that still runs niwa and preserves exit code")
	}

	// NIWA_RESPONSE_FILE is scoped to the niwa invocation (prefix assignment),
	// so it is not inherited by later shell functions.
	if !strings.Contains(tmpl, `NIWA_RESPONSE_FILE="$__niwa_tmp" command niwa "$@"`) {
		t.Error("wrapper missing NIWA_RESPONSE_FILE export scoped to the niwa invocation")
	}

	// Exit code from niwa must be captured before cleanup so cat/rm don't overwrite $?.
	rcIdx := strings.Index(tmpl, "__niwa_rc=$?")
	catIdx := strings.Index(tmpl, `__niwa_dir=$(cat "$__niwa_tmp"`)
	if rcIdx == -1 || catIdx == -1 || rcIdx > catIdx {
		t.Error("wrapper must capture $? into __niwa_rc before reading the temp file")
	}

	// Temp file must be removed after reading.
	if !strings.Contains(tmpl, `rm -f "$__niwa_tmp"`) {
		t.Error("wrapper must remove the temp file after reading it")
	}

	// Final return preserves the niwa exit code.
	if !strings.Contains(tmpl, "return $__niwa_rc") {
		t.Error("wrapper must return niwa's exit code")
	}

	// Non-cd commands delegate directly without any wrapping.
	// The default branch must be a bare `command niwa "$@"` with no temp-file setup.
	defaultBranchStart := strings.Index(tmpl, "*)")
	if defaultBranchStart == -1 {
		t.Fatal("wrapper missing default case branch")
	}
	defaultBranch := tmpl[defaultBranchStart:]
	esacIdx := strings.Index(defaultBranch, "esac")
	if esacIdx == -1 {
		t.Fatal("wrapper default branch not terminated by esac")
	}
	defaultBranch = defaultBranch[:esacIdx]
	if strings.Contains(defaultBranch, "mktemp") || strings.Contains(defaultBranch, "NIWA_RESPONSE_FILE") {
		t.Errorf("default branch should delegate without wrapping, got:\n%s", defaultBranch)
	}
	if !strings.Contains(defaultBranch, `command niwa "$@"`) {
		t.Errorf("default branch must delegate to `command niwa \"$@\"`, got:\n%s", defaultBranch)
	}
}

func TestShellInitAuto_DetectsBash(t *testing.T) {
	t.Setenv("BASH_VERSION", "5.1.0")
	t.Setenv("ZSH_VERSION", "")

	var out bytes.Buffer
	shellInitAutoCmd.SetOut(&out)

	if err := shellInitAutoCmd.RunE(shellInitAutoCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "_NIWA_SHELL_INIT=1") {
		t.Error("auto with BASH_VERSION should produce bash output")
	}
}

func TestShellInitAuto_DetectsZsh(t *testing.T) {
	t.Setenv("ZSH_VERSION", "5.9")
	t.Setenv("BASH_VERSION", "")

	var out bytes.Buffer
	shellInitAutoCmd.SetOut(&out)

	if err := shellInitAutoCmd.RunE(shellInitAutoCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "_NIWA_SHELL_INIT=1") {
		t.Error("auto with ZSH_VERSION should produce zsh output")
	}
}

func TestShellInitAuto_UnknownShell(t *testing.T) {
	t.Setenv("BASH_VERSION", "")
	t.Setenv("ZSH_VERSION", "")

	var out bytes.Buffer
	shellInitAutoCmd.SetOut(&out)

	if err := shellInitAutoCmd.RunE(shellInitAutoCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.Len() != 0 {
		t.Errorf("expected empty output for unknown shell, got %q", out.String())
	}
}

func TestShellInitInstall_CreatesEnvFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := shellInitInstallCmd
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envFile := filepath.Join(tmpDir, ".niwa", "env")
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("env file not created: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, `export PATH="$HOME/.niwa/bin:$PATH"`) {
		t.Error("env file missing PATH export")
	}
	if !strings.Contains(content, "niwa shell-init auto") {
		t.Error("env file missing delegation block")
	}
}

func TestShellInitInstall_AddsSourceLine(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create a .bashrc so it gets detected
	bashrc := filepath.Join(tmpDir, ".bashrc")
	if err := os.WriteFile(bashrc, []byte("# existing config\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := shellInitInstallCmd
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(bashrc)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), `. "$HOME/.niwa/env"`) {
		t.Error("source line not added to .bashrc")
	}
	if !strings.Contains(stderr.String(), "Added source line to") {
		t.Error("expected 'Added source line' message in stderr")
	}
}

func TestShellInitInstall_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	bashrc := filepath.Join(tmpDir, ".bashrc")
	if err := os.WriteFile(bashrc, []byte("# existing config\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := shellInitInstallCmd
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	// Run install twice
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("first install error: %v", err)
	}

	stderr.Reset()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("second install error: %v", err)
	}

	data, err := os.ReadFile(bashrc)
	if err != nil {
		t.Fatal(err)
	}

	sourceLine := `. "$HOME/.niwa/env"`
	count := strings.Count(string(data), sourceLine)
	if count != 1 {
		t.Errorf("expected source line exactly once, found %d times", count)
	}
	if !strings.Contains(stderr.String(), "already present") {
		t.Error("expected 'already present' message on second install")
	}
}

func TestShellInitUninstall_RemovesDelegation(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Set up an env file with delegation
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(niwaDir, "env")
	if err := os.WriteFile(envFile, []byte(EnvFileWithDelegation()), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := shellInitUninstallCmd
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if strings.Contains(content, "niwa shell-init auto") {
		t.Error("env file should not contain delegation block after uninstall")
	}
	if !strings.Contains(content, `export PATH="$HOME/.niwa/bin:$PATH"`) {
		t.Error("env file should still contain PATH export after uninstall")
	}
}

func TestShellInitUninstall_NoEnvFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := shellInitUninstallCmd
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(stderr.String(), "not installed") {
		t.Error("expected 'not installed' message when env file is missing")
	}
}

func TestShellInitStatus_WrapperLoaded(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("_NIWA_SHELL_INIT", "1")

	cmd := shellInitStatusCmd
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stderr.String()
	if !strings.Contains(output, "loaded in current shell") {
		t.Error("expected 'loaded in current shell' when _NIWA_SHELL_INIT is set")
	}
}

func TestShellInitStatus_NotLoaded(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("_NIWA_SHELL_INIT", "")

	cmd := shellInitStatusCmd
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stderr.String()
	if !strings.Contains(output, "not loaded in current shell") {
		t.Error("expected 'not loaded in current shell' when _NIWA_SHELL_INIT is unset")
	}
	if !strings.Contains(output, "not found") {
		t.Error("expected 'not found' for missing env file")
	}
}
