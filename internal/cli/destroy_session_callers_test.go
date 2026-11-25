package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/workspace"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// The four callers of DestroySession other than the hook worktree sweep (the
// failed-hook reconcile, the hook teardown, the person-run worktree destroy
// and destroySessionWorktrees) keep what they do today: they pass
// worktree.StdGitInvoker and a remover that runs git worktree remove --force
// through it with its result discarded, and their dirty check stays a plain
// git status --porcelain.
//
// Every repository and git wrapper here is under t.TempDir(); nothing
// touches this checkout's own configuration.

// destroyCallerSites are the four other calls, by file and enclosing
// function.
var destroyCallerSites = map[string][]string{
	"session_from_hook_cmd.go":    {"reconcileFailedHookCreate", "teardownHookSession"},
	"session_lifecycle_cmd.go":    {"destroyOneWorktree"},
	"worktree_destroy_resolve.go": {"destroySessionWorktrees"},
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

func TestDestroySessionCallers_PassStdGitInvokerAndADiscardingRemover(t *testing.T) {
	for file, funcs := range destroyCallerSites {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]int{}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isSelector(call.Fun, "worktree", "DestroySession") {
					return true
				}
				found[fn.Name.Name]++
				pos := fset.Position(call.Pos())
				if len(call.Args) != 6 {
					t.Errorf("%s: %s calls DestroySession with %d arguments; want six, a remover after the git invoker", pos, fn.Name.Name, len(call.Args))
					return true
				}
				inv := call.Args[4]
				stdInvoker := false
				if lit, ok := inv.(*ast.CompositeLit); ok && isSelector(lit.Type, "worktree", "StdGitInvoker") {
					stdInvoker = true
				}
				if id, ok := inv.(*ast.Ident); ok && id.Name == "git" && fn.Name.Name == "destroySessionWorktrees" {
					stdInvoker = true
				}
				if !stdInvoker {
					t.Errorf("%s: %s's git invoker is not worktree.StdGitInvoker{} (or destroySessionWorktrees' git parameter)", pos, fn.Name.Name)
				}
				rem := call.Args[5]
				if id, ok := rem.(*ast.Ident); ok && (id.Name == "nil" || id.Name == "worktreeRemover") {
					t.Errorf("%s: %s passes %s as its remover; it keeps today's discarded git worktree remove, not the rmtree.go remover", pos, fn.Name.Name, id.Name)
				}
				if sel, ok := rem.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "removal" {
						t.Errorf("%s: %s passes removal.%s as its remover; it keeps today's discarded git worktree remove", pos, fn.Name.Name, sel.Sel.Name)
					}
				}
				return true
			})
		}
		for _, name := range funcs {
			if found[name] != 1 {
				t.Errorf("%s: %s calls worktree.DestroySession %d times, want once", file, name, found[name])
			}
		}
	}
}

