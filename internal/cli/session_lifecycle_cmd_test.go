package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// seedLifecycleSessions writes N session lifecycle state files plus their
// worktree directories under instanceRoot. The caller can then exercise
// runSessionLifecycleList against the same instance root. Returns the
// instance root path.
func seedLifecycleSessions(t *testing.T, sessions []mcp.SessionLifecycleState) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa", "instance.json"), 0o700); err == nil {
		// instance.json must be a regular file, not a dir; fix that.
		_ = os.RemoveAll(filepath.Join(root, ".niwa", "instance.json"))
	}
	if err := os.MkdirAll(filepath.Join(root, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir .niwa: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".niwa", "instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	for _, s := range sessions {
		if s.WorktreePath != "" {
			if err := os.MkdirAll(filepath.Join(s.WorktreePath, ".niwa"), 0o700); err != nil {
				t.Fatalf("mkdir worktree %s: %v", s.WorktreePath, err)
			}
		}
		if err := mcp.WriteSessionLifecycleState(sessionsDir, s); err != nil {
			t.Fatalf("write lifecycle state %s: %v", s.SessionID, err)
		}
	}
	return root
}

func TestSessionList_AvailabilityColumnHeader(t *testing.T) {
	root := seedLifecycleSessions(t, []mcp.SessionLifecycleState{
		{
			V: 1, SessionID: "11111111", Repo: "niwa", Status: mcp.SessionStatusActive,
			WorktreePath: filepath.Join(t.TempDir(), "wt-11111111"),
			CreationTime: time.Now().UTC().Format(time.RFC3339),
		},
	})
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	resetSessionListFlags(t)
	sessionListStatus = "active"
	defer resetSessionListFlags(t)

	var buf bytes.Buffer
	sessionListCmd.SetOut(&buf)
	defer sessionListCmd.SetOut(os.Stdout)
	if err := runSessionList(sessionListCmd, nil); err != nil {
		t.Fatalf("runSessionList: %v", err)
	}
	got := buf.String()
	for _, header := range []string{"SESSION_ID", "REPO", "STATUS", "AVAILABILITY", "CREATED", "PURPOSE"} {
		if !strings.Contains(got, header) {
			t.Errorf("missing header %q in output:\n%s", header, got)
		}
	}
}

