package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
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

// TestShellInitZsh_CompdefDeferGuard verifies the zsh integration ships the
// deferral guard that re-registers completion when the script is sourced before
// compinit has run (the default macOS case, where the env file is sourced from
// ~/.zshenv, before ~/.zshrc's compinit). Without it, cobra's `compdef` call
// no-ops and tab-completion silently never activates.
func TestShellInitZsh_CompdefDeferGuard(t *testing.T) {
	var out bytes.Buffer
	shellInitZshCmd.SetOut(&out)

	if err := shellInitZshCmd.RunE(shellInitZshCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()
	for _, want := range []string{
		"$+functions[compdef]",                  // guards on compdef availability
		"__niwa_register_completion",            // the deferred registration function
		"compdef _niwa niwa",                    // performs the registration
		"precmd_functions+=",                    // queues the one-shot precmd hook
		"unfunction __niwa_register_completion", // hook cleans itself up
	} {
		if !strings.Contains(output, want) {
			t.Errorf("zsh output missing defer-guard fragment %q", want)
		}
	}

	// The guard must come after the completion function is defined so the
	// deferred compdef refers to an existing _niwa.
	if i, j := strings.Index(output, "_niwa()"), strings.Index(output, "__niwa_register_completion"); i == -1 || j == -1 || j < i {
		t.Errorf("defer guard must appear after the _niwa() definition (i=%d, j=%d)", i, j)
	}

	// Cobra's own top-level registration must be guarded so it does not error
	// with "command not found: compdef" when sourced before compinit.
	if strings.Contains(output, "\ncompdef _niwa niwa\n") {
		t.Error("cobra's unconditional `compdef _niwa niwa` should be guarded, not emitted bare")
	}
	if !strings.Contains(output, "(( $+functions[compdef] )) && compdef _niwa niwa") {
		t.Error("zsh output missing guarded top-level compdef registration")
	}
}

func TestGuardZshCompdef(t *testing.T) {
	// The bare cobra line is replaced exactly once.
	in := "#compdef niwa\ncompdef _niwa niwa\n\n_niwa() { : }\n"
	got := guardZshCompdef(in)
	if strings.Contains(got, "\ncompdef _niwa niwa\n") {
		t.Errorf("bare compdef line still present:\n%s", got)
	}
	if !strings.Contains(got, "(( $+functions[compdef] )) && compdef _niwa niwa") {
		t.Errorf("guarded compdef line missing:\n%s", got)
	}

	// Unknown format is returned unchanged (graceful fallback).
	unknown := "something else entirely\n"
	if guardZshCompdef(unknown) != unknown {
		t.Error("guardZshCompdef should return unmatched input unchanged")
	}
}

// resolvedTempDir returns a temp directory with symlinks resolved.
//
// t.TempDir can hand back a path that traverses a symlink (/tmp -> /private/tmp
// on macOS), and bash reports the resolved form in $PWD. Comparing a resolved
// cwd against an unresolved path silently inverts the negative assertions: a
// wrapper that wrongly cd'd would report the resolved path, fail to string-match
// the unresolved one, and the test would pass while reading "did not navigate".
// Every path compared against a wrapper's cwd must come from here.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolving temp dir %q: %v", dir, err)
	}
	return resolved
}

