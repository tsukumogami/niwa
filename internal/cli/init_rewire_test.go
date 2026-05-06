package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// chdirAndXDG sets up a temp cwd plus an isolated XDG_CONFIG_HOME for
// tests that exercise the global registry. Returns the temp dir.
func chdirAndXDG(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	return dir
}

// captureStderr runs fn with the init command's stderr captured to a
// buffer. Returns the captured bytes regardless of success/failure.
func captureStderr(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	initCmd.SetErr(&buf)
	defer initCmd.SetErr(os.Stderr)
	err := fn()
	return buf.String(), err
}

// AC-1, AC-2: Named mode creates `<cwd>/<name>/.niwa/workspace.toml`.
func TestRunInit_Named_CreatesDirectory(t *testing.T) {
	dir := chdirAndXDG(t)
	if err := executeInit(t, "my-ws"); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	configPath := filepath.Join(dir, "my-ws", workspace.StateDir, workspace.WorkspaceConfigFile)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected workspace.toml at %s: %v", configPath, err)
	}
}

// AC-9: Target is a regular file → ErrTargetDirExists with qualifier "file".
func TestRunInit_Named_TargetIsFile_Errors(t *testing.T) {
	dir := chdirAndXDG(t)
	target := filepath.Join(dir, "my-ws")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := executeInit(t, "my-ws")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists (file)") {
		t.Errorf("error %q missing qualifier 'file'", err.Error())
	}
	// The target is a file; we can't os.Stat under it, but we can confirm
	// the file's contents were not modified — init refused before any write.
	body, readErr := os.ReadFile(filepath.Join(dir, "my-ws"))
	if readErr != nil {
		t.Fatalf("reading target file: %v", readErr)
	}
	if string(body) != "hello" {
		t.Errorf("target file modified: got %q, want %q", body, "hello")
	}
}

// AC-10: Target is an unrelated directory → ErrTargetDirExists with qualifier "directory".
func TestRunInit_Named_TargetIsDirectory_Errors(t *testing.T) {
	dir := chdirAndXDG(t)
	target := filepath.Join(dir, "my-ws")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	err := executeInit(t, "my-ws")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists (directory)") {
		t.Errorf("error %q missing qualifier 'directory'", err.Error())
	}
	if _, statErr := os.Stat(filepath.Join(target, workspace.StateDir)); !os.IsNotExist(statErr) {
		t.Errorf("my-ws/.niwa exists after failed init; should not")
	}
}

// AC-11: Target is a symlink → ErrTargetDirExists with qualifier "symlink".
func TestRunInit_Named_TargetIsSymlink_Errors(t *testing.T) {
	dir := chdirAndXDG(t)
	target := filepath.Join(dir, "my-ws")
	dst := filepath.Join(dir, "elsewhere")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dst, target); err != nil {
		t.Fatal(err)
	}
	err := executeInit(t, "my-ws")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists (symlink)") {
		t.Errorf("error %q missing qualifier 'symlink'", err.Error())
	}
}

// AC-12: <cwd>/<name>/.niwa/workspace.toml exists → ErrWorkspaceExists.
func TestRunInit_Named_TargetIsNiwaWorkspace_RoutesToErrWorkspaceExists(t *testing.T) {
	dir := chdirAndXDG(t)
	niwa := filepath.Join(dir, "my-ws", workspace.StateDir)
	if err := os.MkdirAll(niwa, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwa, workspace.WorkspaceConfigFile), []byte("[workspace]\nname=\"existing\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := executeInit(t, "my-ws")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Use niwa apply") {
		t.Errorf("error %q does not contain ErrWorkspaceExists suggestion", err.Error())
	}
	if strings.Contains(err.Error(), "(directory)") || strings.Contains(err.Error(), "(file)") {
		t.Errorf("error %q is the generic ErrTargetDirExists; expected ErrWorkspaceExists routing", err.Error())
	}
}