func TestSessionList_AvailabilityValuesRendered(t *testing.T) {
	wtFree := filepath.Join(t.TempDir(), "wt-free")
	wtAttached := filepath.Join(t.TempDir(), "wt-attached")
	wtStale := filepath.Join(t.TempDir(), "wt-stale")
	for _, p := range []string{wtFree, wtAttached, wtStale} {
		if err := os.MkdirAll(filepath.Join(p, ".niwa"), 0o700); err != nil {
			t.Fatalf("mkdir worktree %s: %v", p, err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	root := seedLifecycleSessions(t, []mcp.SessionLifecycleState{
		{V: 1, SessionID: "11111111", Repo: "niwa", Status: mcp.SessionStatusActive, WorktreePath: wtFree, CreationTime: now},
		{V: 1, SessionID: "22222222", Repo: "niwa", Status: mcp.SessionStatusActive, WorktreePath: wtAttached, CreationTime: now},
		{V: 1, SessionID: "33333333", Repo: "niwa", Status: mcp.SessionStatusActive, WorktreePath: wtStale, CreationTime: now},
	})
	myPID := os.Getpid()
	myStart, _ := mcp.PIDStartTime(myPID)
	if err := mcp.WriteAttachState(wtAttached, mcp.AttachState{V: 1, OwnerPID: myPID, OwnerStartTime: myStart, StartedAt: now, LockPath: ".niwa/attach.lock"}); err != nil {
		t.Fatalf("seed attached sentinel: %v", err)
	}
	if err := mcp.WriteAttachState(wtStale, mcp.AttachState{V: 1, OwnerPID: myPID, OwnerStartTime: 1 /* bogus */, StartedAt: now, LockPath: ".niwa/attach.lock"}); err != nil {
		t.Fatalf("seed stale sentinel: %v", err)
	}
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	resetSessionListFlags(t)
	sessionListStatus = "active"
	defer resetSessionListFlags(t)

	var buf bytes.Buffer
	sessionListCmd.SetOut(&buf)
	defer sessionListCmd.SetOut(os.Stdout)
	if err := runSessionList(sessionListCmd, nil); err != nil {
		t.Fatalf("runSessionList: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "available") || !strings.Contains(out, "attached") {
		t.Errorf("expected both available and attached values in output:\n%s", out)
	}
	// stale-sentinel rows: the listing pass reaps the dead-holder sentinel
	// (reapStale=true), so the row renders as 'available' afterwards rather
	// than 'stale'. Verify that the sentinel was actually removed from disk.
	if _, err := os.Stat(mcp.AttachStatePath(wtStale)); !os.IsNotExist(err) {
		t.Errorf("stale sentinel was not reaped during list: %v", err)
	}
}

func TestSessionList_AttachedFirstSort(t *testing.T) {
	now := time.Now().UTC()
	older := now.Add(-1 * time.Hour).Format(time.RFC3339)
	newer := now.Format(time.RFC3339)
	wtAttached := filepath.Join(t.TempDir(), "wt-attached")
	wtNewer := filepath.Join(t.TempDir(), "wt-newer")
	wtOlder := filepath.Join(t.TempDir(), "wt-older")
	for _, p := range []string{wtAttached, wtNewer, wtOlder} {
		_ = os.MkdirAll(filepath.Join(p, ".niwa"), 0o700)
	}
	root := seedLifecycleSessions(t, []mcp.SessionLifecycleState{
		// Attached session is OLDEST but should sort first because of attached-first rule.
		{V: 1, SessionID: "aaaaaaaa", Repo: "niwa", Status: mcp.SessionStatusActive, WorktreePath: wtAttached, CreationTime: older},
		{V: 1, SessionID: "bbbbbbbb", Repo: "niwa", Status: mcp.SessionStatusActive, WorktreePath: wtNewer, CreationTime: newer},
		{V: 1, SessionID: "cccccccc", Repo: "niwa", Status: mcp.SessionStatusActive, WorktreePath: wtOlder, CreationTime: older},
	})
	myPID := os.Getpid()
	myStart, _ := mcp.PIDStartTime(myPID)
	if err := mcp.WriteAttachState(wtAttached, mcp.AttachState{V: 1, OwnerPID: myPID, OwnerStartTime: myStart, StartedAt: newer, LockPath: ".niwa/attach.lock"}); err != nil {
		t.Fatalf("seed attached: %v", err)
	}
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	resetSessionListFlags(t)
	sessionListStatus = "active"
	defer resetSessionListFlags(t)

	var buf bytes.Buffer
	sessionListCmd.SetOut(&buf)
	defer sessionListCmd.SetOut(os.Stdout)
	if err := runSessionList(sessionListCmd, nil); err != nil {
		t.Fatalf("runSessionList: %v", err)
	}
	out := buf.String()
	posA := strings.Index(out, "aaaaaaaa")
	posB := strings.Index(out, "bbbbbbbb")
	posC := strings.Index(out, "cccccccc")
	if posA < 0 || posB < 0 || posC < 0 {
		t.Fatalf("not all rows present: %s", out)
	}
	// aaaaaaaa is attached -> first.
	if posA > posB || posA > posC {
		t.Errorf("attached row should be first; positions A=%d B=%d C=%d in:\n%s", posA, posB, posC, out)
	}
	// bbbbbbbb (newer) should come before cccccccc (older).
	if posB > posC {
		t.Errorf("newer row should come before older; B=%d C=%d", posB, posC)
	}
}

func TestSessionList_AttachedFilter(t *testing.T) {
	wtFree := filepath.Join(t.TempDir(), "wt-free")
	wtAttached := filepath.Join(t.TempDir(), "wt-attached")
	for _, p := range []string{wtFree, wtAttached} {
		_ = os.MkdirAll(filepath.Join(p, ".niwa"), 0o700)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	root := seedLifecycleSessions(t, []mcp.SessionLifecycleState{
		{V: 1, SessionID: "11111111", Repo: "niwa", Status: mcp.SessionStatusActive, WorktreePath: wtFree, CreationTime: now},
		{V: 1, SessionID: "22222222", Repo: "niwa", Status: mcp.SessionStatusActive, WorktreePath: wtAttached, CreationTime: now},
	})
	myPID := os.Getpid()
	myStart, _ := mcp.PIDStartTime(myPID)
	_ = mcp.WriteAttachState(wtAttached, mcp.AttachState{V: 1, OwnerPID: myPID, OwnerStartTime: myStart, StartedAt: now, LockPath: ".niwa/attach.lock"})
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	resetSessionListFlags(t)
	sessionListAttached = true
	defer resetSessionListFlags(t)

	var buf bytes.Buffer
	sessionListCmd.SetOut(&buf)
	defer sessionListCmd.SetOut(os.Stdout)
	if err := runSessionList(sessionListCmd, nil); err != nil {
		t.Fatalf("runSessionList: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "22222222") {
		t.Errorf("attached row missing under --attached: %s", out)
	}
	if strings.Contains(out, "11111111") {
		t.Errorf("non-attached row present under --attached: %s", out)
	}
}

func TestSessionList_AvailableFilter(t *testing.T) {
	wtFree := filepath.Join(t.TempDir(), "wt-free")
	wtAttached := filepath.Join(t.TempDir(), "wt-attached")
	for _, p := range []string{wtFree, wtAttached} {
		_ = os.MkdirAll(filepath.Join(p, ".niwa"), 0o700)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	root := seedLifecycleSessions(t, []mcp.SessionLifecycleState{
		{V: 1, SessionID: "11111111", Repo: "niwa", Status: mcp.SessionStatusActive, WorktreePath: wtFree, CreationTime: now},
		{V: 1, SessionID: "22222222", Repo: "niwa", Status: mcp.SessionStatusActive, WorktreePath: wtAttached, CreationTime: now},
	})
	myPID := os.Getpid()
	myStart, _ := mcp.PIDStartTime(myPID)
	_ = mcp.WriteAttachState(wtAttached, mcp.AttachState{V: 1, OwnerPID: myPID, OwnerStartTime: myStart, StartedAt: now, LockPath: ".niwa/attach.lock"})
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	resetSessionListFlags(t)
	sessionListAvailable = true
	defer resetSessionListFlags(t)

	var buf bytes.Buffer
	sessionListCmd.SetOut(&buf)
	defer sessionListCmd.SetOut(os.Stdout)
	if err := runSessionList(sessionListCmd, nil); err != nil {
		t.Fatalf("runSessionList: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "11111111") {
		t.Errorf("available row missing under --available: %s", out)
	}
	if strings.Contains(out, "22222222") {
		t.Errorf("attached row present under --available: %s", out)
	}
}

func TestSessionList_AttachedAndAvailableMutuallyExclusive(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".niwa", "instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	resetSessionListFlags(t)
	sessionListAttached = true
	sessionListAvailable = true
	defer resetSessionListFlags(t)

	err := runSessionList(sessionListCmd, nil)
	if err == nil {
		t.Fatalf("want mutual-exclusion error, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error message = %q, want substring 'mutually exclusive'", err.Error())
	}
}

