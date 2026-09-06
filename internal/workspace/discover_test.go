package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// --- DiscoverWorktreeHooks tests ---

func TestDiscoverWorktreeHooks_MissingDir(t *testing.T) {
	dir := t.TempDir()
	hooks, err := DiscoverWorktreeHooks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hooks) != 0 {
		t.Fatalf("expected empty hooks, got %v", hooks)
	}
}

// TestDiscoverWorktreeHooks_TopLevelAndSubdir pins both accepted layouts for an
// event niwa actually consumes: worktree-hooks/{event}.sh and
// worktree-hooks/{event}/*.sh.
//
// It used to use "create" as its per-event-directory example and assert that
// discovery returned that directory's two scripts. Nothing consumed "create",
// so the suite certified the silent no-op rather than catching it. The layout
// assertion is the part worth keeping, so it moved to an event that runs.
func TestDiscoverWorktreeHooks_TopLevelAndSubdir(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, "worktree-hooks")
	mustMkdir(t, hooksDir)

	eventDir := filepath.Join(hooksDir, worktreeApplyEvent)
	mustMkdir(t, eventDir)
	mustWriteFile(t, filepath.Join(eventDir, "a.sh"), "#!/bin/sh")
	mustWriteFile(t, filepath.Join(eventDir, "b.sh"), "#!/bin/sh")
	mustWriteFile(t, filepath.Join(eventDir, "notes.txt"), "ignored")

	hooks, err := DiscoverWorktreeHooks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertHookScripts(t, hooks, worktreeApplyEvent, []string{
		filepath.Join(eventDir, "a.sh"),
		filepath.Join(eventDir, "b.sh"),
	})
}

// TestDiscoverWorktreeHooks_UnknownEventReported is the assertion the old
// version of the test above should have carried: a hook registered under an
// event niwa does not consume is REPORTED, not silently indexed.
func TestDiscoverWorktreeHooks_UnknownEventReported(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, "worktree-hooks")
	mustMkdir(t, hooksDir)

	eventDir := filepath.Join(hooksDir, "create")
	mustMkdir(t, eventDir)
	mustWriteFile(t, filepath.Join(eventDir, "a.sh"), "#!/bin/sh")

	_, err := DiscoverWorktreeHooks(dir)
	if err == nil {
		t.Fatal("expected an error for a worktree-hooks/create/ directory, got nil")
	}
	if !errors.Is(err, ErrUnknownWorktreeHookEvent) {
		t.Fatalf("error does not wrap ErrUnknownWorktreeHookEvent: %v", err)
	}
	if !strings.Contains(err.Error(), eventDir) {
		t.Errorf("diagnostic does not name the offending path %q: %v", eventDir, err)
	}
	if !strings.Contains(err.Error(), worktreeApplyEvent) {
		t.Errorf("diagnostic does not name the valid event set: %v", err)
	}
}

// TestDiscoverWorktreeHooks_TypoInFilenameReported covers the other shape the
// originating issue named: a typo in a top-level script filename, which is the
// same silent no-op as a wrong directory name and is caught the same way.
func TestDiscoverWorktreeHooks_TypoInFilenameReported(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, "worktree-hooks")
	mustMkdir(t, hooksDir)
	typo := filepath.Join(hooksDir, "aply.sh")
	mustWriteFile(t, typo, "#!/bin/sh")

	_, err := DiscoverWorktreeHooks(dir)
	if !errors.Is(err, ErrUnknownWorktreeHookEvent) {
		t.Fatalf("expected ErrUnknownWorktreeHookEvent for %q, got %v", typo, err)
	}
	if !strings.Contains(err.Error(), typo) {
		t.Errorf("diagnostic does not name the offending path %q: %v", typo, err)
	}
}

// TestDiscoverWorktreeHooks_UnknownEventDoesNotDiscardValidHooks is the
// regression this validation could most easily have introduced.
//
// Every other error path in DiscoverWorktreeHooks returns a nil map. Reporting
// an unknown event the same way would mean one stale worktree-hooks/create/
// directory silently disables a live worktree-hooks/apply/ one -- a
// configuration that works today, broken by the change meant to stop hooks
// failing silently.
func TestDiscoverWorktreeHooks_UnknownEventDoesNotDiscardValidHooks(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, "worktree-hooks")
	mustMkdir(t, hooksDir)

	stale := filepath.Join(hooksDir, "create")
	mustMkdir(t, stale)
	mustWriteFile(t, filepath.Join(stale, "bootstrap.sh"), "#!/bin/sh")

	live := filepath.Join(hooksDir, worktreeApplyEvent+".sh")
	mustWriteFile(t, live, "#!/bin/sh")

	hooks, err := DiscoverWorktreeHooks(dir)
	if !errors.Is(err, ErrUnknownWorktreeHookEvent) {
		t.Fatalf("expected the unknown-event diagnostic, got %v", err)
	}
	assertHookScripts(t, hooks, worktreeApplyEvent, []string{live})
}

