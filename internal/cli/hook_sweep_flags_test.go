package cli

import (
	"strings"
	"testing"
)

// On the reap-time sweep's path, DestroySession's git status carries
// --ignore-submodules=all and --untracked-files=all, and the configuration
// check runs again right before that status and right before the removal.
// The removal itself makes no git call, so the second check is placed
// between the status and git branch -d.
//
// Every repository here is under t.TempDir().

// sweepCallOrder sweeps the fixture's clean worktree with a logging git on
// PATH and returns the logged calls, the index of DestroySession's status
// (the last status naming the worktree before git branch -d), the index of
// the status naming the worktree before it (-1 if none) and the index of
// git branch -d (len(lines) if none).
func sweepCallOrder(t *testing.T) (fx sweepRuleFixture, lines []string, prev, status, branch int) {
	t.Helper()
	fx = newSweepRuleFixture(t, nil)
	log := destroyGitWrapper(t, "")

	n, out := sweep(t, fx.f, fx.jobs)
	if n != 1 {
		t.Errorf("removed %d, want the unchanged worktree removed\nstderr:\n%s", n, out)
	}
	lines = scanOrderLog(t, log)
	wt := pathSpellings(fx.st.WorktreePath)
	branch = len(lines)
	for i, line := range lines {
		if scanOrderSubcommand(line) == "branch" && strings.Contains(line, " -d") {
			branch = i
			break
		}
	}
	prev, status = -1, -1
	for i := 0; i < branch; i++ {
		if scanOrderSubcommand(lines[i]) == "status" && namesAnyPath(lines[i], wt) {
			prev, status = status, i
		}
	}
	if status < 0 {
		t.Fatalf("the sweep ran no git status naming the worktree %s:\n%s", fx.st.WorktreePath, strings.Join(lines, "\n"))
	}
	return fx, lines, prev, status, branch
}

func TestSweepRule_StatusCarriesIgnoreSubmodulesAndUntrackedAll(t *testing.T) {
	_, lines, _, status, _ := sweepCallOrder(t)
	line := lines[status]
	for _, flag := range []string{"--ignore-submodules=all", "--untracked-files=all"} {
		if !strings.Contains(line, flag) {
			t.Errorf("DestroySession's status on the sweep's path does not carry %s: git %s", flag, line)
		}
	}
}

func TestSweepRule_ConfigRecheckedBeforeStatusAndBeforeRemoval(t *testing.T) {
	fx, lines, prev, status, branch := sweepCallOrder(t)
	repo := append(pathSpellings(fx.f.repoPath), pathSpellings(fx.st.WorktreePath)...)
	configCheck := func(from, to int) bool {
		for i := from + 1; i < to; i++ {
			if scanOrderSubcommand(lines[i]) == "config" && strings.Contains(lines[i], "--list") && namesAnyPath(lines[i], repo) {
				return true
			}
		}
		return false
	}
	if !configCheck(prev, status) {
		t.Errorf("no configuration check for the worktree's repository right before DestroySession's status (calls %d to %d):\n%s",
			prev+1, status, strings.Join(lines, "\n"))
	}
	if !configCheck(status, branch) {
		t.Errorf("no configuration check for the worktree's repository between DestroySession's status and the removal (calls %d to %d):\n%s",
			status+1, branch, strings.Join(lines, "\n"))
	}
}
