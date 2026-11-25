package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A removal that stops leaves the session as it was: the record keeps every
// field it had, Active included, the worktree stays registered and no branch
// is deleted, forced or not. The next DestroySession on that record isn't
// short-circuited as terminal, and with a working remover it removes the
// worktree and ends the session. The dirty and attach guards still refuse
// before the remover is ever reached.
//
// Like destroy_remover_test.go, these tests reach DestroySession by
// reflection (destroyWithRemover), so the file builds before and after
// DestroySession takes the remover.

// destroyAnySignature calls DestroySession with the remover when it takes
// one, and without it while it takes five parameters. The guard tests use it:
// a guard that refuses before any teardown never reaches a remover either
// way.
func destroyAnySignature(t *testing.T, ctx context.Context, root, sid string, force bool, inv GitInvoker, remover WorktreeRemover) (SessionLifecycleState, error) {
	t.Helper()
	if state, err, ok := destroyWithRemover(ctx, root, sid, force, inv, remover); ok {
		return state, err
	}
	fn := reflect.ValueOf(DestroySession)
	if fn.Type().NumIn() != 5 {
		t.Fatalf("DestroySession is %v; want five parameters or a WorktreeRemover as its sixth", fn.Type())
	}
	out := fn.Call([]reflect.Value{
		reflect.ValueOf(ctx),
		reflect.ValueOf(root),
		reflect.ValueOf(sid),
		reflect.ValueOf(force),
		reflect.ValueOf(&inv).Elem(),
	})
	state := out[0].Interface().(SessionLifecycleState)
	var err error
	if e := out[1].Interface(); e != nil {
		err = e.(error)
	}
	return state, err
}

