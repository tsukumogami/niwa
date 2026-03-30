package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// --- DiscoverHooks tests ---

func TestDiscoverHooks_MissingDir(t *testing.T) {
	dir := t.TempDir()
	hooks, err := DiscoverHooks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hooks) != 0 {
		t.Fatalf("expected empty hooks, got %v", hooks)
	}
}

func TestDiscoverHooks_TopLevelScripts(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, "hooks")
	mustMkdir(t, hooksDir)

	mustWriteFile(t, filepath.Join(hooksDir, "pre_tool_use.sh"), "#!/bin/bash")
	mustWriteFile(t, filepath.Join(hooksDir, "stop.sh"), "#!/bin/bash")

	hooks, err := DiscoverHooks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertHookScripts(t, hooks, "pre_tool_use", []string{filepath.Join(hooksDir, "pre_tool_use.sh")})
	assertHookScripts(t, hooks, "stop", []string{filepath.Join(hooksDir, "stop.sh")})
}

func TestDiscoverHooks_SubdirectoryScripts(t *testing.T) {
	dir := t.TempDir()
	eventDir := filepath.Join(dir, "hooks", "pre_tool_use")
	mustMkdir(t, eventDir)

	mustWriteFile(t, filepath.Join(eventDir, "check_perms.sh"), "#!/bin/bash")
	mustWriteFile(t, filepath.Join(eventDir, "log_usage.sh"), "#!/bin/bash")

	hooks, err := DiscoverHooks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries := hooks["pre_tool_use"]
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(entries), entries)
	}
}

func TestDiscoverHooks_IgnoresNonShFiles(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, "hooks")
	mustMkdir(t, hooksDir)

	mustWriteFile(t, filepath.Join(hooksDir, "stop.sh"), "#!/bin/bash")
	mustWriteFile(t, filepath.Join(hooksDir, "README.md"), "docs")
	mustWriteFile(t, filepath.Join(hooksDir, "config.yaml"), "key: val")

	hooks, err := DiscoverHooks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(hooks) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(hooks), hooks)
	}
	if _, ok := hooks["stop"]; !ok {
		t.Fatal("expected 'stop' event")
	}
}

func TestDiscoverHooks_IgnoresNonShInSubdir(t *testing.T) {
	dir := t.TempDir()
	eventDir := filepath.Join(dir, "hooks", "stop")
	mustMkdir(t, eventDir)

	mustWriteFile(t, filepath.Join(eventDir, "handler.sh"), "#!/bin/bash")
	mustWriteFile(t, filepath.Join(eventDir, "notes.txt"), "ignore me")

	hooks, err := DiscoverHooks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries := hooks["stop"]
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(entries), entries)
	}
}

func TestDiscoverHooks_MixedLayout(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, "hooks")
	mustMkdir(t, hooksDir)

	// Top-level script for one event.
	mustWriteFile(t, filepath.Join(hooksDir, "stop.sh"), "#!/bin/bash")

	// Subdirectory for another event.
	eventDir := filepath.Join(hooksDir, "pre_tool_use")
	mustMkdir(t, eventDir)
	mustWriteFile(t, filepath.Join(eventDir, "a.sh"), "#!/bin/bash")
	mustWriteFile(t, filepath.Join(eventDir, "b.sh"), "#!/bin/bash")

	hooks, err := DiscoverHooks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(hooks) != 2 {
		t.Fatalf("expected 2 events, got %d", len(hooks))
	}
	if len(hooks["stop"]) != 1 {
		t.Fatalf("expected 1 stop entry, got %d", len(hooks["stop"]))
	}
	if len(hooks["pre_tool_use"]) != 2 {
		t.Fatalf("expected 2 pre_tool_use entries, got %d", len(hooks["pre_tool_use"]))
	}
}

func TestDiscoverHooks_EmptyHooksDir(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "hooks"))

	hooks, err := DiscoverHooks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hooks) != 0 {
		t.Fatalf("expected empty hooks, got %v", hooks)
	}
}

// --- DiscoverEnvFiles tests ---