// AC-13: <cwd>/<name>/.niwa/ exists without workspace.toml → ErrNiwaDirectoryExists.
func TestRunInit_Named_TargetIsOrphanNiwaDir_RoutesToErrNiwaDirectoryExists(t *testing.T) {
	dir := chdirAndXDG(t)
	niwa := filepath.Join(dir, "my-ws", workspace.StateDir)
	if err := os.MkdirAll(niwa, 0o755); err != nil {
		t.Fatal(err)
	}
	err := executeInit(t, "my-ws")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Remove the") || !strings.Contains(err.Error(), ".niwa") {
		t.Errorf("error %q does not contain ErrNiwaDirectoryExists suggestion", err.Error())
	}
}

// AC-14 .. AC-18: Name validation invariants. We assert that init exits
// non-zero and the error message both quotes the input (or refuses to,
// for the "."/".." literals which would produce nonsensical paths if
// joined into cwd) and references the allowed character set. We do not
// assert filesystem state for the path-traversal literals because
// filepath.Join cleans them into the cwd / cwd's parent, both of which
// exist by construction; the workspace package's
// TestValidateInitName_RejectsPathTraversal already locks the
// validation contract at the helper level.
func TestRunInit_NameValidation(t *testing.T) {
	cases := []struct {
		label    string
		name     string
		fsCheck  bool // assert the cwd/<name> path was not created
		mustHave string
	}{
		{"whitespace", "foo bar", true, "alphanumerics"},
		{"slash", "foo/bar", true, "alphanumerics"},
		{"dotdot", "..", false, "path-traversal"},
		{"dot", ".", false, "path-traversal"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			dir := chdirAndXDG(t)
			err := executeInit(t, tc.name)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.mustHave) {
				t.Errorf("error %q missing %q", err.Error(), tc.mustHave)
			}
			if tc.fsCheck {
				if _, statErr := os.Stat(filepath.Join(dir, tc.name)); !os.IsNotExist(statErr) {
					t.Errorf("path created at %q after validation failure", tc.name)
				}
			}
		})
	}
}

// AC-18: `niwa init ""` MUST fail with the empty-name validation error.
// The runInit gate fires on len(args) >= 1 specifically so an explicit
// empty positional doesn't fall through to no-args mode.
func TestRunInit_NameValidation_EmptyArg(t *testing.T) {
	dir := chdirAndXDG(t)
	err := executeInit(t, "")
	if err == nil {
		t.Fatal("expected validation error for empty name; got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error %q does not state name cannot be empty", err.Error())
	}
	// No workspace.toml created at cwd.
	if _, statErr := os.Stat(filepath.Join(dir, workspace.StateDir, workspace.WorkspaceConfigFile)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("workspace created in cwd despite validation failure")
	}
}

// AC-11 reinforcement: a symlink whose target itself is a niwa workspace
// must still surface ErrTargetDirExists with qualifier "symlink", not the
// more specific ErrWorkspaceExists. R6 sub-case routing runs only for
// non-symlink paths.
func TestRunInit_Named_SymlinkToNiwaWorkspace_StillSymlinkError(t *testing.T) {
	dir := chdirAndXDG(t)
	// Build a real niwa workspace at <dir>/real, then symlink <dir>/my-ws -> real.
	real := filepath.Join(dir, "real")
	realNiwa := filepath.Join(real, workspace.StateDir)
	if err := os.MkdirAll(realNiwa, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realNiwa, workspace.WorkspaceConfigFile), []byte("[workspace]\nname=\"real\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "my-ws")); err != nil {
		t.Fatal(err)
	}
	err := executeInit(t, "my-ws")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists (symlink)") {
		t.Errorf("error %q missing 'symlink' qualifier (AC-11)", err.Error())
	}
	if strings.Contains(err.Error(), "Use niwa apply") {
		t.Errorf("error %q routed to ErrWorkspaceExists; should be ErrTargetDirExists for symlink", err.Error())
	}
}

