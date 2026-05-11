package sessionattach

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// setupSession seeds a fake instance root with a single lifecycle state file
// pointing at a worktree dir we control. Returns instanceRoot, sessionID,
// worktreePath.
func setupSession(t *testing.T) (instanceRoot, sessionID, worktreePath string) {
	t.Helper()
	instanceRoot = t.TempDir()
	sessionID = "abcd1234"
	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	worktreePath = filepath.Join(instanceRoot, ".niwa", "worktrees", "niwa-"+sessionID)
	if err := os.MkdirAll(filepath.Join(worktreePath, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	state := mcp.SessionLifecycleState{
		V:            1,
		SessionID:    sessionID,
		Repo:         "niwa",
		Status:       mcp.SessionStatusActive,
		WorktreePath: worktreePath,
	}
	if err := mcp.WriteSessionLifecycleState(sessionsDir, state); err != nil {
		t.Fatalf("write lifecycle state: %v", err)
	}
	return instanceRoot, sessionID, worktreePath
}

func TestDetachNoSentinelIsNoOp(t *testing.T) {
	root, sid, _ := setupSession(t)
	var stderr bytes.Buffer
	err := DetachRun(context.Background(), DetachOptions{
		InstanceRoot: root,
		SessionID:    sid,
		Stderr:       &stderr,
	})
	if err != nil {
		t.Errorf("Run with no sentinel: unexpected err %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected silent stderr, got %q", stderr.String())
	}
}

func TestDetachStaleSentinelAutoReaps(t *testing.T) {
	root, sid, wt := setupSession(t)
	if err := mcp.WriteAttachState(wt, mcp.AttachState{
		V: 1, OwnerPID: os.Getpid(), OwnerStartTime: 1, // bogus start time
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		LockPath:  ".niwa/attach.lock",
	}); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	if err := DetachRun(context.Background(), DetachOptions{
		InstanceRoot: root, SessionID: sid,
	}); err != nil {
		t.Errorf("stale-detach: unexpected err %v", err)
	}
	if _, err := os.Stat(mcp.AttachStatePath(wt)); !os.IsNotExist(err) {
		t.Errorf("sentinel not reaped: %v", err)
	}
}

func TestDetachLiveHolderWithoutForceFailsCode3(t *testing.T) {
	root, sid, wt := setupSession(t)
	pid := os.Getpid()
	start, _ := mcp.PIDStartTime(pid)
	if err := mcp.WriteAttachState(wt, mcp.AttachState{
		V: 1, OwnerPID: pid, OwnerStartTime: start,
		StartedAt: "2026-05-10T14:32:11Z",
		LockPath:  ".niwa/attach.lock",
	}); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	err := DetachRun(context.Background(), DetachOptions{
		InstanceRoot: root, SessionID: sid, Force: false,
	})
	if err == nil {
		t.Fatalf("expected ExitCodeError, got nil")
	}
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T", err)
	}
	if ece.Code != 3 {
		t.Errorf("Code = %d, want 3", ece.Code)
	}
	wantSubstrs := []string{
		"is currently attached",
		"pid=" + itoa(pid),
		"started=2026-05-10T14:32:11Z",
		"`niwa session detach " + sid + " --force`",
	}
	for _, s := range wantSubstrs {
		if !strings.Contains(ece.Msg, s) {
			t.Errorf("missing %q in error: %q", s, ece.Msg)
		}
	}
	// Sentinel must NOT have been removed.
	if _, err := os.Stat(mcp.AttachStatePath(wt)); err != nil {
		t.Errorf("sentinel should still exist: %v", err)
	}
}

func TestDetachLiveHolderForceKillsAndReturns4(t *testing.T) {
	root, sid, wt := setupSession(t)
	// Spawn a long-sleeping child so we have a real live PID to SIGTERM.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skipf("sleep not available: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	start, _ := mcp.PIDStartTime(cmd.Process.Pid)
	if err := mcp.WriteAttachState(wt, mcp.AttachState{
		V: 1, OwnerPID: cmd.Process.Pid, OwnerStartTime: start,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		LockPath:  ".niwa/attach.lock",
	}); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	var stderr bytes.Buffer
	err := DetachRun(context.Background(), DetachOptions{
		InstanceRoot: root, SessionID: sid,
		Force:        true,
		GraceSeconds: 1, // tight grace for test speed
		Stderr:       &stderr,
	})
	if err == nil {
		t.Fatalf("expected ExitCodeError code 4, got nil")
	}
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T", err)
	}
	if ece.Code != 4 {
		t.Errorf("Code = %d, want 4", ece.Code)
	}
	if !strings.Contains(stderr.String(), "warning: detaching live attach holder") {
		t.Errorf("missing live-holder warning in stderr: %q", stderr.String())
	}
	if _, err := os.Stat(mcp.AttachStatePath(wt)); !os.IsNotExist(err) {
		t.Errorf("sentinel not removed: %v", err)
	}
}

func TestDetachSessionNotFound(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	_ = os.MkdirAll(sessionsDir, 0o700)
	err := DetachRun(context.Background(), DetachOptions{
		InstanceRoot: root, SessionID: "deadbeef",
	})
	var ece *ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T", err)
	}
	if !strings.Contains(ece.Msg, "session deadbeef not found") {
		t.Errorf("missing not-found in error: %q", ece.Msg)
	}
}
