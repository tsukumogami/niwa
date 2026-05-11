package sessionattach

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initWorktreeRepo creates a minimal git repo in a temp dir with one
// committed file. Returns the worktree path and the session ID used for
// the (fake) session branch name.
func initWorktreeRepo(t *testing.T) (worktree, sessionID string) {
	t.Helper()
	dir := t.TempDir()
	sessionID = "abcd1234"
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustGit(t, dir, "add", "README")
	mustGit(t, dir, "commit", "-q", "-m", "initial")
	mustGit(t, dir, "checkout", "-q", "-b", "session/"+sessionID)
	return dir, sessionID
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestWarningsCleanWorktreeSilent(t *testing.T) {
	wt, sid := initWorktreeRepo(t)
	var buf bytes.Buffer
	Warnings(wt, sid, &buf)
	if buf.Len() != 0 {
		t.Errorf("expected no warnings on clean worktree, got: %q", buf.String())
	}
}

func TestWarningsUncommittedChanges(t *testing.T) {
	wt, sid := initWorktreeRepo(t)
	if err := os.WriteFile(filepath.Join(wt, "README"), []byte("modified\n"), 0o644); err != nil {
		t.Fatalf("modify README: %v", err)
	}
	var buf bytes.Buffer
	Warnings(wt, sid, &buf)
	out := buf.String()
	if !strings.Contains(out, "warning: worktree has uncommitted changes") {
		t.Errorf("missing uncommitted warning: %q", out)
	}
	if strings.Contains(out, "warning: worktree has untracked files") {
		t.Errorf("untracked warning fired on a non-untracked change: %q", out)
	}
}

func TestWarningsUntrackedOnly(t *testing.T) {
	wt, sid := initWorktreeRepo(t)
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write untracked: %v", err)
	}
	var buf bytes.Buffer
	Warnings(wt, sid, &buf)
	out := buf.String()
	if !strings.Contains(out, "warning: worktree has untracked files") {
		t.Errorf("missing untracked warning: %q", out)
	}
	if strings.Contains(out, "warning: worktree has uncommitted changes") {
		t.Errorf("uncommitted warning fired for untracked-only change: %q", out)
	}
}

func TestWarningsBothChangesAndUntracked(t *testing.T) {
	wt, sid := initWorktreeRepo(t)
	if err := os.WriteFile(filepath.Join(wt, "README"), []byte("modified\n"), 0o644); err != nil {
		t.Fatalf("modify README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write untracked: %v", err)
	}
	var buf bytes.Buffer
	Warnings(wt, sid, &buf)
	out := buf.String()
	if !strings.Contains(out, "warning: worktree has uncommitted changes") {
		t.Errorf("missing uncommitted warning: %q", out)
	}
	if !strings.Contains(out, "warning: worktree has untracked files") {
		t.Errorf("missing untracked warning: %q", out)
	}
}

func TestWarningsUnpushedCommitsOnSessionBranch(t *testing.T) {
	wt, sid := initWorktreeRepo(t)
	// Create a bare upstream repo so the session branch has somewhere to
	// be ahead of. Then set tracking and add a local commit that wasn't
	// pushed.
	upstream := t.TempDir() + "/upstream.git"
	mustGit(t, ".", "init", "--bare", "-q", upstream)
	mustGit(t, wt, "remote", "add", "origin", upstream)
	mustGit(t, wt, "push", "-q", "-u", "origin", "session/"+sid)
	// Local commit not yet pushed.
	if err := os.WriteFile(filepath.Join(wt, "follow-up.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	mustGit(t, wt, "add", "follow-up.txt")
	mustGit(t, wt, "commit", "-q", "-m", "follow-up")

	var buf bytes.Buffer
	Warnings(wt, sid, &buf)
	out := buf.String()
	if !strings.Contains(out, "warning: worktree has unpushed commits on session/"+sid) {
		t.Errorf("missing unpushed warning: %q", out)
	}
	if !strings.Contains(out, "ahead by 1 commit") {
		t.Errorf("expected ahead-by-1 detail: %q", out)
	}
}

func TestWarningsBranchWithoutUpstreamSilent(t *testing.T) {
	// A branch with no upstream is routine (the session was never pushed);
	// no warning should fire because the natural-detach UX shouldn't be
	// noisy by default.
	wt, sid := initWorktreeRepo(t)
	if err := os.WriteFile(filepath.Join(wt, "follow.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustGit(t, wt, "add", "follow.txt")
	mustGit(t, wt, "commit", "-q", "-m", "follow")
	var buf bytes.Buffer
	Warnings(wt, sid, &buf)
	if strings.Contains(buf.String(), "unpushed commits") {
		t.Errorf("unexpected unpushed warning for branch with no upstream: %q", buf.String())
	}
}
