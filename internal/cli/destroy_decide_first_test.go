package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/removal"
)

// niwa destroy reads every worktree again (its configuration and a fresh
// status) before the first one goes, then removes them with no git call in
// between.

// destroyGitWrapper puts a git on PATH that logs each call's arguments, one
// line per call, and runs the real git. With plant set, the second status
// the removal path runs in the worktree whose work tree ends in /<plant>
// (the fresh status after the scan's) finds late.txt written there first. It
// returns the log's path.
func destroyGitWrapper(t *testing.T, plant string) string {
	t.Helper()
	real := realGit(t)
	dir := canonicalTempDir(t)
	scratch := canonicalTempDir(t)
	log := filepath.Join(scratch, "git-calls.log")
	counter := filepath.Join(scratch, "status-count")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> '%s'
plant='%s'
if [ -n "$plant" ]; then
	wt=""
	st=""
	for a in "$@"; do
		case "$a" in
		--work-tree=*) wt="${a#--work-tree=}" ;;
		status) st=1 ;;
		esac
	done
	case "$wt" in
	*"/$plant")
		if [ -n "$st" ]; then
			n=$(cat '%s' 2>/dev/null || echo 0)
			n=$((n + 1))
			echo "$n" > '%s'
			if [ "$n" = 2 ]; then
				echo late > "$wt/late.txt"
			fi
		fi
		;;
	esac
fi
exec '%s' "$@"
`, log, plant, counter, counter, real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// countingRemover records each worktree removal with how many git calls the
// wrapper had logged by then, and removes through the no-follow walk.
type countingRemover struct {
	log     string
	removed *[]string
	calls   *[]int
}

func (r countingRemover) RemoveWorktree(instance, workTree, adminDir string) error {
	b, _ := os.ReadFile(r.log)
	*r.removed = append(*r.removed, workTree)
	*r.calls = append(*r.calls, strings.Count(string(b), "\n"))
	return removal.RemoveWorktree(instance, workTree, adminDir)
}

func TestDestroyDecideFirst_EveryWorktreeIsReadBeforeTheFirstGoes(t *testing.T) {
	setup := func(t *testing.T) (root, wt, wt2 string) {
		t.Helper()
		root, clone, wt := destroyWorktreeInstance(t)
		wt2 = filepath.Join(root, "alpha", "wt2")
		passGit(t, clone, "worktree", "add", "-q", wt2, "-b", "wt2-branch", "main")
		passGit(t, clone, "push", "-q", "-u", "origin", "wt2-branch")
		chdirTo(t, root)
		quietDestroyCmd()
		setDestroyFlags(t, false, false, "")
		return root, wt, wt2
	}
	install := func(t *testing.T, log string) (*[]string, *[]int) {
		t.Helper()
		var removed []string
		var calls []int
		prev := worktreeRemover
		worktreeRemover = countingRemover{log: log, removed: &removed, calls: &calls}
		t.Cleanup(func() { worktreeRemover = prev })
		return &removed, &calls
	}

	t.Run("a change in the last worktree keeps the first", func(t *testing.T) {
		root, wt, _ := setup(t)
		log := destroyGitWrapper(t, "alpha/wt2")
		removed, _ := install(t, log)
		err := runDestroy(destroyCmd, []string{"alpha"})
		if err == nil || !strings.Contains(err.Error(), "destroy stopped before worktree wt2 was removed") || !strings.Contains(err.Error(), "wt2/late.txt") {
			t.Fatalf("destroy = %v; want it stopped before wt2 with wt2/late.txt named", err)
		}
		if len(*removed) != 0 {
			t.Fatalf("worktrees removed %q; want none before every worktree was read", *removed)
		}
		if _, err := os.Stat(filepath.Join(wt, "README")); err != nil {
			t.Errorf("the first worktree lost a file: %v", err)
		}
		assertInstancesExist(t, root, "alpha")
	})
	t.Run("no git call between the removals", func(t *testing.T) {
		root, _, _ := setup(t)
		log := destroyGitWrapper(t, "")
		removed, calls := install(t, log)
		if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
			t.Fatalf("destroy: %v", err)
		}
		if len(*removed) != 2 {
			t.Fatalf("worktrees removed %q; want both", *removed)
		}
		if (*calls)[0] != (*calls)[1] {
			b, _ := os.ReadFile(log)
			lines := strings.Split(strings.TrimSpace(string(b)), "\n")
			t.Fatalf("git calls between the removals: %q", lines[(*calls)[0]:(*calls)[1]])
		}
		if _, err := os.Lstat(filepath.Join(root, "alpha")); !os.IsNotExist(err) {
			t.Fatalf("the instance is still there: %v", err)
		}
		assertNoWorktreeRemoval(t, log, false)
	})
}
