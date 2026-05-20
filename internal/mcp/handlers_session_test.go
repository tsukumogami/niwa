package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldWorktreeNiwa_CreatesLayout(t *testing.T) {
	dir := t.TempDir()
	if err := scaffoldWorktreeNiwa(dir, "myrepo"); err != nil {
		t.Fatalf("scaffoldWorktreeNiwa: %v", err)
	}

	niwaDir := filepath.Join(dir, ".niwa")
	wantDirs := []string{
		niwaDir,
		filepath.Join(niwaDir, "tasks"),
		filepath.Join(niwaDir, "sessions"),
		filepath.Join(niwaDir, "roles", "myrepo", "inbox"),
		filepath.Join(niwaDir, "roles", "myrepo", "inbox", "in-progress"),
		filepath.Join(niwaDir, "roles", "myrepo", "inbox", "cancelled"),
		filepath.Join(niwaDir, "roles", "myrepo", "inbox", "expired"),
		filepath.Join(niwaDir, "roles", "myrepo", "inbox", "read"),
	}
	for _, d := range wantDirs {
		if fi, err := os.Stat(d); err != nil {
			t.Errorf("missing dir %s: %v", d, err)
		} else if !fi.IsDir() {
			t.Errorf("%s is not a directory", d)
		}
	}

	wantFiles := []string{
		filepath.Join(niwaDir, "daemon.pid"),
		filepath.Join(niwaDir, "daemon.log"),
	}
	for _, f := range wantFiles {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("missing file %s: %v", f, err)
		}
	}

	// Verify mcp.json and workspace-context.md are NOT created.
	for _, unwanted := range []string{
		filepath.Join(dir, ".mcp.json"),
		filepath.Join(dir, "workspace-context.md"),
	} {
		if _, err := os.Stat(unwanted); err == nil {
			t.Errorf("unexpected file created: %s", unwanted)
		}
	}
}

