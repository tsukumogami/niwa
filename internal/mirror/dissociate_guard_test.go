package mirror

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestGuard_OnlyDissociateCopiesBorrowedObjects reads the module's sources and
// fails when any non-test Go file but dissociate.go passes git a "repack" or a
// "--dissociate": those are the two ways git copies a borrower's borrowed
// objects into it, and niwa dissociate is the one command allowed to (PRD
// R21). It also fails when dissociate.go has neither, so the guard can't pass
// by reading nothing.
func TestGuard_OnlyDissociateCopiesBorrowedObjects(t *testing.T) {
	root := guardModuleRoot(t)
	own := filepath.Join(root, "internal", "mirror", "dissociate.go")
	ownSeen := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil || (v != "repack" && v != "--dissociate") {
				return true
			}
			if path == own {
				ownSeen = true
				return true
			}
			t.Errorf("%s passes git %q; only niwa dissociate copies borrowed objects into a clone", path, v)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ownSeen {
		t.Fatal("dissociate.go passes git no repack, so the guard reads nothing")
	}
}

// guardModuleRoot is the directory holding go.mod, found upward from the
// package.
func guardModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}

// TestGuide_ReferenceClonesNamesTheMirrorsAndThePurgeOrder reads the user
// guide and checks what PRD R11 asks of it: where the mirrors live, the
// setting at both levels, niwa dissociate, and the four purge steps in their
// order, each found after the one before it.
func TestGuide_ReferenceClonesNamesTheMirrorsAndThePurgeOrder(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(guardModuleRoot(t), "docs", "guides", "reference-clones.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := string(data)
	for _, want := range []string{
		"~/.niwa/w-<key>/mirrors/",
		"[workspace]\nname = \"myws\"\nshare_git_history = false",
		"[repos.app]\nshare_git_history = true",
		"The repo's value wins over the workspace's in both directions",
		"niwa dissociate [instance]",
		"alternates.niwa-dissociate",
		"a removed secret included",
	} {
		if !strings.Contains(guide, want) {
			t.Errorf("the guide doesn't say %q", want)
		}
	}
	purge := guide[strings.Index(guide, "## Purging"):]
	at := 0
	for _, step := range []string{
		"1. **Rotate the secret.**",
		"rewrite or delete every local branch that reaches the\n   commit, then run `git fetch --prune`",
		"`.store/<repo-id>-*` directories",
		"4. **Ask the forge to drop the commit.**",
		"Why the order matters",
	} {
		i := strings.Index(purge[at:], step)
		if i < 0 {
			t.Fatalf("the purge section doesn't say %q after what came before it", step)
		}
		at += i + len(step)
	}
}
