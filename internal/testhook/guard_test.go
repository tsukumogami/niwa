package testhook

import (
	"bufio"
	"bytes"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The guards below read the module's sources. They run in the untagged
// build, under go test ./..., so a hook that leaks into a release build
// fails the ordinary test run.

// moduleRoot is the directory holding go.mod, found upward from the package.
func moduleRoot(t *testing.T) string {
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

// goFiles lists every .go file of the module, skipping hidden directories
// (nested worktrees live under one) and testdata.
func goFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
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
		if strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no Go files under %s", root)
	}
	return files
}

// buildConstraint returns the //go:build expression of src, or nil when it
// has none.
func buildConstraint(t *testing.T, path string, src []byte) constraint.Expr {
	t.Helper()
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "package ") {
			return nil
		}
		if constraint.IsGoBuild(line) {
			expr, err := constraint.Parse(line)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			return expr
		}
	}
	return nil
}

// inReleaseBuild reports whether a file with expr can be compiled without
// the niwa_functional tag: with every other tag on, or every other tag off,
// the expression holds.
func inReleaseBuild(expr constraint.Expr) bool {
	if expr == nil {
		return true
	}
	allOn := expr.Eval(func(tag string) bool { return tag != "niwa_functional" })
	allOff := expr.Eval(func(string) bool { return false })
	return allOn || allOff
}

func TestGuard_NoReleaseBuildFileNamesTheTestHookVariables(t *testing.T) {
	names := []string{"NIWA_TEST_MIRROR_LOCK_WAIT", "NIWA_TEST_GIT_ENV_PROBE"}
	root := moduleRoot(t)
	tagged := 0
	for _, path := range goFiles(t, root) {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !inReleaseBuild(buildConstraint(t, path, src)) {
			tagged++
			continue
		}
		for _, name := range names {
			if bytes.Contains(src, []byte(name)) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s names %s but is built without the niwa_functional tag", rel, name)
			}
		}
	}
	if tagged == 0 {
		t.Fatal("no file built only with the niwa_functional tag was found; the guard reads nothing")
	}
}

// The labels Fault answers, which must never reach testfault.Maybe except
// through Fault.
var guardedLabels = map[string]string{
	LabelBorrowClone:    "LabelBorrowClone",
	LabelDissociateCopy: "LabelDissociateCopy",
}

func TestGuard_TheNewFaultLabelsAreReachedOnlyThroughTesthook(t *testing.T) {
	root := moduleRoot(t)
	own := filepath.Join(root, "internal", "testhook")
	byDir := map[string][]string{}
	for _, path := range goFiles(t, root) {
		if filepath.Dir(path) == own {
			continue
		}
		byDir[filepath.Dir(path)] = append(byDir[filepath.Dir(path)], path)
	}
	calls := 0
	for _, paths := range byDir {
		fset := token.NewFileSet()
		var files []*ast.File
		consts := map[string]string{}
		for _, path := range paths {
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}
			files = append(files, f)
			for _, decl := range f.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.CONST {
					continue
				}
				for _, spec := range gen.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if i < len(vs.Values) {
							if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								if v, err := strconv.Unquote(lit.Value); err == nil {
									consts[name.Name] = v
								}
							}
						}
					}
				}
			}
		}
		for _, f := range files {
			faultName, hookName := importNames(f)
			if faultName == "" {
				continue
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Maybe" {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); !ok || x.Name != faultName {
					return true
				}
				calls++
				if label, ok := labelOf(call.Args[0], consts, hookName); ok {
					if _, guarded := guardedLabels[label]; guarded {
						t.Errorf("%s calls testfault.Maybe(%q) directly; reach it through testhook.Fault", fset.Position(call.Pos()), label)
					}
				}
				return true
			})
		}
	}
	if calls == 0 {
		t.Fatal("no testfault.Maybe call was found; the guard reads nothing")
	}
}

// importNames returns the names f refers to internal/testfault and
// internal/testhook by, empty when f doesn't import one.
func importNames(f *ast.File) (fault, hook string) {
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		name := filepath.Base(path)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		switch path {
		case "github.com/tsukumogami/niwa/internal/testfault":
			fault = name
		case "github.com/tsukumogami/niwa/internal/testhook":
			hook = name
		}
	}
	return fault, hook
}

// labelOf returns the label arg names when it's a string literal, a
// package constant set to one, or one of testhook's label constants.
func labelOf(arg ast.Expr, consts map[string]string, hookName string) (string, bool) {
	switch a := arg.(type) {
	case *ast.BasicLit:
		if a.Kind == token.STRING {
			v, err := strconv.Unquote(a.Value)
			return v, err == nil
		}
	case *ast.Ident:
		v, ok := consts[a.Name]
		return v, ok
	case *ast.SelectorExpr:
		if x, ok := a.X.(*ast.Ident); ok && hookName != "" && x.Name == hookName {
			for label, name := range guardedLabels {
				if a.Sel.Name == name {
					return label, true
				}
			}
		}
	}
	return "", false
}

func TestGuard_InReleaseBuildReadsConstraints(t *testing.T) {
	cases := map[string]bool{
		"":                                     true,
		"//go:build niwa_functional":           false,
		"//go:build !niwa_functional":          true,
		"//go:build darwin && niwa_functional": false,
		"//go:build linux || niwa_functional":  true,
		"//go:build !windows":                  true,
	}
	for line, want := range cases {
		var expr constraint.Expr
		if line != "" {
			var err error
			if expr, err = constraint.Parse(line); err != nil {
				t.Fatal(err)
			}
		}
		if got := inReleaseBuild(expr); got != want {
			t.Errorf("inReleaseBuild(%q) = %v, want %v", line, got, want)
		}
	}
}
