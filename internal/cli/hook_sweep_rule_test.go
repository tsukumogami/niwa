package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// The hook worktree sweep goes through the rule: before any other git call
// it walks each worktree and runs the program-naming configuration check on
// every repository there, it calls DestroySession with force off, a git
// invoker built on gitCommand and the rmtree.go remover, and it re-reads the
// worktree's HEAD and per-worktree refs right before the removal.
//
// Every repository, remote, driver and marker file here is under
// t.TempDir(); nothing touches this checkout's own configuration.

// sweepRuleFixture is one hook-made worktree the sweep would remove: its
// Claude session has ended, its record is past the grace and nothing in it
// changed. The repository's main is on a file remote the workspace
// configuration names and the test's removal environment admits, so the
// rule's repository check holds the worktree's commit.
type sweepRuleFixture struct {
	f    *createFlowFixture
	st   worktree.SessionLifecycleState
	jobs string
	// base is the workspace configuration, without a pre-removal command.
	base string
}

// newSweepRuleFixture builds the fixture. seed, when set, runs in the
// repository before its first push, to commit files the worktree checks out.
func newSweepRuleFixture(t *testing.T, seed func(repoPath string)) sweepRuleFixture {
	t.Helper()
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	t.Cleanup(func() { resetSessionCreateFlags(t) })
	t.Setenv("NIWA_REAP_IDLE_GRACE", "24h")
	jobs := scratchJobsDir(t)

	if seed != nil {
		seed(f.repoPath)
	}
	bare := filepath.Join(canonicalTempDir(t), "myrepo.git")
	passGit(t, f.repoPath, "init", "-q", "--bare", "-b", "main", bare)
	passGit(t, f.repoPath, "remote", "add", "origin", "file://"+bare)
	passGit(t, f.repoPath, "push", "-q", "origin", "main")
	base := "[workspace]\nname = \"testws\"\n\n[groups." + f.group + "]\n\n[repos." + f.repo + "]\nurl = \"file://" + bare + "\"\ngroup = \"" + f.group + "\"\n\n[repos." + f.repo + ".claude]\nenabled = false\n"
	if err := os.WriteFile(workspaceConfigPath(f.root), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	prevEnv := newRemovalEnv
	newRemovalEnv = func(r, home, pathEnv string) *removal.Env {
		return removal.NewEnv(r, home, pathEnv, removal.WithFileRemotes())
	}
	t.Cleanup(func() { newRemovalEnv = prevEnv })

	st := createHookWorktreeFor(t, f, "swept", otherSessionID, idleTranscript(t, 48*time.Hour))
	backdate(t, f, st, 48*time.Hour)
	return sweepRuleFixture{f: f, st: st, jobs: jobs, base: base}
}

// markerScript writes an executable script that creates marker and then
// copies its standard input to its output, so it works as a clean filter
// and, run as core.fsmonitor, reports nothing.
func markerScript(t *testing.T, marker string) string {
	t.Helper()
	script := filepath.Join(canonicalTempDir(t), "driver.sh")
	body := fmt.Sprintf("#!/bin/sh\n: > '%s'\ncat\n", marker)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func assertNoMarker(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("the planted driver ran: marker %s exists (stat err %v)", marker, err)
	}
}

func TestSweepRule_RemovesAnUnchangedWorktreeWithNoWorktreeRemoveOrForce(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	log := destroyGitWrapper(t, "")

	n, out := sweep(t, fx.f, fx.jobs)
	if n != 1 {
		t.Fatalf("removed %d, want the unchanged worktree removed\nstderr:\n%s", n, out)
	}
	assertRemoved(t, fx.f, fx.st)
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.Contains(line, "worktree remove") || strings.Contains(line, "worktree prune") || strings.Contains(line, "--force") {
			t.Errorf("the sweep ran git %s; it removes a worktree with no git worktree remove or prune and never forces", line)
		}
	}
}

func TestSweepRule_FsmonitorInTheRepositoryNeverRuns(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	passGit(t, fx.f.repoPath, "config", "core.fsmonitor", markerScript(t, marker))

	n, out := sweep(t, fx.f, fx.jobs)
	assertNoMarker(t, marker)
	if n != 0 {
		t.Fatalf("removed %d, want the worktree kept\nstderr:\n%s", n, out)
	}
	assertKept(t, fx.f, fx.st)
	if !strings.Contains(out, "core.fsmonitor") {
		t.Errorf("sweep output does not name core.fsmonitor:\n%s", out)
	}
}

