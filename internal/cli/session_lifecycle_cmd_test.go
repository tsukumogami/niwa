package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// seedLifecycleSessions writes N session lifecycle state files plus their
// worktree directories under instanceRoot. The caller can then exercise
// runSessionLifecycleList against the same instance root. Returns the
// instance root path.
func seedLifecycleSessions(t *testing.T, sessions []worktree.SessionLifecycleState) string {
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
		if err := worktree.WriteSessionLifecycleState(sessionsDir, s); err != nil {
			t.Fatalf("write lifecycle state %s: %v", s.SessionID, err)
		}
	}
	return root
}

func TestSessionList_AvailabilityColumnHeader(t *testing.T) {
	root := seedLifecycleSessions(t, []worktree.SessionLifecycleState{
		{
			V: 1, SessionID: "11111111", Repo: "niwa", Status: worktree.SessionStatusActive,
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
	root := seedLifecycleSessions(t, []worktree.SessionLifecycleState{
		{V: 1, SessionID: "11111111", Repo: "niwa", Status: worktree.SessionStatusActive, WorktreePath: wtFree, CreationTime: now},
		{V: 1, SessionID: "22222222", Repo: "niwa", Status: worktree.SessionStatusActive, WorktreePath: wtAttached, CreationTime: now},
		{V: 1, SessionID: "33333333", Repo: "niwa", Status: worktree.SessionStatusActive, WorktreePath: wtStale, CreationTime: now},
	})
	myPID := os.Getpid()
	myStart, _ := worktree.PIDStartTime(myPID)
	if err := worktree.WriteAttachState(wtAttached, worktree.AttachState{V: 1, OwnerPID: myPID, OwnerStartTime: myStart, StartedAt: now, LockPath: ".niwa/attach.lock"}); err != nil {
		t.Fatalf("seed attached sentinel: %v", err)
	}
	if err := worktree.WriteAttachState(wtStale, worktree.AttachState{V: 1, OwnerPID: myPID, OwnerStartTime: 1 /* bogus */, StartedAt: now, LockPath: ".niwa/attach.lock"}); err != nil {
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
	if _, err := os.Stat(worktree.AttachStatePath(wtStale)); !os.IsNotExist(err) {
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
	root := seedLifecycleSessions(t, []worktree.SessionLifecycleState{
		// Attached session is OLDEST but should sort first because of attached-first rule.
		{V: 1, SessionID: "aaaaaaaa", Repo: "niwa", Status: worktree.SessionStatusActive, WorktreePath: wtAttached, CreationTime: older},
		{V: 1, SessionID: "bbbbbbbb", Repo: "niwa", Status: worktree.SessionStatusActive, WorktreePath: wtNewer, CreationTime: newer},
		{V: 1, SessionID: "cccccccc", Repo: "niwa", Status: worktree.SessionStatusActive, WorktreePath: wtOlder, CreationTime: older},
	})
	myPID := os.Getpid()
	myStart, _ := worktree.PIDStartTime(myPID)
	if err := worktree.WriteAttachState(wtAttached, worktree.AttachState{V: 1, OwnerPID: myPID, OwnerStartTime: myStart, StartedAt: newer, LockPath: ".niwa/attach.lock"}); err != nil {
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
	root := seedLifecycleSessions(t, []worktree.SessionLifecycleState{
		{V: 1, SessionID: "11111111", Repo: "niwa", Status: worktree.SessionStatusActive, WorktreePath: wtFree, CreationTime: now},
		{V: 1, SessionID: "22222222", Repo: "niwa", Status: worktree.SessionStatusActive, WorktreePath: wtAttached, CreationTime: now},
	})
	myPID := os.Getpid()
	myStart, _ := worktree.PIDStartTime(myPID)
	_ = worktree.WriteAttachState(wtAttached, worktree.AttachState{V: 1, OwnerPID: myPID, OwnerStartTime: myStart, StartedAt: now, LockPath: ".niwa/attach.lock"})
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
	root := seedLifecycleSessions(t, []worktree.SessionLifecycleState{
		{V: 1, SessionID: "11111111", Repo: "niwa", Status: worktree.SessionStatusActive, WorktreePath: wtFree, CreationTime: now},
		{V: 1, SessionID: "22222222", Repo: "niwa", Status: worktree.SessionStatusActive, WorktreePath: wtAttached, CreationTime: now},
	})
	myPID := os.Getpid()
	myStart, _ := worktree.PIDStartTime(myPID)
	_ = worktree.WriteAttachState(wtAttached, worktree.AttachState{V: 1, OwnerPID: myPID, OwnerStartTime: myStart, StartedAt: now, LockPath: ".niwa/attach.lock"})
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

// TestRunSessionCreate_TooManyArgsReturnsUsageError verifies that passing more
// than the two optional positionals (repo, purpose) is a usage error: code 2
// with a usage string that points at --help. After Issue 1 both positionals
// are optional, so the only positional-count error is overflow.
func TestRunSessionCreate_TooManyArgsReturnsUsageError(t *testing.T) {
	err := runSessionCreate(sessionCreateCmd, []string{"repo", "purpose", "extra"})
	if err == nil {
		t.Fatalf("want ExitCodeError, got nil")
	}
	var ece *sessionattach.ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T", err)
	}
	if ece.Code != 2 {
		t.Errorf("Code = %d, want 2 (usage error)", ece.Code)
	}
	wantSubstrs := []string{
		"niwa: usage",
		"niwa worktree create",
		"niwa worktree create --help",
	}
	for _, s := range wantSubstrs {
		if !strings.Contains(ece.Msg, s) {
			t.Errorf("missing %q in usage message: %q", s, ece.Msg)
		}
	}
}

// TestRunSessionCreate_NoArgsCwdOutsideWorkspaceErrors verifies that a bare
// `niwa worktree create` (no repo arg) fails clearly when the process cwd does
// not resolve under any workspace repo. The instance root is a temp dir with no
// repos, so cwd inference must reject rather than fabricate a repo name.
func TestRunSessionCreate_NoArgsCwdOutsideWorkspaceErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir .niwa: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".niwa", "instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	err := runSessionCreate(sessionCreateCmd, nil)
	if err == nil {
		t.Fatalf("want error inferring repo from cwd, got nil")
	}
	if !strings.Contains(err.Error(), "infer repo from working directory") {
		t.Errorf("error = %q, want substring 'infer repo from working directory'", err.Error())
	}
}

// TestRunSessionDestroy_NoArgsReturnsUsageError verifies issue #135 behavior
// for destroy: usage error names <session-id> and points at
// `niwa worktree list` (not --status active, since destroy operates on any
// status).
func TestRunSessionDestroy_NoArgsReturnsUsageError(t *testing.T) {
	err := runSessionDestroy(sessionDestroyCmd, nil)
	if err == nil {
		t.Fatalf("want ExitCodeError, got nil")
	}
	var ece *sessionattach.ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T", err)
	}
	if ece.Code != 2 {
		t.Errorf("Code = %d, want 2 (usage error)", ece.Code)
	}
	wantSubstrs := []string{
		"niwa: usage",
		"niwa worktree destroy",
		"<session-id>",
		"[--force]",
		"niwa worktree list",
	}
	for _, s := range wantSubstrs {
		if !strings.Contains(ece.Msg, s) {
			t.Errorf("missing %q in usage message: %q", s, ece.Msg)
		}
	}
}

// TestSessionCommands_HaveCompletion verifies that the four session
// positional-arg subcommands all expose a ValidArgsFunction. This is a
// regression guard: removing the wiring should fail this test, since
// dropping completion is what issue #135 fixed.
func TestSessionCommands_HaveCompletion(t *testing.T) {
	cmds := []struct {
		name string
		fn   any
	}{
		{"session create", sessionCreateCmd.ValidArgsFunction},
		{"session destroy", sessionDestroyCmd.ValidArgsFunction},
		{"session attach", sessionAttachCmd.ValidArgsFunction},
		{"session detach", sessionDetachCmd.ValidArgsFunction},
	}
	for _, c := range cmds {
		if c.fn == nil {
			t.Errorf("%s: ValidArgsFunction is nil, want a completion function", c.name)
		}
	}
}
