package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEnsureOverlaySnapshot_FirstCloneReportsFresh(t *testing.T) {
	tmp := t.TempDir()
	remote := overlaysyncMakeBareRepo(t, filepath.Join(tmp, "remote"), "overlay.toml", `name = "demo"`)
	dir := filepath.Join(tmp, "snapshot")

	wasFresh, err := EnsureOverlaySnapshot(context.Background(), remote, dir, nil, nil)
	if err != nil {
		t.Fatalf("EnsureOverlaySnapshot: %v", err)
	}
	if !wasFresh {
		t.Error("expected wasFreshClone=true on initial materialization")
	}
	if !provenanceMarkerExists(dir) {
		t.Error("expected provenance marker after fresh materialization")
	}
}

func TestEnsureOverlaySnapshot_RefreshReportsNotFresh(t *testing.T) {
	tmp := t.TempDir()
	remote := overlaysyncMakeBareRepo(t, filepath.Join(tmp, "remote"), "overlay.toml", `name = "demo"`)
	dir := filepath.Join(tmp, "snapshot")

	if _, err := EnsureOverlaySnapshot(context.Background(), remote, dir, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}

	wasFresh, err := EnsureOverlaySnapshot(context.Background(), remote, dir, nil, nil)
	if err != nil {
		t.Fatalf("EnsureOverlaySnapshot refresh: %v", err)
	}
	if wasFresh {
		t.Error("expected wasFreshClone=false on subsequent refresh")
	}
}

func TestEnsureOverlaySnapshot_BadURLReportsFreshClone(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "snapshot")

	wasFresh, err := EnsureOverlaySnapshot(context.Background(), "/does/not/exist/as/a/repo", dir, nil, nil)
	if err == nil {
		t.Error("expected error for invalid URL, got nil")
	}
	if !wasFresh {
		t.Error("expected wasFreshClone=true so caller knows to silently skip")
	}
}

// overlaysyncMakeBareRepo creates a bare git repo at dir, populated
// with a single commit containing filename:content. Returns the
// file:// URL for the bare repo. Used by tests that need a real
// remote without network access.
func overlaysyncMakeBareRepo(t *testing.T, dir, filename, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	overlaysyncGitRun(t, "", "init", "--bare", "--initial-branch=main", dir)

	work := filepath.Join(filepath.Dir(dir), "work-"+filepath.Base(dir))
	overlaysyncGitRun(t, "", "clone", dir, work)
	if err := os.WriteFile(filepath.Join(work, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	overlaysyncGitRun(t, work, "config", "user.email", "test@test.com")
	overlaysyncGitRun(t, work, "config", "user.name", "Test")
	overlaysyncGitRun(t, work, "add", filename)
	overlaysyncGitRun(t, work, "commit", "-m", "init")
	overlaysyncGitRun(t, work, "push", "origin", "main")

	return "file://" + dir
}

func overlaysyncGitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