func recordJSON(t *testing.T, st SessionLifecycleState) string {
	t.Helper()
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDestroySession_AForcedRemovalThatStopsLeavesTheRecordActiveAndTheBranch(t *testing.T) {
	root, repoPath, sid := newRemoverSession(t)
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	before, err := ReadSessionLifecycleState(sessionsDir, sid)
	if err != nil {
		t.Fatal(err)
	}
	seq := &sequence{}
	var got [3]string
	_, err, ok := destroyWithRemover(context.Background(), root, sid, true, loggingInvoker{seq: seq}, stubRemover{seq: seq, fail: true, got: &got})
	if !ok {
		t.Fatal("DestroySession takes no WorktreeRemover, so a forced removal that stops can't be handed back to it")
	}
	if err == nil || !strings.Contains(err.Error(), before.WorktreePath) {
		t.Fatalf("forced DestroySession = %v; want an error naming %s", err, before.WorktreePath)
	}
	persisted, err := ReadSessionLifecycleState(sessionsDir, sid)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != SessionStatusActive {
		t.Errorf("record status = %q after a forced removal that stopped, want %q", persisted.Status, SessionStatusActive)
	}
	if !strings.Contains(registeredWorktrees(t, repoPath), before.WorktreePath) {
		t.Errorf("worktree %s is no longer registered after a forced removal that stopped", before.WorktreePath)
	}
	for _, e := range seq.events {
		if strings.Contains(e, " branch -D") || strings.Contains(e, " branch -d") {
			t.Errorf("forced DestroySession ran %q after the removal stopped", e)
		}
	}
	if out, _ := runCmd("git", "-C", repoPath, "branch", "--list", before.EffectiveBranchName()); strings.TrimSpace(out) == "" {
		t.Errorf("branch %s is gone after a forced removal that stopped", before.EffectiveBranchName())
	}
}

func TestDestroySession_AStoppedRemovalRestoresTheWholeRecord(t *testing.T) {
	root, _, sid := newRemoverSession(t)
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	before, err := ReadSessionLifecycleState(sessionsDir, sid)
	if err != nil {
		t.Fatal(err)
	}
	for _, force := range []bool{false, true} {
		seq := &sequence{}
		var got [3]string
		_, err, ok := destroyWithRemover(context.Background(), root, sid, force, loggingInvoker{seq: seq}, stubRemover{seq: seq, fail: true, got: &got})
		if !ok {
			t.Fatal("DestroySession takes no WorktreeRemover, so a removal that stops can't be handed back to it")
		}
		if err == nil {
			t.Fatalf("DestroySession (force=%v) with a removal that stops returned nil", force)
		}
		after, err := ReadSessionLifecycleState(sessionsDir, sid)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(after, before) {
			t.Errorf("force=%v: the record after a removal that stopped differs from the one before it\nbefore:\n%s\nafter:\n%s",
				force, recordJSON(t, before), recordJSON(t, after))
		}
	}
}

func TestDestroySession_RetriesAfterAStoppedRemoval(t *testing.T) {
	root, repoPath, sid := newRemoverSession(t)
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	before, err := ReadSessionLifecycleState(sessionsDir, sid)
	if err != nil {
		t.Fatal(err)
	}

	first := &sequence{}
	var got [3]string
	_, err, ok := destroyWithRemover(context.Background(), root, sid, false, loggingInvoker{seq: first}, stubRemover{seq: first, fail: true, got: &got})
	if !ok {
		t.Fatal("DestroySession takes no WorktreeRemover, so there is no stopped removal to retry")
	}
	if err == nil {
		t.Fatal("the first DestroySession, whose removal stops, returned nil")
	}

	second := &sequence{}
	got = [3]string{}
	state, err, _ := destroyWithRemover(context.Background(), root, sid, false, loggingInvoker{seq: second}, stubRemover{seq: second, got: &got})
	if err != nil {
		t.Fatalf("the second DestroySession: %v\ncalls: %q", err, second.events)
	}
	if got[1] != before.WorktreePath {
		t.Errorf("the second DestroySession asked the remover for %q, want %q; a record left by a stopped removal is not terminal\ncalls: %q",
			got[1], before.WorktreePath, second.events)
	}
	if state.Status != SessionStatusEnded {
		t.Errorf("returned status = %q, want ended", state.Status)
	}
	persisted, err := ReadSessionLifecycleState(sessionsDir, sid)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != SessionStatusEnded {
		t.Errorf("record status = %q after the retry removed the worktree, want ended", persisted.Status)
	}
	if _, err := os.Stat(before.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("worktree %s still present after the retry: %v", before.WorktreePath, err)
	}
	if strings.Contains(registeredWorktrees(t, repoPath), before.WorktreePath) {
		t.Errorf("worktree %s still registered after the retry", before.WorktreePath)
	}
}

// countRemover counts the removals it was asked for and removes nothing.
type countRemover struct{ n *int }

func (c countRemover) RemoveWorktree(instance, workTree, adminDir string) error {
	*c.n++
	return nil
}

// A guard: the dirty and attach guards refuse before any teardown, so they
// hold before and after DestroySession takes the remover.
func TestDestroySession_DirtyOrAttachedNeverCallsTheRemover(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, worktreePath string)
		want    error
	}{
		{"dirty", func(t *testing.T, wt string) {
			if err := os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("wip"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, ErrWorktreeDirty},
		{"attached", writeLiveAttachSentinel, ErrSessionAttached},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, repoPath, sid := newRemoverSession(t)
			sessionsDir := filepath.Join(root, ".niwa", "sessions")
			before, err := ReadSessionLifecycleState(sessionsDir, sid)
			if err != nil {
				t.Fatal(err)
			}
			tc.prepare(t, before.WorktreePath)

			n := 0
			_, err = destroyAnySignature(t, context.Background(), root, sid, false, StdGitInvoker{}, countRemover{n: &n})
			if !errors.Is(err, tc.want) {
				t.Fatalf("DestroySession = %v; want %v", err, tc.want)
			}
			if n != 0 {
				t.Errorf("the remover was called %d times; the %s guard refuses before any removal", n, tc.name)
			}
			persisted, err := ReadSessionLifecycleState(sessionsDir, sid)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.Status != SessionStatusActive {
				t.Errorf("record status = %q after the %s guard refused, want active", persisted.Status, tc.name)
			}
			if !strings.Contains(registeredWorktrees(t, repoPath), before.WorktreePath) {
				t.Errorf("worktree %s is no longer registered after the %s guard refused", before.WorktreePath, tc.name)
			}
		})
	}
}
