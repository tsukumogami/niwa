package removal

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// importsOf returns every import path the non-test Go files in dir name.
func importsOf(t *testing.T, dir string) map[string][]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			out[p] = append(out[p], filepath.Base(file))
		}
	}
	return out
}

// The removal package is the rule every removal path calls, so it reaches
// nothing that calls it: no import of internal/cli or internal/worktree.
// internal/worktree, in turn, imports nothing from internal/removal; it
// scrubs through the leaf package internal/scrub.
func TestImportBoundaries(t *testing.T) {
	const mod = "github.com/tsukumogami/niwa/internal/"
	for path, files := range importsOf(t, ".") {
		if path == mod+"cli" || strings.HasPrefix(path, mod+"cli/") || path == mod+"worktree" {
			t.Errorf("internal/removal imports %s (in %v)", path, files)
		}
	}
	for path, files := range importsOf(t, filepath.Join("..", "worktree")) {
		if path == mod+"removal" {
			t.Errorf("internal/worktree imports internal/removal (in %v)", files)
		}
	}
}
