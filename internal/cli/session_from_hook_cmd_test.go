package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/worktree"
)

// runFromHook invokes runSessionFromHook with the given hook JSON piped on
// stdin, from inside fromDir (so the create path's cwd resolution and the
// remove path's resolveInstanceRoot can find the instance). It returns
// captured stdout+stderr (separately) and the RunE error.
func runFromHook(t *testing.T, fromDir, hookJSON string) (stdout, stderr string, err error) {
	t.Helper()
	prev, gerr := os.Getwd()
	if gerr != nil {
		t.Fatalf("getwd: %v", gerr)
	}
	if cerr := os.Chdir(fromDir); cerr != nil {
		t.Fatalf("chdir %s: %v", fromDir, cerr)
	}
	defer func() { _ = os.Chdir(prev) }()

	var outBuf, errBuf bytes.Buffer
	sessionFromHookCmd.SetIn(strings.NewReader(hookJSON))
	sessionFromHookCmd.SetOut(&outBuf)
	sessionFromHookCmd.SetErr(&errBuf)
	defer func() {
		sessionFromHookCmd.SetIn(os.Stdin)
		sessionFromHookCmd.SetOut(os.Stdout)
		sessionFromHookCmd.SetErr(os.Stderr)
	}()

	runErr := runSessionFromHook(sessionFromHookCmd, nil)
	return outBuf.String(), errBuf.String(), runErr
}

// TestFromHookCreate_ValidPrintsPathExitsZero verifies a WorktreeCreate hook in
// a known repo prints ONLY the absolute worktree path to stdout and succeeds.
func TestFromHookCreate_ValidPrintsPathExitsZero(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeCreate",
		"session_id":      "claude-sid-123",
		"cwd":             f.repoPath,
		"name":            "fix-the-bug",
	})

	out, errOut, err := runFromHook(t, f.repoPath, hookJSON)
	if err != nil {
		t.Fatalf("from-hook create: %v\nstderr:\n%s", err, errOut)
	}

	// stdout must be exactly the worktree path (single line).
	printed := strings.TrimSpace(out)
	if !filepath.IsAbs(printed) {
		t.Fatalf("stdout %q is not an absolute path", printed)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("stdout should be a single path line, got:\n%q", out)
	}
	if _, statErr := os.Stat(printed); statErr != nil {
		t.Errorf("printed worktree path %q does not exist: %v", printed, statErr)
	}

	// A session must have been recorded with the derived purpose.
	states, err := worktree.ListSessionLifecycleStates(f.sessionsDir)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("want exactly 1 session recorded, got %d", len(states))
	}
	if states[0].Purpose != "fix-the-bug" {
		t.Errorf("purpose = %q, want %q", states[0].Purpose, "fix-the-bug")
	}
	if states[0].WorktreePath != printed {
		t.Errorf("recorded WorktreePath %q != printed %q", states[0].WorktreePath, printed)
	}
}

// TestFromHookCreate_CwdOutsideWorkspaceFails verifies a cwd that resolves
// under no workspace repo is rejected (non-zero exit) and creates no worktree.
func TestFromHookCreate_CwdOutsideWorkspaceFails(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	// A directory that is NOT under any workspace repo. Use a sibling temp dir
	// outside the instance root entirely.
	outside := t.TempDir()

	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeCreate",
		"session_id":      "claude-sid-123",
		"cwd":             outside,
		"name":            "should-not-create",
	})

	out, _, err := runFromHook(t, f.repoPath, hookJSON)
	if err == nil {
		t.Fatalf("want non-zero (error) for out-of-workspace cwd, got nil; stdout=%q", out)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("no path should be printed on failure, got stdout:\n%q", out)
	}

	// No session should have been recorded.
	states, lerr := worktree.ListSessionLifecycleStates(f.sessionsDir)
	if lerr != nil {
		t.Fatalf("list sessions: %v", lerr)
	}
	if len(states) != 0 {
		t.Errorf("want 0 sessions after rejected create, got %d", len(states))
	}
}

