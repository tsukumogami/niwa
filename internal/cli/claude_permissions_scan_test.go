package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestClaudePermissionsHasOneReader pins who touches
// InstanceState.ClaudePermissions. The recorded posture is trusted only for an
// instance the calling process just provisioned, so it has exactly one reader
// -- runDispatch, straight after its own Create -- and its only writers are the
// instance pipeline's Create and Apply, copying the current run's result.
//
// It reads every non-test source under internal/ as a syntax tree, so a
// mention in a comment is not a violation. A field read anywhere else (`niwa
// watch`, `niwa list`, the workspace-root state `niwa init` saves,
// effective-name resolution), a field-level copy from earlier state, or any use
// of derivePermissionMode outside dispatch fails it. It matches syntax only: a
// whole-struct copy of loaded state, or a load-modify-save round trip, carries
// the field forward without naming it and is not caught here.
func TestClaudePermissionsHasOneReader(t *testing.T) {
	type site struct{ file, fn, detail string }
	var reads, writes, derives []site

	internalRoot := ".."
	err := filepath.WalkDir(internalRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(internalRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		visit := func(fn string, n ast.Node) {
			ast.Inspect(n, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if x.Sel.Name == "ClaudePermissions" {
						reads = append(reads, site{rel, fn, types.ExprString(x)})
					}
				case *ast.Ident:
					if x.Name == "derivePermissionMode" {
						derives = append(derives, site{rel, fn, x.Name})
					}
				case *ast.KeyValueExpr:
					if k, ok := x.Key.(*ast.Ident); ok && k.Name == "ClaudePermissions" {
						writes = append(writes, site{rel, fn, types.ExprString(x.Value)})
					}
				}
				return true
			})
		}
		for _, decl := range file.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok {
				visit(fd.Name.Name, fd)
			} else {
				visit("", decl)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// A selector read is the only way to reach the field's value, and an
	// assignment through one (x.ClaudePermissions = ...) is a selector too, so
	// this one list covers reads and copies alike.
	if len(reads) != 1 || reads[0].file != "cli/dispatch.go" || reads[0].fn != "runDispatch" {
		t.Errorf("ClaudePermissions must be read in exactly one place, runDispatch in cli/dispatch.go; found %v", reads)
	}
	for _, r := range reads {
		if strings.HasPrefix(r.file, "watch/") || r.file == "cli/watch.go" {
			t.Errorf("niwa watch must never read ClaudePermissions; found %s in %s (%s)", r.detail, r.file, r.fn)
		}
	}

	// A watch launch must never get a derived mode, whether by reading the
	// recorded posture or by calling the derivation on something it read
	// elsewhere and appending the result after a watch launch helper returns.
	for _, d := range derives {
		if d.file != "cli/dispatch.go" {
			t.Errorf("derivePermissionMode may only be used by dispatch; found it in %s (%s)", d.file, d.fn)
		}
	}

	var fns []string
	for _, w := range writes {
		if w.file != "workspace/apply.go" || w.detail != "result.claudePermissions" {
			t.Errorf("ClaudePermissions may only be set from the current run's pipeline result in workspace/apply.go; found %q in %s (%s)", w.detail, w.file, w.fn)
		}
		fns = append(fns, w.fn)
	}
	sort.Strings(fns)
	if !slices.Equal(fns, []string{"Apply", "Create"}) {
		t.Errorf("ClaudePermissions must be set by exactly Create and Apply; found it set in %v", fns)
	}
}
