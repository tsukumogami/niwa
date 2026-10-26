package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// removalCallers names every non-test function in this package that calls
// destroyInstanceFunc or worktree.DestroySession, as "<file>:<function>".
//
// A new removal path has to be added here on purpose, which is the moment to
// decide whether it asks the pre-removal gate. Each entry says which way it
// went and why.
var removalCallers = map[string]string{}

// TestRemovalCallers_EveryCallerIsAccountedFor fails when a non-test function
// in this package calls destroyInstanceFunc or worktree.DestroySession and
// isn't named in removalCallers, or when a named one no longer does.
func TestRemovalCallers_EveryCallerIsAccountedFor(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if isRemovalCall(call.Fun) {
					found[name+":"+fn.Name.Name] = true
				}
				return true
			})
		}
	}

	var unlisted, stale []string
	for k := range found {
		if _, ok := removalCallers[k]; !ok {
			unlisted = append(unlisted, k)
		}
	}
	for k := range removalCallers {
		if !found[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(unlisted)
	sort.Strings(stale)
	if len(unlisted) > 0 {
		t.Errorf("removal callers not in removalCallers (decide whether each asks the pre-removal gate, then list it): %v", unlisted)
	}
	if len(stale) > 0 {
		t.Errorf("removalCallers names functions that no longer remove anything: %v", stale)
	}
}

// isRemovalCall reports whether fun is destroyInstanceFunc or
// worktree.DestroySession.
func isRemovalCall(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name == "destroyInstanceFunc"
	case *ast.SelectorExpr:
		pkg, ok := f.X.(*ast.Ident)
		return ok && pkg.Name == "worktree" && f.Sel.Name == "DestroySession"
	}
	return false
}
