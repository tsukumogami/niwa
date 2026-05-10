package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedAttachableSession writes a single SessionLifecycleState file plus a
// minimal worktree directory under instanceRoot. Returns the worktree path
// so the test can seed an attach.state sentinel under it.
func seedAttachableSession(t *testing.T, instanceRoot, sessionID string) string {
	t.Helper()
	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	worktreePath := filepath.Join(instanceRoot, ".niwa", "worktrees", "niwa-"+sessionID)
	if err := os.MkdirAll(filepath.Join(worktreePath, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	state := SessionLifecycleState{
		V:            1,
		SessionID:    sessionID,
		Repo:         "niwa",
		Status:       SessionStatusActive,
		WorktreePath: worktreePath,
		CreationTime: time.Now().UTC().Format(time.RFC3339),
	}
	if err := WriteSessionLifecycleState(sessionsDir, state); err != nil {
		t.Fatalf("write lifecycle state: %v", err)
	}
	return worktreePath
}

// seedLiveAttachSentinel writes an attach.state sentinel under worktreePath
// using the current process as the (alive) holder.
func seedLiveAttachSentinel(t *testing.T, worktreePath string) {
	t.Helper()
	pid := os.Getpid()
	start, _ := PIDStartTime(pid)
	if err := WriteAttachState(worktreePath, AttachState{
		V:              1,
		OwnerPID:       pid,
		OwnerStartTime: start,
		StartedAt:      time.Now().UTC().Format(time.RFC3339),
		LockPath:       ".niwa/attach.lock",
	}); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
}

func TestHandleDestroySession_RefusesWhenAttachedNoForce(t *testing.T) {
	root := t.TempDir()
	worktreePath := seedAttachableSession(t, root, "abcd1234")
	seedLiveAttachSentinel(t, worktreePath)

	s := &Server{
		instanceRoot:  root,
		role:          "coordinator",
		daemonStopper: func(string) error { return nil },
	}
	result := s.handleDestroySession(destroySessionArgs{SessionID: "abcd1234"})
	if !result.IsError {
		t.Fatalf("expected error result when attach lock is held")
	}
	if code := errorCode(&result); code != "SESSION_ATTACHED" {
		t.Errorf("error_code = %q, want SESSION_ATTACHED", code)
	}
	msg := result.Content[0].Text
	if !strings.Contains(msg, "niwa session detach") {
		t.Errorf("message missing recovery command: %q", msg)
	}
	if !strings.Contains(msg, "pid=") {
		t.Errorf("message missing pid=: %q", msg)
	}
}

func TestHandleDestroySession_ProceedsWhenAttachedWithForce(t *testing.T) {
	root := t.TempDir()
	worktreePath := seedAttachableSession(t, root, "abcd1234")
	seedLiveAttachSentinel(t, worktreePath)

	s := &Server{
		instanceRoot:  root,
		role:          "coordinator",
		daemonStopper: func(string) error { return nil },
	}
	// With Force=true the destroy should bypass the SESSION_ATTACHED check.
	// We don't have a real worktree git repo, so the function will hit the
	// repoErr path and skip branch deletion -- but it should still write the
	// terminal state and return non-error.
	result := s.handleDestroySession(destroySessionArgs{SessionID: "abcd1234", Force: true})
	if result.IsError {
		t.Fatalf("expected success with force, got error: %s", result.Content[0].Text)
	}
	// Confirm the state transitioned to ended.
	st, err := ReadSessionLifecycleState(filepath.Join(root, ".niwa", "sessions"), "abcd1234")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if st.Status != SessionStatusEnded {
		t.Errorf("status = %q, want %q", st.Status, SessionStatusEnded)
	}
}

func TestHandleListSessions_AttachProjection(t *testing.T) {
	root := t.TempDir()
	wtFree := seedAttachableSession(t, root, "11111111")
	wtAttached := seedAttachableSession(t, root, "22222222")
	seedLiveAttachSentinel(t, wtAttached)

	s := &Server{instanceRoot: root, role: "coordinator"}
	result := s.handleListSessions(listSessionsArgs{})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var got []SessionLifecycleState
	if err := json.Unmarshal([]byte(result.Content[0].Text), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, st := range got {
		switch st.SessionID {
		case "11111111":
			if st.Attach != nil {
				t.Errorf("session at %s should have no attach sub-object, got %+v", wtFree, st.Attach)
			}
		case "22222222":
			if st.Attach == nil {
				t.Errorf("session at %s should have attach sub-object, got nil", wtAttached)
			} else if st.Attach.OwnerPID != os.Getpid() {
				t.Errorf("attach.OwnerPID = %d, want %d", st.Attach.OwnerPID, os.Getpid())
			}
		}
	}
}

func TestHandleListSessions_AttachKeyAbsentNotNullInJSON(t *testing.T) {
	root := t.TempDir()
	seedAttachableSession(t, root, "11111111")
	s := &Server{instanceRoot: root, role: "coordinator"}
	result := s.handleListSessions(listSessionsArgs{})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	raw := result.Content[0].Text
	// PRD R12: attach key MUST be absent (not null) when no lock is held.
	if strings.Contains(raw, `"attach"`) {
		t.Errorf("JSON contains 'attach' key but no lock is held: %s", raw)
	}
	if strings.Contains(raw, "null") {
		t.Errorf("JSON contains null where attach key should be absent: %s", raw)
	}
}

func TestHandleListSessions_AttachedFilter(t *testing.T) {
	root := t.TempDir()
	seedAttachableSession(t, root, "11111111")
	wtAttached := seedAttachableSession(t, root, "22222222")
	seedLiveAttachSentinel(t, wtAttached)

	s := &Server{instanceRoot: root, role: "coordinator"}
	result := s.handleListSessions(listSessionsArgs{Attached: true})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var got []SessionLifecycleState
	_ = json.Unmarshal([]byte(result.Content[0].Text), &got)
	if len(got) != 1 || got[0].SessionID != "22222222" {
		t.Errorf("attached filter returned %d rows: %+v", len(got), got)
	}
}

func TestHandleListSessions_AvailableFilter(t *testing.T) {
	root := t.TempDir()
	seedAttachableSession(t, root, "11111111")
	wtAttached := seedAttachableSession(t, root, "22222222")
	seedLiveAttachSentinel(t, wtAttached)

	s := &Server{instanceRoot: root, role: "coordinator"}
	result := s.handleListSessions(listSessionsArgs{Available: true})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var got []SessionLifecycleState
	_ = json.Unmarshal([]byte(result.Content[0].Text), &got)
	if len(got) != 1 || got[0].SessionID != "11111111" {
		t.Errorf("available filter returned %d rows: %+v", len(got), got)
	}
}

func TestHandleListSessions_AttachedAvailableMutuallyExclusive(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa", "sessions"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s := &Server{instanceRoot: root, role: "coordinator"}
	result := s.handleListSessions(listSessionsArgs{Attached: true, Available: true})
	if !result.IsError {
		t.Fatalf("expected error for mutually-exclusive filters")
	}
	if !strings.Contains(result.Content[0].Text, "mutually exclusive") {
		t.Errorf("message missing 'mutually exclusive': %q", result.Content[0].Text)
	}
}