// TestFromHookCreate_ControlCharsInNameSanitized verifies control characters in
// the untrusted name are stripped before the name becomes the session purpose,
// and creation still succeeds.
func TestFromHookCreate_ControlCharsInNameSanitized(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	// Embedded NUL, newline, carriage return, BEL, and a C1 control char.
	dirty := "clean\x00part\nsecond\rline\x07\x9b"
	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeCreate",
		"session_id":      "claude-sid-123",
		"cwd":             f.repoPath,
		"name":            dirty,
	})

	out, errOut, err := runFromHook(t, f.repoPath, hookJSON)
	if err != nil {
		t.Fatalf("from-hook create: %v\nstderr:\n%s", err, errOut)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("expected a worktree path on stdout")
	}

	states, err := worktree.ListSessionLifecycleStates(f.sessionsDir)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("want 1 session, got %d", len(states))
	}
	got := states[0].Purpose
	// The sanitized purpose must contain no control characters.
	for _, r := range got {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("purpose retained a control char %#x: %q", r, got)
		}
	}
	// The printable content survives (the exact joining of segments is an
	// implementation detail; assert the printable substrings are present).
	for _, want := range []string{"clean", "part", "second", "line"} {
		if !strings.Contains(got, want) {
			t.Errorf("sanitized purpose %q missing printable segment %q", got, want)
		}
	}
}

// TestFromHookRemove_CleanWorktreeDestroyed verifies a WorktreeRemove hook,
// mapped to a niwa session by worktree_path, destroys a clean worktree and
// exits 0.
func TestFromHookRemove_CleanWorktreeDestroyed(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	// Create a real worktree via the create hook so remove has a genuine
	// session to reconcile.
	created := createViaHook(t, f, "to-be-removed")

	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeRemove",
		"session_id":      "claude-different-sid",
		"worktree_path":   created.WorktreePath,
	})

	_, errOut, err := runFromHook(t, f.repoPath, hookJSON)
	if err != nil {
		t.Fatalf("from-hook remove returned error (must always exit 0): %v\nstderr:\n%s", err, errOut)
	}

	state, err := worktree.ReadSessionLifecycleState(f.sessionsDir, created.SessionID)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state.Status != worktree.SessionStatusEnded {
		t.Errorf("status = %q, want ended after clean remove", state.Status)
	}
	if _, statErr := os.Stat(created.WorktreePath); !os.IsNotExist(statErr) {
		t.Errorf("worktree dir still present after remove: %v", statErr)
	}
}

// TestFromHookRemove_DirtyWorktreeRetained verifies a WorktreeRemove hook does
// NOT delete a worktree holding genuine uncommitted work; it logs-and-retains
// and still exits 0.
func TestFromHookRemove_DirtyWorktreeRetained(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	created := createViaHook(t, f, "dirty-work")

	// Introduce genuine uncommitted work (a tracked-pattern file git status
	// reports as untracked). niwa scaffolding is git-excluded, so we need a
	// non-excluded file to make the worktree genuinely dirty.
	dirtyFile := filepath.Join(created.WorktreePath, "uncommitted.txt")
	if err := os.WriteFile(dirtyFile, []byte("work in progress\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeRemove",
		"session_id":      "claude-different-sid",
		"worktree_path":   created.WorktreePath,
	})

	_, errOut, err := runFromHook(t, f.repoPath, hookJSON)
	if err != nil {
		t.Fatalf("from-hook remove returned error (must always exit 0): %v", err)
	}

	// The worktree must still exist (retained, not deleted).
	if _, statErr := os.Stat(created.WorktreePath); statErr != nil {
		t.Errorf("dirty worktree was deleted (should be retained): %v", statErr)
	}
	// The session must NOT be terminal (the guarded destroy refused).
	state, err := worktree.ReadSessionLifecycleState(f.sessionsDir, created.SessionID)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state.Status == worktree.SessionStatusEnded {
		t.Errorf("session marked ended despite dirty-retain; status=%q", state.Status)
	}
	// A retain notice should have been logged.
	if !strings.Contains(errOut, "retaining it") {
		t.Errorf("expected a retain notice on stderr, got:\n%s", errOut)
	}
}

// TestFromHookRemove_UnknownPathExitsZero verifies an unknown worktree path is
// non-blocking: the hook logs a warning and exits 0 without error.
func TestFromHookRemove_UnknownPathExitsZero(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	unknown := filepath.Join(t.TempDir(), "never-a-session")

	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeRemove",
		"session_id":      "claude-different-sid",
		"worktree_path":   unknown,
	})

	_, errOut, err := runFromHook(t, f.repoPath, hookJSON)
	if err != nil {
		t.Fatalf("from-hook remove must exit 0 for unknown path, got error: %v", err)
	}
	if !strings.Contains(errOut, "no niwa session for worktree") {
		t.Errorf("expected a warning logged for unknown path, got:\n%s", errOut)
	}
}