// failingWorktreeRemoveGit puts a git on PATH that logs each call's
// arguments, one line per call, fails git worktree remove without running
// it, and runs the real git for everything else. It returns the log's path.
func failingWorktreeRemoveGit(t *testing.T) string {
	t.Helper()
	real := realGit(t)
	dir := canonicalTempDir(t)
	log := filepath.Join(canonicalTempDir(t), "git-calls.log")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> '%s'
prev=""
for a in "$@"; do
	if [ "$prev" = worktree ] && [ "$a" = remove ]; then
		echo "fatal: worktree remove failed by the test" >&2
		exit 1
	fi
	prev="$a"
done
exec '%s' "$@"
`, log, real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func readCallLog(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the git wrapper logged nothing: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func newCallerCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetContext(context.Background())
	return cmd, &out, &errb
}

// destroyCaller runs one of the four callers against the session st and
// checks what it reports when git worktree remove failed.
type destroyCaller struct {
	name  string
	run   func(t *testing.T, f *createFlowFixture, st worktree.SessionLifecycleState) (stdout, stderr string, err error)
	check func(t *testing.T, st worktree.SessionLifecycleState, stdout, stderr string, err error)
}

var errCallerCause = errors.New("content install failed in the test")

var destroyCallers = []destroyCaller{
	{
		name: "reconcileFailedHookCreate",
		run: func(t *testing.T, f *createFlowFixture, st worktree.SessionLifecycleState) (string, string, error) {
			var stderr bytes.Buffer
			err := reconcileFailedHookCreate(&stderr, f.root, st.SessionID, errCallerCause)
			return "", stderr.String(), err
		},
		check: func(t *testing.T, st worktree.SessionLifecycleState, stdout, stderr string, err error) {
			if err == nil || !errors.Is(err, errCallerCause) {
				t.Fatalf("reconcileFailedHookCreate = %v; want the cause wrapped", err)
			}
			if !strings.Contains(err.Error(), "rolled back") || strings.Contains(err.Error(), "rollback incomplete") {
				t.Errorf("reconcileFailedHookCreate = %q; a failed git worktree remove is discarded, so it still says rolled back", err)
			}
		},
	},
	{
		name: "teardownHookSession",
		run: func(t *testing.T, f *createFlowFixture, st worktree.SessionLifecycleState) (string, string, error) {
			var stderr bytes.Buffer
			teardownHookSession(&stderr, f.root, st.SessionID, hookEventWorktreeRemove)
			return "", stderr.String(), nil
		},
		check: func(t *testing.T, st worktree.SessionLifecycleState, stdout, stderr string, err error) {
			if strings.Contains(stderr, "warning") {
				t.Errorf("teardownHookSession warned after a failed git worktree remove, which it discards:\n%s", stderr)
			}
		},
	},
	{
		name: "destroyOneWorktree",
		run: func(t *testing.T, f *createFlowFixture, st worktree.SessionLifecycleState) (string, string, error) {
			sessionDestroyForce = false
			cmd, out, errb := newCallerCmd()
			err := destroyOneWorktree(cmd, f.root, st.SessionID)
			return out.String(), errb.String(), err
		},
		check: func(t *testing.T, st worktree.SessionLifecycleState, stdout, stderr string, err error) {
			if err != nil {
				t.Fatalf("destroyOneWorktree = %v; a failed git worktree remove is discarded", err)
			}
			if want := "session: destroyed " + st.SessionID; !strings.Contains(stdout, want) {
				t.Errorf("destroyOneWorktree printed %q; want %q", stdout, want)
			}
		},
	},
	{
		name: "destroySessionWorktrees",
		run: func(t *testing.T, f *createFlowFixture, st worktree.SessionLifecycleState) (string, string, error) {
			cmd, out, errb := newCallerCmd()
			err := destroySessionWorktrees(cmd, f.root, workspace.SessionMapping{SessionID: "claude-sid"}, worktree.StdGitInvoker{})
			return out.String(), errb.String(), err
		},
		check: func(t *testing.T, st worktree.SessionLifecycleState, stdout, stderr string, err error) {
			if err != nil {
				t.Fatalf("destroySessionWorktrees = %v (stderr %q); want exit 0 after a discarded git worktree remove", err, stderr)
			}
			if want := "session: destroyed " + st.SessionID; !strings.Contains(stdout, want) {
				t.Errorf("destroySessionWorktrees printed %q; want %q", stdout, want)
			}
		},
	},
}

func findCaller(t *testing.T, name string) destroyCaller {
	t.Helper()
	for _, c := range destroyCallers {
		if c.name == name {
			return c
		}
	}
	t.Fatalf("no caller %s", name)
	return destroyCaller{}
}

// runCallerAfterFailedRemove makes a hook worktree, puts the failing git on
// PATH and runs the caller, returning the session, the git call log and the
// caller's output.
func runCallerAfterFailedRemove(t *testing.T, c destroyCaller) (worktree.SessionLifecycleState, []string, string, string, error) {
	t.Helper()
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	t.Cleanup(func() { resetSessionCreateFlags(t) })
	st := createViaHook(t, f, "caller-"+strings.ToLower(c.name))
	log := failingWorktreeRemoveGit(t)
	stdout, stderr, err := c.run(t, f, st)

	got, readErr := worktree.ReadSessionLifecycleState(f.sessionsDir, st.SessionID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got.Status != worktree.SessionStatusEnded {
		t.Errorf("%s left the record %q after a discarded git worktree remove; want ended", c.name, got.Status)
	}
	return st, readCallLog(t, log), stdout, stderr, err
}

// assertDiscardedRemoveBeforeBranchDelete checks the log holds today's git
// worktree remove --force of the worktree before git branch -d.
func assertDiscardedRemoveBeforeBranchDelete(t *testing.T, name string, st worktree.SessionLifecycleState, lines []string) {
	t.Helper()
	removeAt, branchAt := -1, -1
	for i, line := range lines {
		if removeAt < 0 && strings.Contains(line, "worktree remove --force") && strings.HasSuffix(line, st.WorktreePath) {
			removeAt = i
		}
		if branchAt < 0 && strings.Contains(line, " branch -d ") {
			branchAt = i
		}
	}
	if removeAt < 0 || branchAt < 0 || removeAt > branchAt {
		t.Errorf("%s's git calls %q; want git worktree remove --force %s before git branch -d", name, lines, st.WorktreePath)
	}
}

func testCallerDiscardsAFailedWorktreeRemove(t *testing.T, name string) {
	c := findCaller(t, name)
	st, lines, stdout, stderr, err := runCallerAfterFailedRemove(t, c)
	c.check(t, st, stdout, stderr, err)
	assertDiscardedRemoveBeforeBranchDelete(t, name, st, lines)
}

// A guard: each caller discards a failed git worktree remove today and keeps
// doing so through its remover.
func TestReconcileFailedHookCreate_DiscardsAFailedWorktreeRemove(t *testing.T) {
	testCallerDiscardsAFailedWorktreeRemove(t, "reconcileFailedHookCreate")
}

// A guard, as above.
func TestTeardownHookSession_DiscardsAFailedWorktreeRemove(t *testing.T) {
	testCallerDiscardsAFailedWorktreeRemove(t, "teardownHookSession")
}

// A guard, as above.
func TestDestroyOneWorktree_DiscardsAFailedWorktreeRemove(t *testing.T) {
	testCallerDiscardsAFailedWorktreeRemove(t, "destroyOneWorktree")
}

// A guard, as above.
func TestDestroySessionWorktrees_DiscardsAFailedWorktreeRemove(t *testing.T) {
	testCallerDiscardsAFailedWorktreeRemove(t, "destroySessionWorktrees")
}

// A guard: the other callers' dirty check stays git status --porcelain with
// no added flags.
func TestDestroySessionCallers_StatusStaysPlainPorcelain(t *testing.T) {
	for _, c := range destroyCallers {
		t.Run(c.name, func(t *testing.T) {
			st, lines, _, _, _ := runCallerAfterFailedRemove(t, c)
			want := "-C " + st.WorktreePath + " status --porcelain"
			statuses := 0
			for _, line := range lines {
				isStatus := false
				for _, field := range strings.Fields(line) {
					if field == "status" {
						isStatus = true
					}
				}
				if !isStatus {
					continue
				}
				statuses++
				if line != want {
					t.Errorf("%s ran git %q; want git %q", c.name, line, want)
				}
			}
			if statuses == 0 {
				t.Errorf("%s ran no git status; want git %q\ncalls: %q", c.name, want, lines)
			}
		})
	}
}