// runWrapperWithStubNiwa sources the wrapper in a real bash shell against a
// stub `niwa` on PATH, invokes `niwa <args...>`, and reports the shell's
// working directory afterwards along with the wrapper's exit code.
//
// The stub writes landingPath to $NIWA_RESPONSE_FILE when that variable is set
// (mimicking writeLandingPath) and exits with exitCode. Running the wrapper for
// real is the point: a missing arm in the case dispatcher is invisible to any
// assertion made against the template as a string, because every token a
// substring check looks for also occurs inside some other arm.
func runWrapperWithStubNiwa(t *testing.T, landingPath string, exitCode int, args ...string) (cwd string, rc int, startDir string) {
	t.Helper()

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	stubDir := t.TempDir()
	stub := fmt.Sprintf(`#!/bin/sh
if [ -n "$NIWA_RESPONSE_FILE" ]; then
    printf '%%s\n' %q > "$NIWA_RESPONSE_FILE"
fi
exit %d
`, landingPath, exitCode)
	stubPath := filepath.Join(stubDir, "niwa")
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		t.Fatalf("writing stub niwa: %v", err)
	}

	startDir = resolvedTempDir(t)

	// Print the cwd and the wrapper's exit code on separate lines so the test
	// can assert on both. `command niwa` inside the wrapper resolves through
	// PATH to the stub.
	script := shellWrapperTemplate + `
niwa "$@"
__rc=$?
printf 'CWD=%s\n' "$PWD"
printf 'RC=%s\n' "$__rc"
`
	cmd := exec.Command(bash, append([]string{"-c", script, "bash"}, args...)...)
	cmd.Dir = startDir
	cmd.Env = append(os.Environ(), "PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running wrapper: %v\noutput:\n%s", err, out)
	}

	for line := range strings.SplitSeq(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "CWD="):
			cwd = strings.TrimPrefix(line, "CWD=")
		case strings.HasPrefix(line, "RC="):
			if _, err := fmt.Sscanf(strings.TrimPrefix(line, "RC="), "%d", &rc); err != nil {
				t.Fatalf("parsing exit code from %q: %v", line, err)
			}
		}
	}
	if cwd == "" {
		t.Fatalf("wrapper produced no CWD line; output:\n%s", out)
	}

	// t.TempDir can hand back a path under a symlink (/tmp -> /private/tmp on
	// macOS); bash reports the resolved form in $PWD, so compare resolved.
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	return cwd, rc, startDir
}

// TestShellWrapper_CdEligibleCommands runs the wrapper against a stub niwa and
// asserts the shell actually changes directory for every cd-eligible command,
// and does not for anything else.
//
// This replaces a test that asserted strings.Contains(shellWrapperTemplate,
// "create"). That check passed while `niwa worktree create` was broken (issue
// #281), because "create" also occurs inside the nested `session create` arm --
// it would have passed against a template with no case statement at all. A
// substring assertion cannot detect a missing case arm, which is precisely the
// defect class here, so the wrapper is exercised rather than pattern-matched.
func TestShellWrapper_CdEligibleCommands(t *testing.T) {
	landing := resolvedTempDir(t)

	cdEligible := [][]string{
		{"create"},
		{"destroy"},
		{"go"},
		{"init"},
		// The canonical spelling, and the one docs/guides/worktree.md
		// documents. This is the regression issue #281 reported.
		{"worktree", "create"},
		// The deprecated alias must keep working.
		{"session", "create"},
	}
	for _, args := range cdEligible {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			cwd, rc, _ := runWrapperWithStubNiwa(t, landing, 0, args...)
			if cwd != landing {
				t.Errorf("niwa %s did not cd: cwd = %q, want %q",
					strings.Join(args, " "), cwd, landing)
			}
			if rc != 0 {
				t.Errorf("niwa %s: exit code = %d, want 0", strings.Join(args, " "), rc)
			}
		})
	}
}

// TestShellWrapper_NonCdCommandsDoNotNavigate verifies commands with no landing
// path run unwrapped and leave the shell where it was. Subcommands under
// `worktree` other than `create` write no landing path, so they must fall
// through to the default arm rather than being swept in by the group.
func TestShellWrapper_NonCdCommandsDoNotNavigate(t *testing.T) {
	landing := resolvedTempDir(t)

	for _, args := range [][]string{
		{"worktree", "list"},
		{"worktree", "destroy", "ab12cd34"},
		{"session", "list"},
		{"apply"},
		{"version"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			// The stub still offers a valid landing path. A command that is
			// not cd-eligible never sets NIWA_RESPONSE_FILE, so the path is
			// never written and the shell must stay put.
			cwd, rc, startDir := runWrapperWithStubNiwa(t, landing, 0, args...)
			// Assert where the shell IS, not merely where it isn't: a bare
			// `cwd != landing` would also be satisfied by the shell ending up
			// somewhere else entirely.
			if cwd != startDir {
				t.Errorf("niwa %s moved the shell to %q; it is not cd-eligible and must stay at %q",
					strings.Join(args, " "), cwd, startDir)
			}
			if rc != 0 {
				t.Errorf("niwa %s: exit code = %d, want 0", strings.Join(args, " "), rc)
			}
		})
	}
}