// TestFromHookRemove_FallsBackToCwd verifies that when worktree_path is absent,
// the remove path uses cwd to map the worktree to a session.
func TestFromHookRemove_FallsBackToCwd(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	created := createViaHook(t, f, "cwd-fallback")

	// No worktree_path; only cwd carries the worktree directory.
	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeRemove",
		"session_id":      "claude-different-sid",
		"cwd":             created.WorktreePath,
	})

	_, errOut, err := runFromHook(t, f.repoPath, hookJSON)
	if err != nil {
		t.Fatalf("from-hook remove (cwd fallback): %v\nstderr:\n%s", err, errOut)
	}
	state, err := worktree.ReadSessionLifecycleState(f.sessionsDir, created.SessionID)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state.Status != worktree.SessionStatusEnded {
		t.Errorf("cwd-fallback remove did not end session; status=%q", state.Status)
	}
}

// TestFromHook_UnknownEventErrors verifies an unrecognized hook_event_name is a
// hard error (non-zero exit), not a silent success.
func TestFromHook_UnknownEventErrors(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "SomethingElse",
		"cwd":             f.repoPath,
	})

	_, _, err := runFromHook(t, f.repoPath, hookJSON)
	if err == nil {
		t.Fatalf("want error for unknown hook_event_name, got nil")
	}
}

// createViaHook drives a WorktreeCreate hook end-to-end and returns the created
// session's id and worktree path by reading back the recorded state.
func createViaHook(t *testing.T, f *createFlowFixture, name string) worktree.SessionLifecycleState {
	t.Helper()
	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeCreate",
		"session_id":      "claude-sid",
		"cwd":             f.repoPath,
		"name":            name,
	})
	out, errOut, err := runFromHook(t, f.repoPath, hookJSON)
	if err != nil {
		t.Fatalf("createViaHook: %v\nstderr:\n%s", err, errOut)
	}
	printed := strings.TrimSpace(out)

	states, err := worktree.ListSessionLifecycleStates(f.sessionsDir)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	for _, st := range states {
		if st.WorktreePath == printed {
			return st
		}
	}
	t.Fatalf("createViaHook: no session recorded for printed path %q", printed)
	return worktree.SessionLifecycleState{}
}

// mustHookJSON marshals a hook payload map to a JSON string.
func mustHookJSON(t *testing.T, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal hook json: %v", err)
	}
	return string(b)
}

// TestReconcileFailedHookCreate_CleanRollsBack covers design Decision 8's
// normal case: a delegated create that fails past `git worktree add` must leave
// no worktree, no session branch, and no `active` session record behind, so
// `niwa worktree list` never reports an active worktree no process is in.
func TestReconcileFailedHookCreate_CleanRollsBack(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	created := createViaHook(t, f, "will-fail")

	var stderr bytes.Buffer
	cause := errors.New("promoted key \"GH_TOKEN\" not found in resolved env vars")
	err := reconcileFailedHookCreate(&stderr, f.root, created.SessionID, cause)

	if err == nil {
		t.Fatal("reconcileFailedHookCreate must return an error so the hook exits non-zero")
	}
	// The original cause must survive so the developer sees WHY it failed.
	if !errors.Is(err, cause) {
		t.Errorf("error %v must wrap the original cause", err)
	}
	// ...and the reconciliation outcome must be named so they know what was
	// left behind.
	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("error %q should say the partial worktree was rolled back", err)
	}

	if _, statErr := os.Stat(created.WorktreePath); !os.IsNotExist(statErr) {
		t.Errorf("worktree %s should have been removed, stat err = %v", created.WorktreePath, statErr)
	}

	state, readErr := worktree.ReadSessionLifecycleState(f.sessionsDir, created.SessionID)
	if readErr != nil {
		t.Fatalf("read state: %v", readErr)
	}
	if state.Status == worktree.SessionStatusActive {
		t.Errorf("session %s left active after a failed create; status=%q", created.SessionID, state.Status)
	}

	// The session branch was created at HEAD with no commits, so the guarded
	// delete inside DestroySession should have removed it.
	out, _ := exec.Command("git", "-C", f.repoPath, "branch", "--list", created.BranchName).CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("session branch %q should have been deleted, git branch --list gave %q",
			created.BranchName, out)
	}
}