// The sweep's git calls run with hooks off, so hooks planted in the
// repository don't run when the sweep removes the worktree and deletes its
// branch with branch -d. A plain checkout afterwards shows the hooks were
// live all along.
func TestSweepRule_HooksInTheRepositoryNeverRunOnTheBranchDelete(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	markers := map[string]string{}
	for _, hook := range []string{"reference-transaction", "post-checkout"} {
		marker := filepath.Join(t.TempDir(), hook+"-ran")
		markers[hook] = marker
		body := fmt.Sprintf("#!/bin/sh\n: > '%s'\n", marker)
		if err := os.WriteFile(filepath.Join(fx.f.repoPath, ".git", "hooks", hook), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	n, out := sweep(t, fx.f, fx.jobs)
	if n != 1 {
		t.Fatalf("removed %d, want the unchanged worktree removed\nstderr:\n%s", n, out)
	}
	assertRemoved(t, fx.f, fx.st)
	for _, marker := range markers {
		assertNoMarker(t, marker)
	}

	passGit(t, fx.f.repoPath, "checkout", "-q", "-b", "probe")
	for hook, marker := range markers {
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("the planted %s hook did not run on a plain checkout: %v", hook, err)
		}
	}
}

func TestSweepRule_SSHCommandInTheRepositoryKeepsAndNeverRuns(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	marker := filepath.Join(t.TempDir(), "ssh-ran")
	passGit(t, fx.f.repoPath, "config", "core.sshCommand", markerScript(t, marker))

	n, out := sweep(t, fx.f, fx.jobs)
	assertNoMarker(t, marker)
	if n != 0 {
		t.Fatalf("removed %d, want the worktree kept\nstderr:\n%s", n, out)
	}
	assertKept(t, fx.f, fx.st)
	// git lists configuration keys in lower case.
	if !strings.Contains(out, "core.sshcommand") {
		t.Errorf("sweep output does not name core.sshCommand:\n%s", out)
	}
}

func TestSweepRule_ACleanFilterDriverKeepsTheWorktree(t *testing.T) {
	fx := newSweepRuleFixture(t, func(repo string) {
		writeTestFile(t, filepath.Join(repo, ".gitattributes"), "*.txt filter=x\n")
		writeTestFile(t, filepath.Join(repo, "a.txt"), "data\n")
		passGit(t, repo, "add", ".gitattributes", "a.txt")
		passGit(t, repo, "commit", "-q", "-m", "attributes")
	})
	marker := filepath.Join(t.TempDir(), "filter-ran")
	passGit(t, fx.f.repoPath, "config", "filter.x.clean", markerScript(t, marker))
	// Stat-dirty, same contents: a status would run the clean filter to
	// compare.
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(fx.st.WorktreePath, "a.txt"), later, later); err != nil {
		t.Fatal(err)
	}

	n, out := sweep(t, fx.f, fx.jobs)
	assertNoMarker(t, marker)
	if n != 0 {
		t.Fatalf("removed %d, want the worktree kept\nstderr:\n%s", n, out)
	}
	assertKept(t, fx.f, fx.st)
	if !strings.Contains(out, "filter.x.clean") {
		t.Errorf("sweep output does not name filter.x.clean:\n%s", out)
	}
}

// The pre-removal command runs after the sweep's evaluation and before
// DestroySession, so a command that moves a per-worktree ref and allows the
// removal is a change made between the evaluation and the removal.
func TestSweepRule_APerWorktreeRefMovedBeforeTheRemovalKeeps(t *testing.T) {
	fx := newSweepRuleFixture(t, nil)
	git := realGit(t)
	setBeforeRemoveCommand(t, fx.f.root, fx.base, fmt.Sprintf(
		"'%s' -C '%s' update-ref refs/worktree/moved HEAD; exit 0", git, fx.st.WorktreePath))

	n, out := sweep(t, fx.f, fx.jobs)
	if n != 0 {
		t.Fatalf("removed %d, want the worktree kept after its ref moved\nstderr:\n%s", n, out)
	}
	assertKept(t, fx.f, fx.st)
	if want := "changed during the pass: ref refs/worktree/moved"; !strings.Contains(out, want) {
		t.Errorf("sweep output does not say %q:\n%s", want, out)
	}
}

func TestSweepRule_TheSweepPassesStdGitInvokerNowhere(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "reap_worktrees.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "StdGitInvoker" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "worktree" {
			t.Errorf("reap_worktrees.go uses worktree.StdGitInvoker at %s; the sweep's git calls go through gitCommand", fset.Position(sel.Pos()))
		}
		return true
	})
}
