package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// writeFailingRepoSetup drops a setup script into a repo's scripts/setup/ and
// COMMITS it.
//
// The commit is load-bearing, not tidiness. `git worktree add` materializes
// tracked files only, so an uncommitted script exists in the clone and not in
// the worktree -- RunSetupScripts then finds no setup directory there and
// reports Skipped, and a test written without the commit passes for the wrong
// reason: nothing ran, so nothing failed.
//
// This is a real property of the feature rather than a fixture quirk: a repo's
// setup scripts reach its worktrees only once they are committed. The published
// contract says so.
func writeFailingRepoSetup(t *testing.T, dir, body string) {
	t.Helper()
	setupDir := filepath.Join(dir, "scripts", "setup")
	if err := os.MkdirAll(setupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(setupDir, "01-fails.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", dir, "add", "-A"},
		{"-C", dir, "-c", "user.email=test@test.com", "-c", "user.name=Test", "commit", "-m", "add setup script"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestReportWorktreeSetup_NamesTheWorktreeAndScript is the attribution
// requirement at the level a user reads it.
//
// "setup failed for app" is not enough: a repo has a clone and can have several
// worktrees, so the operator cannot tell which tree is unprovisioned. The
// message names the failing script and the worktree.
func TestReportWorktreeSetup_NamesTheWorktreeAndScript(t *testing.T) {
	var buf strings.Builder
	result := &workspace.SetupResult{
		RepoName: "app",
		Scripts: []workspace.ScriptResult{
			{Name: "01-ok.sh"},
			{Name: "02-boom.sh", Error: os.ErrPermission},
		},
	}

	reportWorktreeSetup(&buf, "/ws/.niwa/worktrees/app-abc123", result)

	got := buf.String()
	if !strings.Contains(got, "02-boom.sh") {
		t.Errorf("the diagnostic does not name the failing script: %q", got)
	}
	if !strings.Contains(got, "app-abc123") {
		t.Errorf("the diagnostic does not name the worktree, so the operator cannot "+
			"tell which tree is unprovisioned: %q", got)
	}
	if strings.Contains(got, "01-ok.sh") {
		t.Errorf("the diagnostic reports a script that succeeded: %q", got)
	}
}

// TestReportWorktreeSetup_SilentOnSuccess pins that a clean run says nothing,
// so the diagnostic stays a signal rather than noise on every create.
func TestReportWorktreeSetup_SilentOnSuccess(t *testing.T) {
	var buf strings.Builder
	reportWorktreeSetup(&buf, "/ws/.niwa/worktrees/app-abc123", &workspace.SetupResult{
		RepoName: "app",
		Scripts:  []workspace.ScriptResult{{Name: "01-ok.sh"}},
	})
	if buf.Len() != 0 {
		t.Errorf("expected silence on a clean setup run, got %q", buf.String())
	}

	var nilBuf strings.Builder
	reportWorktreeSetup(&nilBuf, "/ws/.niwa/worktrees/app-abc123", nil)
	if nilBuf.Len() != 0 {
		t.Errorf("expected silence for a nil result, got %q", nilBuf.String())
	}
}

// TestFromHookCreate_SetupFailureKeepsWorktreeAndStdout is the criterion that
// decides whether this feature is safe on the surface that matters most.
//
// Two properties, and they pull against each other, which is why they are
// asserted together:
//
//   - The worktree survives a failing setup script. On this path a failed
//     content install runs a guarded teardown that deletes it, and the guard
//     retains only a tree git reports dirty -- while everything niwa writes is
//     git-excluded. The script here writes NOTHING before failing, so the tree
//     reads perfectly clean and the teardown would succeed.
//   - stdout stays byte-identical to the no-setup case. The hook contract is
//     that stdout carries only the absolute worktree path, which Claude Code
//     reads as the session working directory. The obvious way to satisfy
//     "report the failure" is to print it, and printing it on stdout would
//     break that contract while passing a weaker test.
func TestFromHookCreate_SetupFailureKeepsWorktreeAndStdout(t *testing.T) {
	f := newCreateFlowFixture(t)

	// Baseline: what stdout looks like with no setup script at all.
	baseline := createViaHook(t, f, "baseline-session")
	if baseline.WorktreePath == "" {
		t.Fatal("baseline create produced no worktree path")
	}

	// Now opt the repo in and give it a script that fails having written
	// nothing, then create again.
	optInRepoForWorktreeSetup(t, f.root, f.repo)
	writeFailingRepoSetup(t, f.repoPath, "#!/bin/sh\necho boom >&2\nexit 1\n")

	stdout, stderr, err := runFromHook(t, f.repoPath, mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeCreate",
		"name":            "setup-fails",
		"cwd":             f.repoPath,
	}))
	if err != nil {
		t.Fatalf("a failing setup script must not fail the delegated create: %v\nstderr: %s", err, stderr)
	}

	// Check stdout's SHAPE before using it as a path. If a diagnostic leaked
	// onto stdout, parsing it as a path first produces a confusing
	// "worktree was destroyed" failure that names the warning text -- the right
	// verdict for the wrong stated reason, which costs the next reader a
	// detour.
	wtPath := strings.TrimSpace(stdout)
	if wtPath == "" {
		t.Fatal("stdout carried no worktree path; the hook contract is broken")
	}
	if lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n"); len(lines) != 1 {
		t.Fatalf("stdout carried %d lines, want exactly the worktree path. A "+
			"diagnostic on stdout breaks the hook contract, because Claude Code "+
			"reads this stream as the session working directory:\n%q", len(lines), stdout)
	}
	if !filepath.IsAbs(wtPath) {
		t.Fatalf("stdout is not an absolute worktree path, so something else was "+
			"written to it: %q", wtPath)
	}

	if _, statErr := os.Stat(wtPath); statErr != nil {
		t.Fatalf("the worktree was destroyed by a setup-script failure: %v", statErr)
	}

	// And the failure has to be visible somewhere, or this is just silence
	// with extra steps.
	if !strings.Contains(stderr, "01-fails.sh") {
		t.Errorf("the setup failure was not reported on stderr: %q", stderr)
	}
}

// optInRepoForWorktreeSetup rewrites the fixture's workspace.toml to turn
// worktree setup on for one repo.
func optInRepoForWorktreeSetup(t *testing.T, instanceRoot, repo string) {
	t.Helper()
	path := filepath.Join(instanceRoot, ".niwa", "workspace.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading workspace.toml: %v", err)
	}
	updated := string(raw) + "\n[repos." + repo + "]\nworktree_setup = true\n"
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("writing workspace.toml: %v", err)
	}
}
