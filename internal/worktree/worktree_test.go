package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldWorktreeNiwa_CreatesLayout(t *testing.T) {
	dir := t.TempDir()
	if err := scaffoldWorktreeNiwa(dir); err != nil {
		t.Fatalf("scaffoldWorktreeNiwa: %v", err)
	}

	niwaDir := filepath.Join(dir, ".niwa")
	wantDirs := []string{
		niwaDir,
		filepath.Join(niwaDir, "sessions"),
	}
	for _, d := range wantDirs {
		if fi, err := os.Stat(d); err != nil {
			t.Errorf("missing dir %s: %v", d, err)
		} else if !fi.IsDir() {
			t.Errorf("%s is not a directory", d)
		}
	}

	// Verify the removed mesh-shaped scaffolding (roles/, tasks/) and the
	// main-instance artifacts (mcp.json, workspace-context.md) are NOT created.
	for _, unwanted := range []string{
		filepath.Join(dir, ".mcp.json"),
		filepath.Join(dir, "workspace-context.md"),
		filepath.Join(niwaDir, "tasks"),
		filepath.Join(niwaDir, "roles"),
	} {
		if _, err := os.Stat(unwanted); err == nil {
			t.Errorf("unexpected file created: %s", unwanted)
		}
	}
}

func TestScaffoldWorktreeNiwa_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if err := scaffoldWorktreeNiwa(dir); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := scaffoldWorktreeNiwa(dir); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestFindRepoInWorkspace(t *testing.T) {
	root := t.TempDir()
	// Create a group/repo structure.
	repoPath := filepath.Join(root, "public", "myrepo")
	if err := os.MkdirAll(filepath.Join(repoPath, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := findRepoInWorkspace(root, "myrepo")
	if err != nil {
		t.Fatalf("findRepoInWorkspace: %v", err)
	}
	if got != repoPath {
		t.Errorf("got %q, want %q", got, repoPath)
	}
}

func TestFindRepoInWorkspace_NotFound(t *testing.T) {
	root := t.TempDir()
	_, err := findRepoInWorkspace(root, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent repo")
	}
}

func TestCreateSession_RequiredFields(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name   string
		params CreateSessionParams
	}{
		{"missing_repo", CreateSessionParams{InstanceRoot: root, Purpose: "p", GitInvoker: StdGitInvoker{}}},
		{"missing_purpose", CreateSessionParams{InstanceRoot: root, Repo: "r", GitInvoker: StdGitInvoker{}}},
		{"missing_invoker", CreateSessionParams{InstanceRoot: root, Repo: "r", Purpose: "p"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := CreateSession(context.Background(), tc.params); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestCreateSession_UnknownRole(t *testing.T) {
	root := t.TempDir()
	_, _, _, err := CreateSession(context.Background(), CreateSessionParams{
		InstanceRoot: root,
		Repo:         "nonexistent",
		Purpose:      "test",
		GitInvoker:   StdGitInvoker{},
	})
	if err == nil {
		t.Fatal("expected error for unknown role")
	}
}

// TestCreateSession_Integration creates a real git repository and exercises
// the full CreateSession flow, verifying the session state file, git
// worktree, branch, and scaffolded .niwa layout.
func TestCreateSession_Integration(t *testing.T) {
	if _, err := runCmd("git", "--version"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	repoPath := filepath.Join(root, "group", "myrepo")
	if err := os.MkdirAll(repoPath, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range [][]string{
		{"git", "-C", repoPath, "init", "-b", "main"},
		{"git", "-C", repoPath, "-c", "user.email=test@test.com", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "init"},
	} {
		if _, err := runCmd(cmd[0], cmd[1:]...); err != nil {
			t.Fatalf("git setup: %v", err)
		}
	}
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	sessionID, worktreePath, branch, err := CreateSession(context.Background(), CreateSessionParams{
		InstanceRoot: root,
		Repo:         "myrepo",
		Purpose:      "integration test",
		GitInvoker:   StdGitInvoker{},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sessionID == "" || worktreePath == "" {
		t.Fatalf("empty session id or worktree path: %q %q", sessionID, worktreePath)
	}

	state, err := ReadSessionLifecycleState(sessionsDir, sessionID)
	if err != nil {
		t.Fatalf("ReadSessionLifecycleState: %v", err)
	}
	if state.Status != SessionStatusActive {
		t.Errorf("status = %q, want active", state.Status)
	}
	if state.Repo != "myrepo" {
		t.Errorf("repo = %q, want myrepo", state.Repo)
	}
	if state.WorktreePath != worktreePath {
		t.Errorf("WorktreePath = %q, want %q", state.WorktreePath, worktreePath)
	}

	wantDirs := []string{
		filepath.Join(worktreePath, ".niwa", "sessions"),
	}
	for _, d := range wantDirs {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("missing scaffold dir %s: %v", d, err)
		}
	}

	if branch != "session/"+sessionID {
		t.Errorf("branch = %q, want %q", branch, "session/"+sessionID)
	}
	branchOutput, err := runCmd("git", "-C", repoPath, "branch")
	if err != nil {
		t.Fatalf("git branch: %v", err)
	}
	if !strings.Contains(branchOutput, "session/"+sessionID) {
		t.Errorf("branch %q not found; got:\n%s", "session/"+sessionID, branchOutput)
	}

	worktreeOutput, err := runCmd("git", "-C", repoPath, "worktree", "list")
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	if !strings.Contains(worktreeOutput, worktreePath) {
		t.Errorf("worktree %q not listed; got:\n%s", worktreePath, worktreeOutput)
	}
}

// TestDestroySession_Integration creates a real session then destroys it,
// verifying the terminal state transition and idempotency.
func TestDestroySession_Integration(t *testing.T) {
	if _, err := runCmd("git", "--version"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	repoPath := filepath.Join(root, "group", "myrepo")
	if err := os.MkdirAll(repoPath, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range [][]string{
		{"git", "-C", repoPath, "init", "-b", "main"},
		{"git", "-C", repoPath, "-c", "user.email=test@test.com", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "init"},
	} {
		if _, err := runCmd(cmd[0], cmd[1:]...); err != nil {
			t.Fatalf("git setup: %v", err)
		}
	}
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	sessionID, _, _, err := CreateSession(context.Background(), CreateSessionParams{
		InstanceRoot: root,
		Repo:         "myrepo",
		Purpose:      "destroy test",
		GitInvoker:   StdGitInvoker{},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	state, err := DestroySession(context.Background(), root, sessionID, true /* force */, StdGitInvoker{})
	if err != nil {
		t.Fatalf("DestroySession: %v", err)
	}
	if state.Status != SessionStatusEnded {
		t.Errorf("status = %q, want ended", state.Status)
	}

	persisted, err := ReadSessionLifecycleState(sessionsDir, sessionID)
	if err != nil {
		t.Fatalf("ReadSessionLifecycleState after destroy: %v", err)
	}
	if persisted.Status != SessionStatusEnded {
		t.Errorf("persisted status = %q, want ended", persisted.Status)
	}

	// Idempotent second destroy returns the terminal state, no error.
	state2, err := DestroySession(context.Background(), root, sessionID, true, StdGitInvoker{})
	if err != nil {
		t.Fatalf("second DestroySession: %v", err)
	}
	if state2.Status != SessionStatusEnded {
		t.Errorf("idempotent destroy status = %q, want ended", state2.Status)
	}
}

// recordingInvoker is a GitInvoker that records the argv of every git call
// and runs a harmless no-op command instead of forking real git, so a test
// can assert whether teardown git ops were attempted without needing a repo.
type recordingInvoker struct {
	calls [][]string
}

func (r *recordingInvoker) CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	r.calls = append(r.calls, args)
	// `true` exits 0 and touches nothing; stands in for the real git binary.
	return exec.CommandContext(ctx, "true")
}

// writeLiveAttachSentinel writes an attach.state whose OwnerPID is this test
// process (guaranteed alive) so ReadAttachState reports AttachAttached.
func writeLiveAttachSentinel(t *testing.T, worktreePath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(worktreePath, ".niwa"), 0o700); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	start, _ := PIDStartTime(pid)
	if err := WriteAttachState(worktreePath, AttachState{
		V:              1,
		OwnerPID:       pid,
		OwnerStartTime: start,
		StartedAt:      "2026-06-05T00:00:00Z",
		LockPath:       ".niwa/attach.lock",
	}); err != nil {
		t.Fatalf("WriteAttachState: %v", err)
	}
}

// seedActiveSession writes an active session lifecycle state file pointing at
// worktreePath and returns the session ID.
func seedActiveSession(t *testing.T, root, repo, worktreePath string) string {
	t.Helper()
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sid := "abcd1234"
	state := NewSessionLifecycleState(sid, repo, "guard test", "", worktreePath, "session/"+sid)
	if err := WriteSessionLifecycleState(sessionsDir, state); err != nil {
		t.Fatalf("WriteSessionLifecycleState: %v", err)
	}
	return sid
}

// TestDestroySession_AttachLockGuard verifies the attach-lock guard restored
// from the old MCP destroy handler: destroying a session whose worktree is
// held by a live attach process is refused unless force is set.
func TestDestroySession_AttachLockGuard(t *testing.T) {
	repo := "myrepo"

	// Refused: live attach holder, force=false.
	t.Run("refused_when_attached_and_live", func(t *testing.T) {
		root := t.TempDir()
		worktreePath := filepath.Join(root, "group", repo)
		sid := seedActiveSession(t, root, repo, worktreePath)
		writeLiveAttachSentinel(t, worktreePath)

		inv := &recordingInvoker{}
		state, err := DestroySession(context.Background(), root, sid, false /* force */, inv)
		if !errors.Is(err, ErrSessionAttached) {
			t.Fatalf("want ErrSessionAttached, got %v", err)
		}
		// Guard must run before any teardown: no git ops, status not advanced.
		if len(inv.calls) != 0 {
			t.Errorf("expected no git calls when refused, got %v", inv.calls)
		}
		if state.Status == SessionStatusEnded {
			t.Errorf("status must not be advanced to ended when destroy is refused")
		}
		// Persisted state must remain active (terminal write skipped).
		persisted, perr := ReadSessionLifecycleState(filepath.Join(root, ".niwa", "sessions"), sid)
		if perr != nil {
			t.Fatalf("ReadSessionLifecycleState: %v", perr)
		}
		if persisted.Status != SessionStatusActive {
			t.Errorf("persisted status = %q, want active", persisted.Status)
		}
	})

	// Allowed: live attach holder but force=true bypasses the guard.
	t.Run("succeeds_when_attached_but_force", func(t *testing.T) {
		root := t.TempDir()
		worktreePath := filepath.Join(root, "group", repo)
		sid := seedActiveSession(t, root, repo, worktreePath)
		writeLiveAttachSentinel(t, worktreePath)

		inv := &recordingInvoker{}
		state, err := DestroySession(context.Background(), root, sid, true /* force */, inv)
		if err != nil {
			t.Fatalf("DestroySession with force: %v", err)
		}
		if state.Status != SessionStatusEnded {
			t.Errorf("status = %q, want ended", state.Status)
		}
	})

	// Allowed: no attach sentinel, force=false proceeds normally.
	t.Run("succeeds_when_not_attached", func(t *testing.T) {
		root := t.TempDir()
		worktreePath := filepath.Join(root, "group", repo)
		sid := seedActiveSession(t, root, repo, worktreePath)
		// No attach sentinel written -> AttachAvailable.

		inv := &recordingInvoker{}
		state, err := DestroySession(context.Background(), root, sid, false /* force */, inv)
		if err != nil {
			t.Fatalf("DestroySession without attach: %v", err)
		}
		if state.Status != SessionStatusEnded {
			t.Errorf("status = %q, want ended", state.Status)
		}
	})
}

// TestWorktreeHasUncommittedChanges exercises the dirty-check helper against a
// real git repo: a clean checkout reports not-dirty, an untracked/modified file
// reports dirty, and a missing path reports not-dirty (nothing to lose).
func TestWorktreeHasUncommittedChanges(t *testing.T) {
	if _, err := runCmd("git", "--version"); err != nil {
		t.Skip("git not available")
	}

	repo := t.TempDir()
	for _, cmd := range [][]string{
		{"git", "-C", repo, "init", "-b", "main"},
		{"git", "-C", repo, "-c", "user.email=test@test.com", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "init"},
	} {
		if _, err := runCmd(cmd[0], cmd[1:]...); err != nil {
			t.Fatalf("git setup: %v", err)
		}
	}

	ctx := context.Background()
	inv := StdGitInvoker{}

	// Clean checkout: not dirty.
	dirty, err := worktreeHasUncommittedChanges(ctx, repo, inv)
	if err != nil {
		t.Fatalf("clean check: %v", err)
	}
	if dirty {
		t.Error("clean worktree reported dirty")
	}

	// Untracked file: dirty.
	if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("wip"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirty, err = worktreeHasUncommittedChanges(ctx, repo, inv)
	if err != nil {
		t.Fatalf("dirty check: %v", err)
	}
	if !dirty {
		t.Error("worktree with an untracked file reported not dirty")
	}

	// Missing path: not dirty (nothing to lose).
	dirty, err = worktreeHasUncommittedChanges(ctx, filepath.Join(repo, "does-not-exist"), inv)
	if err != nil {
		t.Fatalf("missing-path check: %v", err)
	}
	if dirty {
		t.Error("missing worktree path reported dirty")
	}

	// Empty path: not dirty.
	dirty, err = worktreeHasUncommittedChanges(ctx, "", inv)
	if err != nil {
		t.Fatalf("empty-path check: %v", err)
	}
	if dirty {
		t.Error("empty worktree path reported dirty")
	}
}

// TestDestroySession_DirtyGuard verifies the uncommitted-work guard: a dirty
// worktree is refused (ErrWorktreeDirty, no teardown, state preserved) unless
// force is set, and a clean worktree destroys as before. Uses real git so the
// porcelain status the helper reads is genuine.
func TestDestroySession_DirtyGuard(t *testing.T) {
	if _, err := runCmd("git", "--version"); err != nil {
		t.Skip("git not available")
	}

	// setup creates a real repo + an active session whose worktree is a git
	// worktree on a fresh branch, returning the root, session id, and worktree
	// path so each subtest can dirty (or not dirty) it independently.
	setup := func(t *testing.T) (root, sid, worktreePath string) {
		t.Helper()
		root = t.TempDir()
		repoPath := filepath.Join(root, "group", "myrepo")
		if err := os.MkdirAll(repoPath, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, cmd := range [][]string{
			{"git", "-C", repoPath, "init", "-b", "main"},
			{"git", "-C", repoPath, "-c", "user.email=test@test.com", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "init"},
		} {
			if _, err := runCmd(cmd[0], cmd[1:]...); err != nil {
				t.Fatalf("git setup: %v", err)
			}
		}
		sessionsDir := filepath.Join(root, ".niwa", "sessions")
		if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		var err error
		sid, worktreePath, _, err = CreateSession(context.Background(), CreateSessionParams{
			InstanceRoot: root,
			Repo:         "myrepo",
			Purpose:      "dirty guard test",
			GitInvoker:   StdGitInvoker{},
		})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		return root, sid, worktreePath
	}

	// Refused: dirty worktree, force=false. No teardown; state stays active.
	t.Run("refused_when_dirty", func(t *testing.T) {
		root, sid, worktreePath := setup(t)
		if err := os.WriteFile(filepath.Join(worktreePath, "scratch.txt"), []byte("wip"), 0o600); err != nil {
			t.Fatal(err)
		}

		state, err := DestroySession(context.Background(), root, sid, false /* force */, StdGitInvoker{})
		if !errors.Is(err, ErrWorktreeDirty) {
			t.Fatalf("want ErrWorktreeDirty, got %v", err)
		}
		if state.Status == SessionStatusEnded {
			t.Error("status must not advance to ended when destroy is refused")
		}
		// Persisted state must remain active (terminal write skipped) and the
		// worktree must still exist (no teardown ran).
		persisted, perr := ReadSessionLifecycleState(filepath.Join(root, ".niwa", "sessions"), sid)
		if perr != nil {
			t.Fatalf("ReadSessionLifecycleState: %v", perr)
		}
		if persisted.Status != SessionStatusActive {
			t.Errorf("persisted status = %q, want active", persisted.Status)
		}
		if _, statErr := os.Stat(worktreePath); statErr != nil {
			t.Errorf("worktree was torn down despite refusal: %v", statErr)
		}
	})

	// Allowed: dirty worktree but force=true bypasses the guard.
	t.Run("succeeds_when_dirty_but_force", func(t *testing.T) {
		root, sid, worktreePath := setup(t)
		if err := os.WriteFile(filepath.Join(worktreePath, "scratch.txt"), []byte("wip"), 0o600); err != nil {
			t.Fatal(err)
		}

		state, err := DestroySession(context.Background(), root, sid, true /* force */, StdGitInvoker{})
		if err != nil {
			t.Fatalf("DestroySession with force on dirty worktree: %v", err)
		}
		if state.Status != SessionStatusEnded {
			t.Errorf("status = %q, want ended", state.Status)
		}
	})

	// Allowed: clean worktree, force=false destroys as before.
	t.Run("succeeds_when_clean", func(t *testing.T) {
		root, sid, _ := setup(t)

		state, err := DestroySession(context.Background(), root, sid, false /* force */, StdGitInvoker{})
		if err != nil {
			t.Fatalf("DestroySession on clean worktree: %v", err)
		}
		if state.Status != SessionStatusEnded {
			t.Errorf("status = %q, want ended", state.Status)
		}
	})
}

// runCmd runs a command and returns its combined output.
func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}