// AC-19: Registry collision without --rebind → ErrRegistryNameInUse with the
// existing Root in Detail and both --rebind + global config path in Suggestion.
func TestRunInit_RegistryCollision_NoRebind_Errors(t *testing.T) {
	dir := chdirAndXDG(t)
	otherRoot := filepath.Join(dir, "other")
	if err := os.MkdirAll(otherRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatal(err)
	}
	globalCfg.SetRegistryEntry("my-team", config.RegistryEntry{Root: otherRoot})
	if err := config.SaveGlobalConfig(globalCfg); err != nil {
		t.Fatal(err)
	}

	err = executeInit(t, "my-team")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, otherRoot) {
		t.Errorf("error %q does not mention existing Root %q", msg, otherRoot)
	}
	if !strings.Contains(msg, "--rebind") {
		t.Errorf("error %q does not mention --rebind", msg)
	}
	if !strings.Contains(msg, "config.toml") {
		t.Errorf("error %q does not mention global config TOML path", msg)
	}
	// Registry entry's Root must still be /path/A (otherRoot).
	globalCfg2, _ := config.LoadGlobalConfig()
	entry := globalCfg2.LookupWorkspace("my-team")
	if entry == nil || entry.Root != otherRoot {
		t.Errorf("registry Root changed without --rebind: got %v", entry)
	}
	// No filesystem writes at or below <cwd>/my-team.
	if _, statErr := os.Stat(filepath.Join(dir, "my-team")); !os.IsNotExist(statErr) {
		t.Errorf("<cwd>/my-team created when init refused")
	}
}

// AC-19a, AC-20: Registry collision with --rebind succeeds and emits the
// prominent stderr warning naming both Roots.
func TestRunInit_RegistryCollision_WithRebind_Succeeds(t *testing.T) {
	dir := chdirAndXDG(t)
	otherRoot := filepath.Join(dir, "other")
	if err := os.MkdirAll(otherRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatal(err)
	}
	globalCfg.SetRegistryEntry("my-team", config.RegistryEntry{Root: otherRoot})
	if err := config.SaveGlobalConfig(globalCfg); err != nil {
		t.Fatal(err)
	}

	stderr, err := captureStderr(t, func() error {
		return executeInit(t, "my-team", "--rebind")
	})
	if err != nil {
		t.Fatalf("init --rebind failed: %v", err)
	}
	// New directory created.
	configPath := filepath.Join(dir, "my-team", workspace.StateDir, workspace.WorkspaceConfigFile)
	if _, statErr := os.Stat(configPath); statErr != nil {
		t.Errorf("expected workspace.toml at %s: %v", configPath, statErr)
	}
	// Registry Root updated.
	globalCfg2, _ := config.LoadGlobalConfig()
	entry := globalCfg2.LookupWorkspace("my-team")
	if entry == nil {
		t.Fatal("registry entry missing after rebind")
	}
	wantRoot := filepath.Join(dir, "my-team")
	if entry.Root != wantRoot {
		t.Errorf("expected Root %q, got %q", wantRoot, entry.Root)
	}
	// Old directory left intact.
	if _, statErr := os.Stat(otherRoot); statErr != nil {
		t.Errorf("previous root %q removed after rebind", otherRoot)
	}
	// AC-20: warning includes both Roots.
	if !strings.Contains(stderr, "WARNING:") {
		t.Errorf("stderr missing WARNING prefix: %q", stderr)
	}
	if !strings.Contains(stderr, otherRoot) {
		t.Errorf("stderr missing previous Root %q: %q", otherRoot, stderr)
	}
	if !strings.Contains(stderr, wantRoot) {
		t.Errorf("stderr missing new Root %q: %q", wantRoot, stderr)
	}
}