// TestShellWrapper_NoCdOnFailure pins the negative direction of the exit-code
// gate: a non-zero exit must leave the shell where it was, even when the
// response file holds a perfectly good path.
//
// This is load-bearing beyond the wrapper itself. The failure posture argued in
// docs/designs/current/DESIGN-post-clone-scripts.md (that a failed setup script
// is carried as data rather than surfaced as a non-zero exit) rests on this
// gate: a command that exits non-zero must not strand the operator outside the
// directory they need to enter in order to fix what failed. Issue #280 depends
// on it. Do not relax this without revisiting that design.
func TestShellWrapper_NoCdOnFailure(t *testing.T) {
	landing := resolvedTempDir(t)

	for _, args := range [][]string{
		{"worktree", "create"},
		{"session", "create"},
		{"create"},
		{"go"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			cwd, rc, startDir := runWrapperWithStubNiwa(t, landing, 3, args...)
			if cwd != startDir {
				t.Errorf("niwa %s cd'd to %q on a non-zero exit; the shell must stay at %q",
					strings.Join(args, " "), cwd, startDir)
			}
			if rc != 3 {
				t.Errorf("niwa %s: exit code = %d, want 3 (wrapper must propagate it)",
					strings.Join(args, " "), rc)
			}
		})
	}
}

// TestShellWrapperTemplate_AllCdPathsRouteThroughWrap keeps the structural
// invariant the behavioral tests above rely on: every cd happens inside
// __niwa_cd_wrap, so the exit-code gate cannot be bypassed by a case arm that
// calls builtin cd directly.
func TestShellWrapperTemplate_AllCdPathsRouteThroughWrap(t *testing.T) {
	if !strings.Contains(shellWrapperTemplate, `__niwa_cd_wrap "$@"`) {
		t.Error("wrapper template missing __niwa_cd_wrap dispatch")
	}
	if got := strings.Count(shellWrapperTemplate, "builtin cd"); got != 1 {
		t.Errorf("wrapper has %d `builtin cd` calls, want exactly 1 (inside __niwa_cd_wrap); "+
			"a cd outside the wrapper would skip the exit-code gate", got)
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

func TestShellInitInstall_AddsSourceLineToZshrc(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Default macOS zsh layout: only .zshrc exists.
	zshrc := filepath.Join(tmpDir, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("# existing config\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := shellInitInstallCmd
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `. "$HOME/.niwa/env"`) {
		t.Error("source line not added to .zshrc")
	}
	if !strings.Contains(stderr.String(), "Added source line to") {
		t.Error("expected 'Added source line' message in stderr")
	}
}

func TestShellInitInstall_CreatesZshrcWhenNoRcFiles_ZshShell(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SHELL", "/bin/zsh")

	cmd := shellInitInstallCmd
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	zshrc := filepath.Join(tmpDir, ".zshrc")
	data, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatalf(".zshrc not created: %v", err)
	}
	if !strings.Contains(string(data), `. "$HOME/.niwa/env"`) {
		t.Error("source line not present in created .zshrc")
	}
	if !strings.Contains(stderr.String(), "Created "+zshrc) {
		t.Errorf("expected 'Created %s' in stderr; got: %s", zshrc, stderr.String())
	}
}

func TestShellInitInstall_CreatesBashrcWhenNoRcFiles_BashShell(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SHELL", "/bin/bash")

	cmd := shellInitInstallCmd
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bashrc := filepath.Join(tmpDir, ".bashrc")
	data, err := os.ReadFile(bashrc)
	if err != nil {
		t.Fatalf(".bashrc not created: %v", err)
	}
	if !strings.Contains(string(data), `. "$HOME/.niwa/env"`) {
		t.Error("source line not present in created .bashrc")
	}
	if !strings.Contains(stderr.String(), "Created "+bashrc) {
		t.Errorf("expected 'Created %s' in stderr; got: %s", bashrc, stderr.String())
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