func TestScaffoldWorktreeNiwa_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if err := scaffoldWorktreeNiwa(dir, "repo1"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Write a sentinel into daemon.log to ensure it's not truncated.
	logPath := filepath.Join(dir, ".niwa", "daemon.log")
	if err := os.WriteFile(logPath, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	if err := scaffoldWorktreeNiwa(dir, "repo1"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	// sentinel should still be there.
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if string(data) != "sentinel" {
		t.Errorf("daemon.log was overwritten; got %q", data)
	}
}

func TestHandleCreateSession_MissingDaemonStarter(t *testing.T) {
	s := newTestServer(t, "coordinator", "")
	// daemonStarter is nil by default; expect error.
	result := s.handleCreateSession(createSessionArgs{Repo: "web", Purpose: "test"})
	if !result.IsError {
		t.Error("expected error result when daemonStarter is nil")
	}
}

func TestHandleCreateSession_UnknownRole(t *testing.T) {
	root := t.TempDir()
	s := &Server{
		instanceRoot:  root,
		role:          "coordinator",
		daemonStarter: func(string, []string) error { return nil },
	}
	result := s.handleCreateSession(createSessionArgs{Repo: "nonexistent", Purpose: "test"})
	if !result.IsError {
		t.Error("expected error for unknown role")
	}
	if code := errorCode(&result); code != "UNKNOWN_ROLE" {
		t.Errorf("error_code = %q, want UNKNOWN_ROLE", code)
	}
}

func TestHandleDestroySession_MissingDaemonStopper(t *testing.T) {
	s := newTestServer(t, "coordinator", "")
	result := s.handleDestroySession(destroySessionArgs{SessionID: "ab12cd34"})
	if !result.IsError {
		t.Error("expected error when daemonStopper is nil")
	}
}

func TestHandleDestroySession_SessionNotFound(t *testing.T) {
	root := t.TempDir()
	// Create .niwa/sessions/ but no session file.
	if err := os.MkdirAll(filepath.Join(root, ".niwa", "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		instanceRoot:  root,
		role:          "coordinator",
		daemonStopper: func(string) error { return nil },
	}
	result := s.handleDestroySession(destroySessionArgs{SessionID: "ab12cd34"})
	if !result.IsError {
		t.Error("expected error for nonexistent session")
	}
	if code := errorCode(&result); code != "SESSION_NOT_FOUND" {
		t.Errorf("error_code = %q, want SESSION_NOT_FOUND", code)
	}
}

func TestHandleDestroySession_Idempotent_AlreadyEnded(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	state := SessionLifecycleState{
		V: 1, SessionID: "ab12cd34", Repo: "web",
		Status: SessionStatusEnded, WorktreePath: "/tmp/wt",
	}
	if err := WriteSessionLifecycleState(sessionsDir, state); err != nil {
		t.Fatalf("write: %v", err)
	}

	stopperCalled := false
	s := &Server{
		instanceRoot:  root,
		role:          "coordinator",
		daemonStopper: func(string) error { stopperCalled = true; return nil },
	}
	result := s.handleDestroySession(destroySessionArgs{SessionID: "ab12cd34"})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	if stopperCalled {
		t.Error("daemonStopper must not be called when session is already ended")
	}
	// Verify response contains the state.
	var got SessionLifecycleState
	if err := json.Unmarshal([]byte(result.Content[0].Text), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Status != SessionStatusEnded {
		t.Errorf("status = %q, want ended", got.Status)
	}
}

func TestHandleCreateSession_RequiredFields(t *testing.T) {
	s := &Server{
		instanceRoot:  t.TempDir(),
		role:          "coordinator",
		daemonStarter: func(string, []string) error { return nil },
	}
	cases := []struct {
		name string
		args createSessionArgs
		code string
	}{
		{"missing_repo", createSessionArgs{Purpose: "p"}, "BAD_PAYLOAD"},
		{"missing_purpose", createSessionArgs{Repo: "r"}, "BAD_PAYLOAD"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := s.handleCreateSession(tc.args)
			if !result.IsError {
				t.Errorf("expected error result")
			}
			if code := errorCode(&result); code != tc.code {
				t.Errorf("error_code = %q, want %s", code, tc.code)
			}
		})
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

// TestHandleCreateSession_Integration creates a real git repository and
// exercises the full niwa_create_session flow using a mock daemon starter.
// It verifies the session state file, git worktree, and scaffolded .niwa layout.
func TestHandleCreateSession_Integration(t *testing.T) {
	if _, err := runCmd("git", "--version"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()

	// Create a workspace structure: <root>/group/myrepo (a git repo).
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

	// Create the niwa roles directory so role validation passes.
	roleDir := filepath.Join(root, ".niwa", "roles", "myrepo")
	if err := os.MkdirAll(roleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Pre-create sessions dir (normally created by InstallChannelInfrastructure).
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	var daemonStartedAt string
	s := &Server{
		instanceRoot: root,
		role:         "coordinator",
		daemonStarter: func(instanceRoot string, extraEnv []string) error {
			daemonStartedAt = instanceRoot
			return nil
		},
	}

	result := s.handleCreateSession(createSessionArgs{
		Repo:    "myrepo",
		Purpose: "integration test",
	})
	if result.IsError {
		t.Fatalf("handleCreateSession error: %v", result.Content)
	}

	// Parse response.
	var resp map[string]string
	if err := json.Unmarshal([]byte(result.Content[0].Text), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	sessionID := resp["session_id"]
	worktreePath := resp["worktree_path"]

	if sessionID == "" {
		t.Error("session_id must not be empty")
	}
	if worktreePath == "" {
		t.Error("worktree_path must not be empty")
	}

	// Verify session state file.
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

	// Verify worktree directory and scaffold.
	wantDirs := []string{
		filepath.Join(worktreePath, ".niwa", "tasks"),
		filepath.Join(worktreePath, ".niwa", "roles", "myrepo", "inbox"),
		filepath.Join(worktreePath, ".niwa", "roles", "myrepo", "inbox", "in-progress"),
	}
	for _, d := range wantDirs {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("missing scaffold dir %s: %v", d, err)
		}
	}

	// Verify daemon was started with the worktree path.
	if daemonStartedAt != worktreePath {
		t.Errorf("daemon started at %q, want %q", daemonStartedAt, worktreePath)
	}

	// Verify git branch was created.
	branchOutput, err := runCmd("git", "-C", repoPath, "branch")
	if err != nil {
		t.Fatalf("git branch: %v", err)
	}
	expectedBranch := "session/" + sessionID
	if !strings.Contains(branchOutput, expectedBranch) {
		t.Errorf("branch %q not found; got:\n%s", expectedBranch, branchOutput)
	}

	// Verify git worktree is listed.
	worktreeOutput, err := runCmd("git", "-C", repoPath, "worktree", "list")
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	if !strings.Contains(worktreeOutput, worktreePath) {
		t.Errorf("worktree %q not listed; got:\n%s", worktreePath, worktreeOutput)
	}
}

// TestHandleListSessions_DaemonSubObject verifies Issue 3: each row carries
// a `daemon: {alive, pid, started_at}` sub-object computed at API call time
// from <worktreePath>/.niwa/daemon.pid + IsPIDAlive — without modifying the
// persisted SessionLifecycleState file (the lifecycle Status field stays
// single-writer).
func TestHandleListSessions_DaemonSubObject(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Two worktrees: one with a live daemon (this test process), one with no
	// daemon (no daemon.pid file at all).
	liveWT := filepath.Join(root, "wt-live")
	deadWT := filepath.Join(root, "wt-dead")
	for _, wt := range []string{liveWT, deadWT} {
		if err := os.MkdirAll(filepath.Join(wt, ".niwa"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Write a daemon.pid pointing at this test process. PIDStartTime returns
	// the recorded /proc start time so IsPIDAlive's cross-check passes.
	pid := os.Getpid()
	startTime, _ := PIDStartTime(pid)
	pidContent := []byte(fmt.Sprintf("%d\n%d\n", pid, startTime))
	if err := os.WriteFile(filepath.Join(liveWT, ".niwa", "daemon.pid"), pidContent, 0o600); err != nil {
		t.Fatal(err)
	}

	// Persist two SessionLifecycleState rows. SessionID must be 8 lowercase
	// hex characters per the lifecycle validator.
	for _, st := range []SessionLifecycleState{
		NewSessionLifecycleState("aabbccdd", "myrepo", "live test", "", liveWT, ""),
		NewSessionLifecycleState("11223344", "myrepo", "dead test", "", deadWT, ""),
	} {
		if err := WriteSessionLifecycleState(sessionsDir, st); err != nil {
			t.Fatal(err)
		}
	}

	s := &Server{instanceRoot: root, role: "coordinator"}
	res := s.handleListSessions(listSessionsArgs{})
	if res.IsError {
		t.Fatalf("handleListSessions error: %v", res.Content)
	}
	var rows []sessionListEntry
	if err := json.Unmarshal([]byte(res.Content[0].Text), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}

	for _, r := range rows {
		switch r.SessionID {
		case "aabbccdd":
			if !r.Daemon.Alive {
				t.Errorf("live session: Daemon.Alive=false, want true")
			}
			if r.Daemon.PID != pid {
				t.Errorf("live session: Daemon.PID=%d, want %d", r.Daemon.PID, pid)
			}
			if r.Daemon.StartedAt == "" {
				t.Errorf("live session: Daemon.StartedAt is empty")
			}
			if r.Status != SessionStatusActive {
				t.Errorf("live session: Status=%q, want active (single-writer preserved)", r.Status)
			}
		case "11223344":
			if r.Daemon.Alive {
				t.Errorf("dead session: Daemon.Alive=true, want false")
			}
			if r.Daemon.PID != 0 {
				t.Errorf("dead session: Daemon.PID=%d, want 0", r.Daemon.PID)
			}
			if r.Status != SessionStatusActive {
				t.Errorf("dead session: Status=%q, want active (Status doesn't mutate on daemon death)", r.Status)
			}
		default:
			t.Errorf("unexpected session_id %q", r.SessionID)
		}
	}

	// Persisted state file must NOT have gained a daemon field — the embedded
	// SessionLifecycleState shape on disk is unchanged by Issue 3.
	persistedPath := filepath.Join(sessionsDir, "aabbccdd.json")
	persistedRaw, _ := os.ReadFile(persistedPath)
	if strings.Contains(string(persistedRaw), `"daemon":`) {
		t.Errorf("persisted state file gained a 'daemon' field; Issue 3 must keep daemon computed-only:\n%s", persistedRaw)
	}
}

// TestHandleListSessions_EmptyResult verifies an empty filter result returns
// an empty array (not null), matching the legacy behavior preserved in the
// Issue 3 wrapping refactor.
func TestHandleListSessions_EmptyResult(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	s := &Server{instanceRoot: root, role: "coordinator"}
	res := s.handleListSessions(listSessionsArgs{Repo: "nonexistent"})
	if res.IsError {
		t.Fatalf("handleListSessions error: %v", res.Content)
	}
	if strings.TrimSpace(res.Content[0].Text) != "[]" {
		t.Errorf("empty result: got %q, want []", res.Content[0].Text)
	}
}

// TestHandleCreateSession_DaemonSpawnTimeoutRollsBack verifies the Issue 2
// contract: when the daemonStarter returns mcp.ErrDaemonSpawnTimeout, the
// handler must roll back the worktree, the session-state file, and the
// branch, and return an errResult with code DAEMON_SPAWN_TIMEOUT — not a
// soft daemon_warning. Mirrors the inotify-exhaustion failure mode in #110.
func TestHandleCreateSession_DaemonSpawnTimeoutRollsBack(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(root, ".niwa", "roles", "myrepo"), 0o700); err != nil {
		t.Fatal(err)
	}
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	s := &Server{
		instanceRoot: root,
		role:         "coordinator",
		daemonStarter: func(instanceRoot string, extraEnv []string) error {
			return ErrDaemonSpawnTimeout
		},
	}

	result := s.handleCreateSession(createSessionArgs{
		Repo:    "myrepo",
		Purpose: "spawn timeout rollback test",
	})
	if !result.IsError {
		t.Fatalf("expected errResult on spawn timeout; got success: %v", result.Content)
	}
	if !strings.Contains(result.Content[0].Text, "DAEMON_SPAWN_TIMEOUT") {
		t.Errorf("expected error to carry DAEMON_SPAWN_TIMEOUT code; got: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "rolled back") {
		t.Errorf("expected error message to mention rollback; got: %s", result.Content[0].Text)
	}

	// Worktree must not exist on disk (rollback removed it).
	entries, _ := os.ReadDir(filepath.Join(root, ".niwa", "worktrees"))
	if len(entries) != 0 {
		t.Errorf("worktree directory should be empty after rollback; got entries: %v", entries)
	}

	// No session state file should remain.
	sessionEntries, _ := os.ReadDir(sessionsDir)
	for _, e := range sessionEntries {
		if strings.HasSuffix(e.Name(), ".json") {
			t.Errorf("session state file %s should have been removed on rollback", e.Name())
		}
	}

	// Branch must not survive (best-effort delete).
	branchOutput, _ := runCmd("git", "-C", repoPath, "branch")
	if strings.Contains(branchOutput, "session/") {
		t.Errorf("session branch should be deleted after rollback; got:\n%s", branchOutput)
	}
}

// TestHandleDestroySession_Integration creates a real session via handleCreateSession
// then destroys it, verifying idempotency and state transitions.
func TestHandleDestroySession_Integration(t *testing.T) {
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
	roleDir := filepath.Join(root, ".niwa", "roles", "myrepo")
	if err := os.MkdirAll(roleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	stopCalls := 0
	s := &Server{
		instanceRoot: root,
		role:         "coordinator",
		daemonStarter: func(string, []string) error { return nil },
		daemonStopper: func(string) error { stopCalls++; return nil },
	}

	// Create the session.
	createResult := s.handleCreateSession(createSessionArgs{Repo: "myrepo", Purpose: "destroy test"})
	if createResult.IsError {
		t.Fatalf("create: %v", createResult.Content)
	}
	var resp map[string]string
	_ = json.Unmarshal([]byte(createResult.Content[0].Text), &resp)
	sessionID := resp["session_id"]

	// Destroy the session with Force=true so the branch is deleted even though
	// it has no merged commits (session branch starts from an empty commit).
	destroyResult := s.handleDestroySession(destroySessionArgs{SessionID: sessionID, Force: true})
	if destroyResult.IsError {
		t.Fatalf("destroy: %v", destroyResult.Content)
	}

	// Verify state is ended.
	state, err := ReadSessionLifecycleState(sessionsDir, sessionID)
	if err != nil {
		t.Fatalf("ReadSessionLifecycleState after destroy: %v", err)
	}
	if state.Status != SessionStatusEnded {
		t.Errorf("status = %q, want ended", state.Status)
	}
	if stopCalls != 1 {
		t.Errorf("daemonStopper called %d times, want 1", stopCalls)
	}

	// Destroy again (idempotency check).
	destroyResult2 := s.handleDestroySession(destroySessionArgs{SessionID: sessionID, Force: true})
	if destroyResult2.IsError {
		t.Fatalf("second destroy unexpectedly errored: %v", destroyResult2.Content)
	}
	// Daemon stopper must NOT be called again.
	if stopCalls != 1 {
		t.Errorf("daemonStopper called %d times after idempotent destroy, want 1", stopCalls)
	}
}

// runCmd runs a command and returns its combined output.
func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}
