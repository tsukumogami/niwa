package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCloneOrSyncOverlay_MissingDirReturnsFirstTime(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "nonexistent")

	// Use a local path that doesn't exist as a repo URL — the clone will fail,
	// but it should fail with firstTime=true.
	firstTime, err := CloneOrSyncOverlay("/does/not/exist/as/a/repo", dir)
	if !firstTime {
		t.Errorf("expected firstTime=true for missing dir, got false")
	}
	if err == nil {
		t.Error("expected error when cloning from invalid URL, got nil")
	}
}

func TestCloneOrSyncOverlay_ExistingValidRepoReturnsNotFirstTime(t *testing.T) {
	tmp := t.TempDir()

	// Create a minimal git repo to serve as the "remote".
	remoteDir := filepath.Join(tmp, "remote")
	if err := os.MkdirAll(remoteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	overlaysyncGitRunIn(t, remoteDir, "init", "--initial-branch=main")
	overlaysyncGitRunIn(t, remoteDir, "config", "user.email", "test@test.com")
	overlaysyncGitRunIn(t, remoteDir, "config", "user.name", "Test")
	overlaysyncGitRunIn(t, remoteDir, "commit", "--allow-empty", "-m", "init")

	// Clone it.
	cloneDir := filepath.Join(tmp, "clone")
	firstTime, err := CloneOrSyncOverlay(remoteDir, cloneDir)
	if err != nil {
		t.Fatalf("initial clone failed: %v", err)
	}
	if !firstTime {
		t.Error("expected firstTime=true on initial clone")
	}

	// Sync (pull) — should return firstTime=false.
	firstTime2, err := CloneOrSyncOverlay(remoteDir, cloneDir)
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if firstTime2 {
		t.Error("expected firstTime=false on subsequent sync")
	}
}

// overlaysyncGitRunIn runs a git command inside dir, failing the test on error.
func overlaysyncGitRunIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	fullArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", fullArgs...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
