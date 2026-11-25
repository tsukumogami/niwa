package removal

import (
	"os"
	"path/filepath"
	"testing"
)

// A git worktree lock taken after the read of the tree, while the removal
// is under way, keeps the admin directory: the lock is read again right
// before the admin directory goes, and the walk stops with it named.
func TestRemoveWorktree_ALockTakenDuringTheRemovalKeepsTheAdminDirectory(t *testing.T) {
	f := newWorktreeFixture(t)
	deep := filepath.Join(f.wt, "deep")
	locked := filepath.Join(f.admin, "locked")
	opens := 0
	t.Cleanup(SetBeforeOpenHook(func(p string) {
		if p != deep {
			return
		}
		opens++
		if opens == 2 { // the removal walk, after the read of both trees
			if err := os.WriteFile(locked, []byte("taken during the removal\n"), 0o644); err != nil {
				t.Errorf("taking the lock: %v", err)
			}
		}
	}))
	err := RemoveWorktree(f.instance, f.wt, f.admin)
	if opens < 2 {
		t.Fatalf("the removal walk never reached %s", deep)
	}
	wantStop(t, err, "the worktree is locked: app/.git/worktrees/wt/locked", false)
	for _, p := range []string{locked, filepath.Join(f.admin, "HEAD")} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s was removed with the lock in place: %v", p, err)
		}
	}
}
