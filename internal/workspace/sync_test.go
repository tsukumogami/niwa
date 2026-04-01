package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupBareAndClone creates a bare "remote" repo with an initial commit,
// clones it to a "local" directory, and returns (bareDir, localDir).
func setupBareAndClone(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()

	bareDir := filepath.Join(base, "remote.git")
	localDir := filepath.Join(base, "local")
	helperDir := filepath.Join(base, "helper")

	// Create bare repo with main as default branch.
	run(t, "git", "init", "--bare", "--initial-branch=main", bareDir)

	// Clone into helper, make initial commit, push.
	run(t, "git", "clone", bareDir, helperDir)
	gitConfig(t, helperDir)
	writeFile(t, filepath.Join(helperDir, "README.md"), "initial")
	run(t, "git", "-C", helperDir, "add", ".")
	run(t, "git", "-C", helperDir, "commit", "-m", "initial commit")
	run(t, "git", "-C", helperDir, "push", "origin", "main")

	// Clone into local (the repo under test).
	run(t, "git", "clone", bareDir, localDir)
	gitConfig(t, localDir)

	return bareDir, localDir
}

// pushCommitFromHelper creates a new clone of bare, commits a file, and pushes.
// This simulates remote changes that the local clone doesn't have yet.
func pushCommitFromHelper(t *testing.T, bareDir, filename, content string) {
	t.Helper()
	helperDir := t.TempDir()
	run(t, "git", "clone", bareDir, helperDir)
	gitConfig(t, helperDir)
	writeFile(t, filepath.Join(helperDir, filename), content)
	run(t, "git", "-C", helperDir, "add", ".")
	run(t, "git", "-C", helperDir, "commit", "-m", "add "+filename)
	run(t, "git", "-C", helperDir, "push", "origin", "main")
}

// gitConfig sets user name/email so commits work in temp repos.
func gitConfig(t *testing.T, dir string) {
	t.Helper()
	run(t, "git", "-C", dir, "config", "user.email", "test@test.com")
	run(t, "git", "-C", dir, "config", "user.name", "Test")
}

// run executes a command and fails the test on error. It forces
// init.defaultBranch=main so tests behave the same regardless of the host
// git configuration.
func run(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// writeFile creates or overwrites a file with the given content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncRepo_CleanDefaultBehind(t *testing.T) {
	bareDir, localDir := setupBareAndClone(t)

	// Push a commit to remote that local doesn't have.
	pushCommitFromHelper(t, bareDir, "new.txt", "new content")

	result, err := SyncRepo(context.Background(), localDir, "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "pulled" {
		t.Errorf("expected action=pulled, got %q", result.Action)
	}
	if result.Commits != 1 {
		t.Errorf("expected 1 commit pulled, got %d", result.Commits)
	}

	// Verify the file actually arrived.
	if _, err := os.Stat(filepath.Join(localDir, "new.txt")); err != nil {
		t.Error("expected new.txt to exist after pull")
	}
}

func TestSyncRepo_DirtyDefault(t *testing.T) {
	_, localDir := setupBareAndClone(t)

	// Dirty the working tree.
	writeFile(t, filepath.Join(localDir, "dirty.txt"), "uncommitted")

	result, err := SyncRepo(context.Background(), localDir, "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "skipped" {
		t.Errorf("expected action=skipped, got %q", result.Action)
	}
	if result.Reason != "dirty working tree" {
		t.Errorf("expected reason 'dirty working tree', got %q", result.Reason)
	}
}

func TestSyncRepo_CleanOtherBranch(t *testing.T) {
	_, localDir := setupBareAndClone(t)

	// Switch to a different branch.
	run(t, "git", "-C", localDir, "checkout", "-b", "feature")

	result, err := SyncRepo(context.Background(), localDir, "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "skipped" {
		t.Errorf("expected action=skipped, got %q", result.Action)
	}
	if result.Reason != "on branch feature, not main" {
		t.Errorf("expected reason about wrong branch, got %q", result.Reason)
	}
}

func TestSyncRepo_CleanDefaultDiverged(t *testing.T) {
	bareDir, localDir := setupBareAndClone(t)

	// Push a remote commit.
	pushCommitFromHelper(t, bareDir, "remote.txt", "remote change")

	// Fetch so local knows about the remote commit.
	run(t, "git", "-C", localDir, "fetch", "origin")

	// Make a local commit (creates divergence).
	writeFile(t, filepath.Join(localDir, "local.txt"), "local change")
	run(t, "git", "-C", localDir, "add", ".")
	run(t, "git", "-C", localDir, "commit", "-m", "local commit")

	result, err := SyncRepo(context.Background(), localDir, "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "skipped" {
		t.Errorf("expected action=skipped, got %q", result.Action)
	}
	if result.Reason != "diverged from remote" {
		t.Errorf("expected reason 'diverged from remote', got %q", result.Reason)
	}
}

func TestSyncRepo_CleanDefaultUpToDate(t *testing.T) {
	_, localDir := setupBareAndClone(t)

	result, err := SyncRepo(context.Background(), localDir, "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "up-to-date" {
		t.Errorf("expected action=up-to-date, got %q", result.Action)
	}
}

func TestInspectRepo_NoTracking(t *testing.T) {
	dir := t.TempDir()

	// Create a standalone repo with no remote.
	run(t, "git", "init", "--initial-branch=main", dir)
	gitConfig(t, dir)
	writeFile(t, filepath.Join(dir, "file.txt"), "content")
	run(t, "git", "-C", dir, "add", ".")
	run(t, "git", "-C", dir, "commit", "-m", "initial")

	status, err := InspectRepo(dir, "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.NoTracking {
		t.Error("expected NoTracking=true for repo without upstream")
	}
	if !status.Clean {
		t.Error("expected Clean=true")
	}
}
