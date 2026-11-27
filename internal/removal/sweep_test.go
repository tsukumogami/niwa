package removal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// EvaluateSweptWorktree takes a record's worktree path only when, as text
// and without following a link below the instance, it names the linked
// worktree the walk found there. A link at or above the instance root is
// allowed, so the instance can be named in either spelling; a path that
// isn't absolute, carries a ".." component, or reaches the worktree through
// a link outside the instance or below its root is refused before any git
// call.

// sweptFixture is the worktree fixture with the instance also reachable
// through a link above it: alias names the instance through that link.
type sweptFixture struct {
	wtFixture
	alias string
}

func newSweptFixture(t *testing.T) sweptFixture {
	t.Helper()
	f := newWorktreeFixture(t)
	other, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(other, "above")
	if err := os.Symlink(filepath.Dir(f.instance), link); err != nil {
		t.Fatal(err)
	}
	return sweptFixture{wtFixture: f, alias: filepath.Join(link, filepath.Base(f.instance))}
}

// observedEnv is the worktree environment with every git call it builds
// recorded by its work tree, or its git directory when it has none.
func observedEnv(t *testing.T, f wtFixture) (*Env, *[]string) {
	t.Helper()
	e := worktreeEnv(t, f)
	var calls []string
	e.observe = func(r repoDirs, args []string) {
		dir := r.WorkTree
		if dir == "" {
			dir = r.GitDir
		}
		calls = append(calls, dir+": "+strings.Join(args, " "))
	}
	return e, &calls
}

func TestEvaluateSweptWorktree_AcceptsTheWorktreeUnderEitherSpellingOfTheInstance(t *testing.T) {
	f := newSweptFixture(t)
	// The instance as the walk is given it, and with the link above it
	// resolved: under the alias both spellings are the instance's, under
	// the resolved path only that one is.
	for _, c := range []struct{ inst, rec string }{
		{f.alias, filepath.Join(f.alias, "wt")},
		{f.alias, f.wt},
		{f.instance, f.wt},
	} {
		e, _ := observedEnv(t, f.wtFixture)
		s, keep := e.EvaluateSweptWorktree(context.Background(), c.inst, c.rec)
		if keep != "" || s == nil {
			t.Fatalf("instance %s, record %s: kept %q; want the worktree taken", c.inst, c.rec, keep)
		}
		if s.workTree != c.rec {
			t.Errorf("instance %s, record %s: git runs in %s; want the record's spelling", c.inst, c.rec, s.workTree)
		}
	}
}

func TestEvaluateSweptWorktree_RefusesAPathThatIsNotTheWalksWorktreeAsText(t *testing.T) {
	f := newSweptFixture(t)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outsideLink := filepath.Join(outside, "wt-link")
	if err := os.Symlink(f.wt, outsideLink); err != nil {
		t.Fatal(err)
	}
	insideLink := filepath.Join(f.instance, "wt-alias")
	if err := os.Symlink(f.wt, insideLink); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, rec string }{
		{"empty", ""},
		{"relative", filepath.Join(filepath.Base(f.instance), "wt")},
		{"a dot-dot component", f.instance + "/app/../wt"},
		{"a dot-dot component above the instance", f.instance + "/../" + filepath.Base(f.instance) + "/wt"},
		{"a link outside the instance", outsideLink},
		{"a link below the instance root", insideLink},
		{"a link above the instance that is neither of its spellings", filepath.Join(f.alias, "wt")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, calls := observedEnv(t, f.wtFixture)
			s, keep := e.EvaluateSweptWorktree(context.Background(), f.instance, c.rec)
			if s != nil || keep == "" {
				t.Fatalf("record %q was taken; want it kept", c.rec)
			}
			if len(*calls) != 0 {
				t.Errorf("record %q: git ran before the refusal:\n%s", c.rec, strings.Join(*calls, "\n"))
			}
		})
	}
	f.assertKept(t)
}