func TestDiscoverEnvFiles_MissingEnvDir(t *testing.T) {
	dir := t.TempDir()
	wsFile, repoFiles, err := DiscoverEnvFiles(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wsFile != "" {
		t.Fatalf("expected empty workspace file, got %q", wsFile)
	}
	if len(repoFiles) != 0 {
		t.Fatalf("expected empty repo files, got %v", repoFiles)
	}
}

func TestDiscoverEnvFiles_WorkspaceEnvOnly(t *testing.T) {
	dir := t.TempDir()
	envDir := filepath.Join(dir, "env")
	mustMkdir(t, envDir)
	mustWriteFile(t, filepath.Join(envDir, "workspace.env"), "FOO=bar")

	wsFile, repoFiles, err := DiscoverEnvFiles(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wsFile != filepath.Join(envDir, "workspace.env") {
		t.Fatalf("expected workspace.env path, got %q", wsFile)
	}
	if len(repoFiles) != 0 {
		t.Fatalf("expected empty repo files, got %v", repoFiles)
	}
}

func TestDiscoverEnvFiles_RepoEnvFiles(t *testing.T) {
	dir := t.TempDir()
	reposDir := filepath.Join(dir, "env", "repos")
	mustMkdir(t, reposDir)

	mustWriteFile(t, filepath.Join(reposDir, "tsuku.env"), "KEY=val")
	mustWriteFile(t, filepath.Join(reposDir, "niwa.env"), "OTHER=val")

	wsFile, repoFiles, err := DiscoverEnvFiles(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wsFile != "" {
		t.Fatalf("expected empty workspace file, got %q", wsFile)
	}
	if len(repoFiles) != 2 {
		t.Fatalf("expected 2 repo files, got %d", len(repoFiles))
	}
	if repoFiles["tsuku"] != filepath.Join(reposDir, "tsuku.env") {
		t.Fatalf("unexpected tsuku path: %q", repoFiles["tsuku"])
	}
	if repoFiles["niwa"] != filepath.Join(reposDir, "niwa.env") {
		t.Fatalf("unexpected niwa path: %q", repoFiles["niwa"])
	}
}

func TestDiscoverEnvFiles_IgnoresNonEnvFiles(t *testing.T) {
	dir := t.TempDir()
	reposDir := filepath.Join(dir, "env", "repos")
	mustMkdir(t, reposDir)

	mustWriteFile(t, filepath.Join(reposDir, "tsuku.env"), "KEY=val")
	mustWriteFile(t, filepath.Join(reposDir, "README.md"), "docs")

	_, repoFiles, err := DiscoverEnvFiles(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repoFiles) != 1 {
		t.Fatalf("expected 1 repo file, got %d: %v", len(repoFiles), repoFiles)
	}
}

func TestDiscoverEnvFiles_IgnoresSubdirectories(t *testing.T) {
	dir := t.TempDir()
	reposDir := filepath.Join(dir, "env", "repos")
	mustMkdir(t, reposDir)

	mustWriteFile(t, filepath.Join(reposDir, "tsuku.env"), "KEY=val")
	mustMkdir(t, filepath.Join(reposDir, "subdir"))

	_, repoFiles, err := DiscoverEnvFiles(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repoFiles) != 1 {
		t.Fatalf("expected 1 repo file, got %d: %v", len(repoFiles), repoFiles)
	}
}

func TestDiscoverEnvFiles_BothWorkspaceAndRepos(t *testing.T) {
	dir := t.TempDir()
	envDir := filepath.Join(dir, "env")
	reposDir := filepath.Join(envDir, "repos")
	mustMkdir(t, reposDir)

	mustWriteFile(t, filepath.Join(envDir, "workspace.env"), "GLOBAL=yes")
	mustWriteFile(t, filepath.Join(reposDir, "myrepo.env"), "LOCAL=yes")

	wsFile, repoFiles, err := DiscoverEnvFiles(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wsFile != filepath.Join(envDir, "workspace.env") {
		t.Fatalf("expected workspace.env path, got %q", wsFile)
	}
	if len(repoFiles) != 1 {
		t.Fatalf("expected 1 repo file, got %d", len(repoFiles))
	}
	if repoFiles["myrepo"] != filepath.Join(reposDir, "myrepo.env") {
		t.Fatalf("unexpected myrepo path: %q", repoFiles["myrepo"])
	}
}

func TestDiscoverEnvFiles_MissingReposDir(t *testing.T) {
	dir := t.TempDir()
	envDir := filepath.Join(dir, "env")
	mustMkdir(t, envDir)
	mustWriteFile(t, filepath.Join(envDir, "workspace.env"), "FOO=bar")

	wsFile, repoFiles, err := DiscoverEnvFiles(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wsFile == "" {
		t.Fatal("expected workspace.env to be found")
	}
	if len(repoFiles) != 0 {
		t.Fatalf("expected empty repo files, got %v", repoFiles)
	}
}

// --- validateWithinDir tests ---

func TestValidateWithinDir_ValidPaths(t *testing.T) {
	dir := t.TempDir()
	if err := validateWithinDir(dir, filepath.Join(dir, "hooks", "stop.sh")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWithinDir_EscapingPath(t *testing.T) {
	dir := t.TempDir()
	err := validateWithinDir(dir, filepath.Join(dir, "..", "etc", "passwd"))
	if err == nil {
		t.Fatal("expected error for escaping path")
	}
}

func TestValidateWithinDir_SameDir(t *testing.T) {
	dir := t.TempDir()
	if err := validateWithinDir(dir, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- helpers ---

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func assertHookScripts(t *testing.T, hooks config.HooksConfig, event string, expected []string) {
	t.Helper()
	entries, ok := hooks[event]
	if !ok {
		t.Fatalf("event %q not found in hooks", event)
	}
	// Collect all scripts from all entries for comparison.
	var got []string
	for _, e := range entries {
		got = append(got, e.Scripts...)
	}
	sort.Strings(got)
	sort.Strings(expected)
	if len(got) != len(expected) {
		t.Fatalf("event %q: expected %d scripts, got %d: %v", event, len(expected), len(got), got)
	}
	for i := range got {
		if got[i] != expected[i] {
			t.Fatalf("event %q script[%d]: expected %q, got %q", event, i, expected[i], got[i])
		}
	}
}
