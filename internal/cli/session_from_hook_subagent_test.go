package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/workspace"
	"github.com/tsukumogami/niwa/internal/worktree"
)

const testAgentID = "a0123456789abcdef"

// createAgentWorktree makes a worktree through the WorktreeCreate hook the way
// Claude Code's Agent tool does: named agent-<agent id>, with the parent
// session's transcript path in the payload.
func createAgentWorktree(t *testing.T, f *createFlowFixture, agentID, transcript string) worktree.SessionLifecycleState {
	t.Helper()
	t.Setenv("CLAUDE_PROJECT_DIR", f.root)
	out, errOut, err := runFromHook(t, f.root, mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeCreate",
		"session_id":      "claude-sid",
		"transcript_path": transcript,
		"cwd":             f.root,
		"name":            "agent-" + agentID,
	}))
	if err != nil {
		t.Fatalf("create: %v\nstderr:\n%s", err, errOut)
	}
	printed := strings.TrimSpace(out)
	states, err := worktree.ListSessionLifecycleStates(f.sessionsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range states {
		if st.WorktreePath == printed {
			return st
		}
	}
	t.Fatalf("no session recorded for %q", printed)
	return worktree.SessionLifecycleState{}
}

func subagentStop(t *testing.T, f *createFlowFixture, agentID, cwd string) string {
	t.Helper()
	t.Setenv("CLAUDE_PROJECT_DIR", f.root)
	out, errOut, err := runFromHook(t, f.root, mustHookJSON(t, map[string]any{
		"hook_event_name":  "SubagentStop",
		"session_id":       "claude-sid",
		"cwd":              cwd,
		"agent_id":         agentID,
		"agent_type":       "general-purpose",
		"stop_hook_active": false,
	}))
	if err != nil || out != "" {
		t.Fatalf("SubagentStop must exit 0 with empty stdout: err=%v out=%q\nstderr:\n%s", err, out, errOut)
	}
	return errOut
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func assertRemoved(t *testing.T, f *createFlowFixture, st worktree.SessionLifecycleState) {
	t.Helper()
	got, err := worktree.ReadSessionLifecycleState(f.sessionsDir, st.SessionID)
	if err != nil || got.Status != worktree.SessionStatusEnded {
		t.Errorf("status = %q (%v), want ended", got.Status, err)
	}
	if _, err := os.Stat(st.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("worktree %s still present: %v", st.WorktreePath, err)
	}
	if out := gitIn(t, f.repoPath, "branch", "--list", st.BranchName); out != "" {
		t.Errorf("branch %s still present", st.BranchName)
	}
}

func assertKept(t *testing.T, f *createFlowFixture, st worktree.SessionLifecycleState) {
	t.Helper()
	got, err := worktree.ReadSessionLifecycleState(f.sessionsDir, st.SessionID)
	if err != nil || got.Status != worktree.SessionStatusActive {
		t.Errorf("status = %q (%v), want active", got.Status, err)
	}
	if _, err := os.Stat(st.WorktreePath); err != nil {
		t.Errorf("worktree %s was removed: %v", st.WorktreePath, err)
	}
	if out := gitIn(t, f.repoPath, "branch", "--list", st.BranchName); out == "" {
		t.Errorf("branch %s was deleted", st.BranchName)
	}
}

// TestFromHookCreate_RecordsTeardownFields pins what the hook records for
// later teardown: the marker, the base commit and the transcript path.
func TestFromHookCreate_RecordsTeardownFields(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	transcript := filepath.Join(t.TempDir(), "parent.jsonl")

	st := createAgentWorktree(t, f, testAgentID, transcript)
	if st.CreatedBy != worktree.CreatedByWorktreeHook {
		t.Errorf("CreatedBy = %q", st.CreatedBy)
	}
	if want := gitIn(t, f.repoPath, "rev-parse", "HEAD"); st.BaseCommit != want {
		t.Errorf("BaseCommit = %q, want %q", st.BaseCommit, want)
	}
	if st.ClaudeTranscriptPath != transcript {
		t.Errorf("ClaudeTranscriptPath = %q, want %q", st.ClaudeTranscriptPath, transcript)
	}
	// The fingerprints are of what niwa left ignored after setting the
	// worktree up.
	want, err := worktree.FingerprintIgnoredFiles(context.Background(), st.WorktreePath, worktree.StdGitInvoker{})
	if err != nil {
		t.Fatal(err)
	}
	if st.IgnoredFingerprints == nil || !reflect.DeepEqual(st.IgnoredFingerprints.Files, want.Files) {
		t.Errorf("IgnoredFingerprints = %+v, want %+v", st.IgnoredFingerprints, want)
	}
}

// seedIgnoredFile gives the worktree an ignored file as if niwa's setup had
// written it, by writing it and recording the fingerprints again the way the
// hook does at the end of setup.
func seedIgnoredFile(t *testing.T, f *createFlowFixture, st worktree.SessionLifecycleState, name, content string) worktree.SessionLifecycleState {
	t.Helper()
	if err := os.WriteFile(filepath.Join(st.WorktreePath, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	recordIgnoredBaseline(io.Discard, f.root, st.SessionID, st.WorktreePath)
	got, err := worktree.ReadSessionLifecycleState(f.sessionsDir, st.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IgnoredFingerprints == nil || !strings.HasPrefix(got.IgnoredFingerprints.Files[name], "sha256:") {
		t.Fatalf("seeded file %s has no fingerprint: %+v", name, got.IgnoredFingerprints)
	}
	return got
}

// TestFromHookSubagentStop_PreExistingIgnoredFiles covers an ignored file that
// was there when niwa set the worktree up: an edit or a deletion keeps the
// worktree, and the notice says which file; rewriting it with the same bytes
// is no change, so the worktree goes.
func TestFromHookSubagentStop_PreExistingIgnoredFiles(t *testing.T) {
	cases := []struct {
		name    string
		change  func(t *testing.T, path string)
		removed bool
		reason  string
	}{
		{"edited", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(`{"permissions":{"allow":["Bash"]}}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}, false, "has changed: settings.local.json"},
		{"deleted", func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}, false, "has been deleted: settings.local.json"},
		{"rewritten with the same bytes", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
				t.Fatal(err)
			}
			later := time.Now().Add(time.Hour)
			if err := os.Chtimes(path, later, later); err != nil {
				t.Fatal(err)
			}
		}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCreateFlowFixture(t)
			resetSessionCreateFlags(t)
			defer resetSessionCreateFlags(t)

			st := createAgentWorktree(t, f, testAgentID, "")
			st = seedIgnoredFile(t, f, st, "settings.local.json", `{}`)
			tc.change(t, filepath.Join(st.WorktreePath, "settings.local.json"))
			errOut := subagentStop(t, f, testAgentID, st.WorktreePath)
			if tc.removed {
				assertRemoved(t, f, st)
				return
			}
			assertKept(t, f, st)
			if !strings.Contains(errOut, "keeping worktree") || !strings.Contains(errOut, tc.reason) {
				t.Errorf("stderr should say it kept the worktree because %q:\n%s", tc.reason, errOut)
			}
		})
	}
}

// TestAutomaticTeardown_RequiresFingerprints checks that a record without
// ignored-file fingerprints, as one written by an earlier niwa is, is never
// removed by either path, unchanged worktree or not.
func TestAutomaticTeardown_RequiresFingerprints(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	t.Setenv("NIWA_REAP_IDLE_GRACE", "1h")

	st := createAgentWorktree(t, f, testAgentID, filepath.Join(t.TempDir(), "gone.jsonl"))
	got, err := worktree.ReadSessionLifecycleState(f.sessionsDir, st.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	got.IgnoredFingerprints = nil
	if err := worktree.WriteSessionLifecycleState(f.sessionsDir, got); err != nil {
		t.Fatal(err)
	}
	backdate(t, f, st, 2*time.Hour)

	errOut := subagentStop(t, f, testAgentID, st.WorktreePath)
	assertKept(t, f, st)
	if !strings.Contains(errOut, "no fingerprints of its ignored files") {
		t.Errorf("stderr should name the missing fingerprints:\n%s", errOut)
	}
	var stderr bytes.Buffer
	if n := sweepHookWorktreesIn([]string{f.root}, time.Now(), &stderr); n != 0 {
		t.Errorf("sweep removed %d worktrees without fingerprints", n)
	}
	assertKept(t, f, st)
}

// TestFromHookSubagentStop_RemovesUnchangedWorktree covers the case the hook
// exists for: an isolated subagent that changed nothing finishes, and its
// worktree, branch and record go, including when it had cd'd below the root.
func TestFromHookSubagentStop_RemovesUnchangedWorktree(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	commitGitignore(t, f, "build/\n")

	st := createAgentWorktree(t, f, testAgentID, "")
	sub := filepath.Join(st.WorktreePath, ".niwa")
	subagentStop(t, f, testAgentID, sub)
	assertRemoved(t, f, st)
}

// TestFromHookSubagentStop_KeepsChangedWorktree pins that a worktree with
// anything in it is never removed: untracked or staged files, an ignored file
// that wasn't there when niwa set it up (under a .gitignore pattern or under
// niwa's own *.local* pattern), a commit, a branch moved under a detached
// HEAD, or a stash made from its branch.
func TestFromHookSubagentStop_KeepsChangedWorktree(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, wt string)
		reason string
	}{
		{"file only a .gitignore hides", func(t *testing.T, wt string) {
			if err := os.MkdirAll(filepath.Join(wt, "build"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(wt, "build", "out.txt"))
		}, "weren't there when niwa set it up, such as build/out.txt"},
		{"file niwa's own *.local* exclude pattern hides", func(t *testing.T, wt string) {
			writeFile(t, filepath.Join(wt, ".env.local"))
		}, "weren't there when niwa set it up, such as .env.local"},
		{"branch moved under a detached HEAD", func(t *testing.T, wt string) {
			gitIn(t, wt, "commit", "--allow-empty", "-m", "work")
			gitIn(t, wt, "checkout", "--detach", "HEAD~1")
		}, "has moved off the commit it started at"},
		{"untracked file", func(t *testing.T, wt string) {
			writeFile(t, filepath.Join(wt, "new.txt"))
		}, "uncommitted or untracked"},
		{"staged file", func(t *testing.T, wt string) {
			writeFile(t, filepath.Join(wt, "new.txt"))
			gitIn(t, wt, "add", "new.txt")
		}, "uncommitted or untracked"},
		{"commit", func(t *testing.T, wt string) {
			gitIn(t, wt, "commit", "--allow-empty", "-m", "work")
		}, "HEAD is no longer"},
		{"stash from its branch", func(t *testing.T, wt string) {
			writeFile(t, filepath.Join(wt, "new.txt"))
			gitIn(t, wt, "stash", "push", "-u", "-m", "saved")
		}, "stash entry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCreateFlowFixture(t)
			resetSessionCreateFlags(t)
			defer resetSessionCreateFlags(t)
			commitGitignore(t, f, "build/\n")

			st := createAgentWorktree(t, f, testAgentID, "")
			tc.change(t, st.WorktreePath)
			errOut := subagentStop(t, f, testAgentID, st.WorktreePath)
			assertKept(t, f, st)
			if !strings.Contains(errOut, "keeping worktree") || !strings.Contains(errOut, tc.reason) {
				t.Errorf("stderr should say it kept the worktree because of %q:\n%s", tc.reason, errOut)
			}
		})
	}
}

// TestFromHookSubagentStop_LeavesOtherWorktreesAlone covers the worktrees the
// hook must not touch: one made by `niwa worktree create` under the same
// name, and a hook-made worktree that a different agent (a subagent without
// isolation, whose cwd is its parent's worktree) stops in.
func TestFromHookSubagentStop_LeavesOtherWorktreesAlone(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	sid, wtPath, branch, err := worktree.CreateSession(context.Background(), worktree.CreateSessionParams{
		InstanceRoot: f.root,
		Repo:         f.repo,
		Purpose:      "agent-" + testAgentID,
		GitInvoker:   worktree.StdGitInvoker{},
	})
	if err != nil {
		t.Fatal(err)
	}
	manual := worktree.SessionLifecycleState{SessionID: sid, WorktreePath: wtPath, BranchName: branch}
	subagentStop(t, f, testAgentID, wtPath)
	assertKept(t, f, manual)

	parent := createAgentWorktree(t, f, "afedcba9876543210", "")
	subagentStop(t, f, testAgentID, parent.WorktreePath)
	assertKept(t, f, parent)

	// A subagent outside every niwa worktree, and one outside any instance.
	subagentStop(t, f, testAgentID, f.repoPath)
	subagentStop(t, f, testAgentID, t.TempDir())
	assertKept(t, f, parent)
}

// TestSweepHookWorktrees covers the reap backstop: an unchanged hook-made
// worktree whose record is older than the grace goes once its session's
// transcript is gone or idle past the grace. It stays while the transcript is
// fresh, when it has changes, when no transcript was recorded, and when the
// record itself is younger than the grace, since a transcript Claude Code
// hasn't written yet looks exactly like a gone one.
func TestSweepHookWorktrees(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	t.Setenv("NIWA_REAP_IDLE_GRACE", "1h")
	dir := t.TempDir()

	gone := createAgentWorktree(t, f, "a1111111111111111", filepath.Join(dir, "gone.jsonl"))

	idleTranscript := filepath.Join(dir, "idle.jsonl")
	writeFile(t, idleTranscript)
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(idleTranscript, old, old); err != nil {
		t.Fatal(err)
	}
	idle := createAgentWorktree(t, f, "a2222222222222222", idleTranscript)

	freshTranscript := filepath.Join(dir, "fresh.jsonl")
	writeFile(t, freshTranscript)
	fresh := createAgentWorktree(t, f, "a3333333333333333", freshTranscript)

	changed := createAgentWorktree(t, f, "a4444444444444444", filepath.Join(dir, "gone-too.jsonl"))
	writeFile(t, filepath.Join(changed.WorktreePath, "work.txt"))

	unknown := createAgentWorktree(t, f, "a5555555555555555", "")

	for _, st := range []worktree.SessionLifecycleState{gone, idle, fresh, changed, unknown} {
		backdate(t, f, st, 2*time.Hour)
	}
	young := createAgentWorktree(t, f, "a6666666666666666", filepath.Join(dir, "not-written-yet.jsonl"))

	// The fixture's root is the instance itself, so the sweep gets it as its
	// one instance.
	var stderr bytes.Buffer
	n := sweepHookWorktreesIn([]string{f.root}, time.Now(), &stderr)
	if n != 2 {
		t.Errorf("removed %d, want 2\nstderr:\n%s", n, stderr.String())
	}
	assertRemoved(t, f, gone)
	assertRemoved(t, f, idle)
	assertKept(t, f, fresh)
	assertKept(t, f, changed)
	assertKept(t, f, unknown)
	assertKept(t, f, young)
}

// TestSweepHookWorktrees_OptedOutInstance checks the sweep leaves an instance
// that opted out of worktree delegation alone, as the hooks do.
func TestSweepHookWorktrees_OptedOutInstance(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	t.Setenv("NIWA_REAP_IDLE_GRACE", "1h")

	st := createAgentWorktree(t, f, testAgentID, filepath.Join(t.TempDir(), "gone.jsonl"))
	backdate(t, f, st, 2*time.Hour)
	state, err := workspace.LoadState(f.root)
	if err != nil {
		t.Fatal(err)
	}
	state.NoWorktreeDelegation = true
	if err := workspace.SaveState(f.root, state); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	if n := sweepHookWorktreesIn([]string{f.root}, time.Now(), &stderr); n != 0 {
		t.Errorf("removed %d from an opted-out instance\nstderr:\n%s", n, stderr.String())
	}
	assertKept(t, f, st)
}

// commitGitignore commits a .gitignore to the fixture repo, so worktrees made
// afterwards start from a commit that has it.
func commitGitignore(t *testing.T, f *createFlowFixture, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.repoPath, ".gitignore"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.repoPath, "add", ".gitignore")
	gitIn(t, f.repoPath, "commit", "-m", "ignore")
}

// backdate moves a session record's creation time into the past.
func backdate(t *testing.T, f *createFlowFixture, st worktree.SessionLifecycleState, age time.Duration) {
	t.Helper()
	got, err := worktree.ReadSessionLifecycleState(f.sessionsDir, st.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	got.CreationTime = time.Now().Add(-age).UTC().Format(time.RFC3339)
	if err := worktree.WriteSessionLifecycleState(f.sessionsDir, got); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