// TestDiscoverWorktreeHooks_ContainmentErrorReturnsAlone holds the line the
// non-fatal downgrade depends on.
//
// errors.Is matches a sentinel anywhere inside a joined error, so a walk that
// collected an unknown-event diagnostic AND a containment failure and returned
// them together would let the symlink escape ride inside the case
// runWorktreeHooks treats as non-fatal -- disabling the containment control
// through the very mechanism added to make this design safe.
//
// The fixture therefore holds BOTH faults. A fixture with only the fatal one
// does not discriminate: the joining implementation returns a non-sentinel
// error there and looks correct.
//
// The fatal fault used here is an unreadable event subdirectory rather than a
// symlink escape. That is deliberate, and the reason is worth recording:
// validateWithinDir is a LEXICAL check (filepath.Abs, Clean, prefix compare --
// it never resolves symlinks), and every path it guards in this walk is built
// with filepath.Join from a bare os.ReadDir entry name, which cannot escape.
// So all three of its calls here are unreachable, and the doc comment's claim
// that scripts are "validated to stay within configDir (no symlink escape)" does
// not hold. That gap is pre-existing and out of scope for this change; the
// defensive calls are left in place, and this test pins the property they exist
// to protect using the fatal path that IS reachable.
func TestDiscoverWorktreeHooks_FatalErrorReturnsAlone(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: an unreadable directory is still readable")
	}
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, "worktree-hooks")
	mustMkdir(t, hooksDir)

	// Fault 1: an unknown event, which the walk collects and would report
	// through the sentinel.
	stale := filepath.Join(hooksDir, "create")
	mustMkdir(t, stale)
	mustWriteFile(t, filepath.Join(stale, "bootstrap.sh"), "#!/bin/sh")

	// Fault 2: a consumed event whose directory cannot be read. Sorted after
	// "create", so the walk meets it with a diagnostic already collected --
	// which is the ordering that makes joining tempting.
	applyDir := filepath.Join(hooksDir, worktreeApplyEvent)
	mustMkdir(t, applyDir)
	if err := os.Chmod(applyDir, 0o000); err != nil {
		t.Skipf("cannot make directory unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(applyDir, 0o755) })

	_, err := DiscoverWorktreeHooks(dir)
	if err == nil {
		t.Fatal("expected the unreadable-directory failure to be reported")
	}
	if errors.Is(err, ErrUnknownWorktreeHookEvent) {
		t.Fatalf("a fatal error was returned joined with the unknown-event sentinel, "+
			"so runWorktreeHooks would downgrade it to a warning: %v", err)
	}
}

// TestApplyToWorktree_UnknownEventIsNonFatal pins the downgrade at the level it
// actually matters: a stale worktree-hooks/create/ directory must not fail
// ApplyToWorktree, because on the delegated WorktreeCreate path a failed
// content install runs a guarded teardown that deletes the worktree.
func TestApplyToWorktree_UnknownEventIsNonFatal(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	stale := filepath.Join(configDir, "worktree-hooks", "create")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "bootstrap.sh"), []byte("#!/bin/sh\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stderr strings.Builder
	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app",
		"purpose", "branch", WorktreeApplyOptions{Stderr: &stderr}); err != nil {
		t.Fatalf("a stale worktree-hooks/create/ directory must not fail ApplyToWorktree: %v", err)
	}
	if !strings.Contains(stderr.String(), "create") {
		t.Errorf("expected a warning naming the unknown event, got: %q", stderr.String())
	}
}

// TestApplyToWorktree_FatalDiscoveryErrorStillFails is the other half, and it is
// the one the isolated discovery test cannot cover: the downgrade lives in
// runWorktreeHooks, so only a test through ApplyToWorktree can catch a
// downgrade written as "discovery returned an error" rather than as a match on
// the one sentinel.
//
// Without this, an implementation that warns on EVERY discovery failure passes
// the whole rest of this file while silently swallowing unreadable-directory
// and containment failures.
func TestApplyToWorktree_FatalDiscoveryErrorStillFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: an unreadable directory is still readable")
	}
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	applyDir := filepath.Join(configDir, "worktree-hooks", worktreeApplyEvent)
	if err := os.MkdirAll(applyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(applyDir, 0o000); err != nil {
		t.Skipf("cannot make directory unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(applyDir, 0o755) })

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app",
		"purpose", "branch", WorktreeApplyOptions{}); err == nil {
		t.Fatal("a fatal discovery error must still fail ApplyToWorktree; " +
			"the downgrade is scoped to ErrUnknownWorktreeHookEvent alone")
	}
}

// TestWorktreeHookEvents_ConsumedFromTheSet is the both-sides check for the
// event vocabulary: it adds an event to the set and asserts the runner executes
// its scripts WITHOUT any edit to runWorktreeHooks.
//
// Without this, an implementation that validates against the set while the
// runner still reads worktreeApplyEvent passes every other assertion in this
// file -- and adding an event would make it valid and still never run, which is
// the original bug with a validation step in front of it.
func TestWorktreeHookEvents_ConsumedFromTheSet(t *testing.T) {
	original := worktreeHookEvents
	worktreeHookEvents = append(append([]string{}, original...), "destroy")
	t.Cleanup(func() { worktreeHookEvents = original })

	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	hooksDir := filepath.Join(configDir, "worktree-hooks", "destroy")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf ran > \"$NIWA_WORKTREE_PATH/destroy-ran.txt\"\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "a.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app",
		"purpose", "branch", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	if _, err := os.Stat(filepath.Join(worktreePath, "destroy-ran.txt")); err != nil {
		t.Fatalf("a script registered under a newly-added event did not run, so the "+
			"runner is not reading worktreeHookEvents: %v", err)
	}
}

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