// TestReconcileFailedHookCreate_DirtyRetains covers the exception Decision 8
// carves out. A failed create is almost never dirty — every file niwa writes is
// git-excluded and the agent never enters the directory — but a workspace-
// authored worktree apply script can touch tracked files before a later step
// fails. That is the case Decision 3 says to retain, so the dirty guard stays
// armed here rather than being forced past.
func TestReconcileFailedHookCreate_DirtyRetains(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	created := createViaHook(t, f, "dirty-on-failure")

	dirtyFile := filepath.Join(created.WorktreePath, "uncommitted.txt")
	if err := os.WriteFile(dirtyFile, []byte("a hook script wrote this\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	var stderr bytes.Buffer
	cause := errors.New("content install blew up")
	err := reconcileFailedHookCreate(&stderr, f.root, created.SessionID, cause)

	if err == nil {
		t.Fatal("reconcileFailedHookCreate must return an error so the hook exits non-zero")
	}
	if !errors.Is(err, cause) {
		t.Errorf("error %v must wrap the original cause", err)
	}
	if _, statErr := os.Stat(created.WorktreePath); statErr != nil {
		t.Errorf("dirty worktree was deleted (should be retained): %v", statErr)
	}
	if !strings.Contains(stderr.String(), "retaining it") {
		t.Errorf("expected a retain notice on stderr, got:\n%s", stderr.String())
	}
	if !strings.Contains(err.Error(), "retained") {
		t.Errorf("error %q should say the worktree was retained", err)
	}
}

// TestFromHookCreate_ContentInstallFailureLeavesNoActiveSession drives a REAL
// content-install failure through runFromHookCreate, rather than calling the
// reconciliation helper directly. This is the test that actually guards the
// wiring: without the reconcile call in the create path it fails, because the
// worktree and an active session record both survive.
//
// The failure is induced by declaring a repo content source that does not
// exist, so ApplyToWorktree fails after CreateSession has already run
// `git worktree add` and written the session record.
func TestFromHookCreate_ContentInstallFailureLeavesNoActiveSession(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	// Re-point the workspace config at a content source that is not on disk.
	wsTOML := "[workspace]\nname = \"testws\"\ncontent_dir = \"claude\"\n\n" +
		"[claude.content.repos." + f.repo + "]\nsource = \"repos/does-not-exist.md\"\n"
	if err := os.WriteFile(filepath.Join(f.root, ".niwa", "workspace.toml"), []byte(wsTOML), 0o644); err != nil {
		t.Fatalf("rewrite workspace.toml: %v", err)
	}

	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeCreate",
		"session_id":      "claude-sid-fail",
		"cwd":             f.repoPath,
		"name":            "doomed",
	})

	out, errOut, err := runFromHook(t, f.repoPath, hookJSON)
	if err == nil {
		t.Fatalf("expected the hook to fail; stdout=%q stderr=%q", out, errOut)
	}
	// The hook contract is that stdout carries ONLY a worktree path. On failure
	// it must carry nothing, or Claude Code would chdir into a path that the
	// rollback just deleted.
	if strings.TrimSpace(out) != "" {
		t.Errorf("failed create must print no worktree path, got %q", out)
	}

	states, listErr := worktree.ListSessionLifecycleStates(f.sessionsDir)
	if listErr != nil {
		t.Fatalf("list sessions: %v", listErr)
	}
	for _, st := range states {
		if st.Status == worktree.SessionStatusActive {
			t.Errorf("session %s left active after a failed create (worktree %s)", st.SessionID, st.WorktreePath)
		}
		if st.WorktreePath != "" {
			if _, statErr := os.Stat(st.WorktreePath); !os.IsNotExist(statErr) {
				t.Errorf("worktree %s survived a failed create, stat err = %v", st.WorktreePath, statErr)
			}
		}
	}

	// No stray git worktree registration should remain either.
	listOut, _ := exec.Command("git", "-C", f.repoPath, "worktree", "list").CombinedOutput()
	if strings.Count(strings.TrimSpace(string(listOut)), "\n") != 0 {
		t.Errorf("expected only the main checkout registered, got:\n%s", listOut)
	}
}