// AC-20b: Target-exists wins over registry collision regardless of --rebind.
func TestRunInit_RegistryCollision_TargetExists_TargetExistsWins(t *testing.T) {
	dir := chdirAndXDG(t)
	otherRoot := filepath.Join(dir, "other")
	if err := os.MkdirAll(otherRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "my-team")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatal(err)
	}
	globalCfg.SetRegistryEntry("my-team", config.RegistryEntry{Root: otherRoot})
	if err := config.SaveGlobalConfig(globalCfg); err != nil {
		t.Fatal(err)
	}

	for _, useRebind := range []bool{false, true} {
		t.Run(fmt.Sprintf("rebind=%v", useRebind), func(t *testing.T) {
			args := []string{"my-team"}
			if useRebind {
				args = append(args, "--rebind")
			}
			err := executeInit(t, args...)
			if err == nil {
				t.Fatal("expected target-exists error, got nil")
			}
			if !strings.Contains(err.Error(), "already exists (directory)") {
				t.Errorf("error %q is not target-exists (R5 should win over R8)", err.Error())
			}
			// Registry must not have been rebound.
			globalCfg2, _ := config.LoadGlobalConfig()
			entry := globalCfg2.LookupWorkspace("my-team")
			if entry == nil || entry.Root != otherRoot {
				t.Errorf("registry rebound when target-exists should win: got %v", entry)
			}
		})
	}
}

// R4 / AC-26: success message includes the resolved absolute path.
func TestRunInit_SuccessMessage_IncludesAbsolutePath(t *testing.T) {
	dir := chdirAndXDG(t)
	var stdout bytes.Buffer
	initCmd.SetOut(&stdout)
	defer initCmd.SetOut(os.Stdout)
	if err := executeInit(t, "my-ws"); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	wantPath := filepath.Join(dir, "my-ws")
	resolved, _ := filepath.EvalSymlinks(wantPath)
	if resolved == "" {
		resolved = wantPath
	}
	if !strings.Contains(stdout.String(), resolved) {
		t.Errorf("success stdout %q does not contain workspace path %q", stdout.String(), resolved)
	}
}

// Internal invariant: ConfigNameOverride is populated in init state when a
// positional name is given, regardless of whether it differs from the
// scaffolded config's [workspace] name.
func TestRunInit_PopulatesConfigNameOverride(t *testing.T) {
	dir := chdirAndXDG(t)
	if err := executeInit(t, "my-ws"); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	state, err := workspace.LoadState(filepath.Join(dir, "my-ws"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.ConfigNameOverride != "my-ws" {
		t.Errorf("ConfigNameOverride: got %q, want %q", state.ConfigNameOverride, "my-ws")
	}
}

// Internal invariant: no state file in no-args mode (no flags trigger a
// write, and there's no positional name to record an override for).
func TestRunInit_NoArgs_NoStateFile(t *testing.T) {
	dir := chdirAndXDG(t)
	if err := executeInit(t); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	statePath := filepath.Join(dir, workspace.StateDir, workspace.StateFile)
	if _, statErr := os.Stat(statePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("expected no state file in no-args scaffold; stat returned %v", statErr)
	}
}

// R11: help text describes new behavior.
func TestRunInit_LongHelpText_DescribesNewBehavior(t *testing.T) {
	long := initCmd.Long
	wantSubstrings := []string{
		"<cwd>/<name>",
		"overrides",
		"--rebind",
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(long, s) {
			t.Errorf("Long help text missing %q", s)
		}
	}
}

// R4: override note emitted on stderr in named scaffold mode when the
// explicit name differs from the scaffolded `[workspace] name`. Note
// that scaffold writes `name = name` so the names always match in the
// scaffold path — we exercise the suppression case here. The
// emission case (names differ) only fires in clone mode, which is
// covered end-to-end by Issue 4's @critical Gherkin scenario where a
// `localGitServer` fixture provides an upstream config with a
// different name.
func TestRunInit_OverrideNote_NotEmittedWhenNamesMatch(t *testing.T) {
	chdirAndXDG(t)
	stderr, err := captureStderr(t, func() error {
		return executeInit(t, "my-ws")
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if strings.Contains(stderr, "overrides") {
		t.Errorf("expected no override note when names match; got: %q", stderr)
	}
}

// pathTypeQualifier helper unit test. Symlink case verified via the
// Lstat-driven AC tests above; keep this focused on directory and file.
func TestPathTypeQualifier(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		want string
	}{
		{dir, "directory"},
		{file, "file"},
	}
	for _, tc := range cases {
		info, err := os.Lstat(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := pathTypeQualifier(info); got != tc.want {
			t.Errorf("pathTypeQualifier(%s) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
