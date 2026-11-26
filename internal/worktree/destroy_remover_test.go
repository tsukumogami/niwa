package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// DestroySession takes a WorktreeRemover beside its GitInvoker and removes
// the worktree through it before git branch -d. The session record goes
// Ended only once that removal succeeds, so a removal that stops leaves the
// record Active for the next sweep to try again.
//
// These tests reach DestroySession through destroyWithRemover, which calls
// it by reflection: the same test file then builds before and after
// DestroySession takes the remover, and before it does, every test here
// fails on that, not on a build error.

var worktreeRemoverType = reflect.TypeOf((*WorktreeRemover)(nil)).Elem()

// destroyWithRemover calls DestroySession(ctx, root, sid, force, inv,
// remover). ok is false while DestroySession takes no WorktreeRemover as
// its last parameter.
func destroyWithRemover(ctx context.Context, root, sid string, force bool, inv GitInvoker, remover WorktreeRemover) (state SessionLifecycleState, err error, ok bool) {
	fn := reflect.ValueOf(DestroySession)
	ft := fn.Type()
	if ft.NumIn() != 6 || ft.In(5) != worktreeRemoverType {
		return SessionLifecycleState{}, nil, false
	}
	out := fn.Call([]reflect.Value{
		reflect.ValueOf(ctx),
		reflect.ValueOf(root),
		reflect.ValueOf(sid),
		reflect.ValueOf(force),
		reflect.ValueOf(&inv).Elem(),
		reflect.ValueOf(&remover).Elem(),
	})
	state = out[0].Interface().(SessionLifecycleState)
	if e := out[1].Interface(); e != nil {
		err = e.(error)
	}
	return state, err, true
}

// sequence is one ordered log the invoker and the remover both append to.
type sequence struct{ events []string }

// loggingInvoker runs the real git and logs each call's arguments.
type loggingInvoker struct{ seq *sequence }

func (l loggingInvoker) CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	l.seq.events = append(l.seq.events, "git "+strings.Join(args, " "))
	return exec.CommandContext(ctx, "git", args...)
}

// stubRemover logs the removal it was asked for. With fail set it removes
// nothing and returns an error naming the work tree, as the rmtree.go
// remover does when it stops; otherwise it removes the work tree and the
// admin directory.
type stubRemover struct {
	seq  *sequence
	fail bool
	got  *[3]string
}

func (s stubRemover) RemoveWorktree(instance, workTree, adminDir string) error {
	s.seq.events = append(s.seq.events, "remove "+workTree)
	*s.got = [3]string{instance, workTree, adminDir}
	if s.fail {
		return errors.New("removal stopped at " + workTree + ": changed during the pass")
	}
	if err := os.RemoveAll(workTree); err != nil {
		return err
	}
	return os.RemoveAll(adminDir)
}

// newRemoverSession makes an instance root holding one repository with a
// worktree made by CreateSession, and returns the root, the repository and
// the session id.
func newRemoverSession(t *testing.T) (root, repoPath, sid string) {
	t.Helper()
	if _, err := runCmd("git", "--version"); err != nil {
		t.Skip("git not available")
	}
	root = t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	repoPath = filepath.Join(root, "group", "myrepo")
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
	if err := os.MkdirAll(filepath.Join(root, ".niwa", "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	sid, _, _, err := CreateSession(context.Background(), CreateSessionParams{
		InstanceRoot: root,
		Repo:         "myrepo",
		Purpose:      "remover test",
		GitInvoker:   StdGitInvoker{},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return root, repoPath, sid
}

func registeredWorktrees(t *testing.T, repoPath string) string {
	t.Helper()
	out, err := runCmd("git", "-C", repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	return out
}

func TestDestroySession_TakesAWorktreeRemoverBesideItsInvoker(t *testing.T) {
	ft := reflect.TypeOf(DestroySession)
	if ft.NumIn() != 6 || ft.In(5) != worktreeRemoverType {
		t.Fatalf("DestroySession is %v; want a WorktreeRemover parameter after its GitInvoker", ft)
	}
}

func TestDestroySession_RemovesThroughTheRemoverBeforeBranchDelete(t *testing.T) {
	root, repoPath, sid := newRemoverSession(t)
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	before, err := ReadSessionLifecycleState(sessionsDir, sid)
	if err != nil {
		t.Fatal(err)
	}
	seq := &sequence{}
	var got [3]string
	state, err, ok := destroyWithRemover(context.Background(), root, sid, false, loggingInvoker{seq: seq}, stubRemover{seq: seq, got: &got})
	if !ok {
		t.Fatal("DestroySession takes no WorktreeRemover, so it can't remove the worktree through one")
	}
	if err != nil {
		t.Fatalf("DestroySession: %v\ncalls: %q", err, seq.events)
	}
	if state.Status != SessionStatusEnded {
		t.Errorf("status = %q, want ended", state.Status)
	}
	if got[1] != before.WorktreePath {
		t.Errorf("remover asked for work tree %q, want %q", got[1], before.WorktreePath)
	}
	if got[2] == "" || !strings.HasPrefix(got[2], filepath.Join(repoPath, ".git", "worktrees")+string(filepath.Separator)) {
		t.Errorf("remover asked for admin directory %q, want one under %s", got[2], filepath.Join(repoPath, ".git", "worktrees"))
	}
	removeAt, branchAt := -1, -1
	for i, e := range seq.events {
		if strings.HasPrefix(e, "git ") && strings.Contains(e, " worktree remove") {
			t.Errorf("DestroySession ran %q; with a remover it makes no git worktree remove call", e)
		}
		if strings.HasPrefix(e, "remove ") && removeAt < 0 {
			removeAt = i
		}
		if strings.Contains(e, " branch -d") && branchAt < 0 {
			branchAt = i
		}
	}
	if removeAt < 0 || branchAt < 0 || removeAt > branchAt {
		t.Errorf("calls %q; want the remover's removal before git branch -d", seq.events)
	}
	if _, err := os.Stat(before.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("worktree %s still present after the removal: %v", before.WorktreePath, err)
	}
}

func TestDestroySession_ARemovalThatStopsLeavesTheRecordActive(t *testing.T) {
	root, repoPath, sid := newRemoverSession(t)
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	before, err := ReadSessionLifecycleState(sessionsDir, sid)
	if err != nil {
		t.Fatal(err)
	}
	seq := &sequence{}
	var got [3]string
	_, err, ok := destroyWithRemover(context.Background(), root, sid, false, loggingInvoker{seq: seq}, stubRemover{seq: seq, fail: true, got: &got})
	if !ok {
		t.Fatal("DestroySession takes no WorktreeRemover, so a removal that stops can't be handed back to it")
	}
	if err == nil || !strings.Contains(err.Error(), before.WorktreePath) {
		t.Fatalf("DestroySession = %v; want an error naming %s", err, before.WorktreePath)
	}
	persisted, err := ReadSessionLifecycleState(sessionsDir, sid)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != SessionStatusActive {
		t.Errorf("record status = %q after a removal that stopped, want %q so the next sweep tries again", persisted.Status, SessionStatusActive)
	}
	if !strings.Contains(registeredWorktrees(t, repoPath), before.WorktreePath) {
		t.Errorf("worktree %s is no longer registered after a removal that stopped", before.WorktreePath)
	}
	for _, e := range seq.events {
		if strings.Contains(e, " branch -d") || strings.Contains(e, " branch -D") {
			t.Errorf("DestroySession ran %q after the removal stopped", e)
		}
	}
}
