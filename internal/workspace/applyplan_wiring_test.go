package workspace

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

// This file checks the shape of the wiring rather than its behavior: that the
// executor takes a whole plan and nothing smaller, that this package never
// builds a plan of its own, and that the operation vocabulary is interpreted in
// one file. Together those three make the leaf package the only source of what
// niwa writes -- a property review has to be able to check mechanically,
// because the previous attempt at this boundary passed review and leaked
// anyway.

// planLeafPackage is the import path of the leaf package that produces plans.
const planLeafPackage = "github.com/tsukumogami/niwa/internal/agentplan"

// planExecutorFile is the one file allowed to interpret the plan vocabulary.
const planExecutorFile = "applyplan.go"

// planVocabulary is the set of leaf identifiers that mean "act on an entry":
// the operation and precondition types and their members. Any name beginning
// with "Op" is included by prefix as well, so a member added later is covered
// without editing this list.
var planVocabulary = map[string]bool{
	"Op":           true,
	"Precondition": true,
	"Always":       true,
}

// planCompositeTypes are the leaf types this package must never construct: a
// plan and its entries come from the leaf's producers, never from here.
var planCompositeTypes = map[string]bool{
	"Plan":  true,
	"Entry": true,
}

func TestExecutorTakesAWholePlan(t *testing.T) {
	fn := findApplyPlanDecl(t)

	params := fn.Type.Params.List
	if len(params) != 1 || len(params[0].Names) != 1 {
		t.Fatalf("applyPlan takes %d parameter groups, want exactly one parameter", len(params))
	}
	if got := planTypeString(params[0].Type); got != "*agentplan.Plan" {
		t.Errorf("applyPlan parameter is %s, want *agentplan.Plan", got)
	}

	if fn.Type.Results == nil {
		t.Fatal("applyPlan returns nothing, want ([]string, []string, error)")
	}
	var results []string
	for _, r := range fn.Type.Results.List {
		count := len(r.Names)
		if count == 0 {
			count = 1
		}
		for i := 0; i < count; i++ {
			results = append(results, planTypeString(r.Type))
		}
	}
	want := []string{"[]string", "[]string", "error"}
	if strings.Join(results, ",") != strings.Join(want, ",") {
		t.Errorf("applyPlan returns (%s), want (%s)",
			strings.Join(results, ", "), strings.Join(want, ", "))
	}
}

func TestWorkspaceConstructsNoPlanLiterals(t *testing.T) {
	forEachWorkspaceProductionFile(t, func(t *testing.T, name string, file *ast.File, fset *token.FileSet) {
		leaf := planLeafImportName(file)
		if leaf == "" {
			return
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			// A slice of entries is the same construction wearing a
			// different hat, and its elements elide their type, so the
			// element type is what gets checked.
			litType := lit.Type
			if arr, ok := litType.(*ast.ArrayType); ok && arr.Len == nil {
				litType = arr.Elt
			}
			sel, ok := litType.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != leaf {
				return true
			}
			if planCompositeTypes[sel.Sel.Name] {
				t.Errorf("%s: constructs %s.%s; plans come from the leaf package's producers",
					fset.Position(lit.Pos()), leaf, sel.Sel.Name)
			}
			return true
		})
	})
}

func TestPlanVocabularyIsInterpretedInOneFile(t *testing.T) {
	forEachWorkspaceProductionFile(t, func(t *testing.T, name string, file *ast.File, fset *token.FileSet) {
		if name == planExecutorFile {
			return
		}
		leaf := planLeafImportName(file)
		if leaf == "" {
			return
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != leaf {
				return true
			}
			if planVocabulary[sel.Sel.Name] || strings.HasPrefix(sel.Sel.Name, "Op") ||
				strings.HasPrefix(sel.Sel.Name, "If") {
				t.Errorf("%s: names %s.%s outside %s; the plan vocabulary is executed in one place",
					fset.Position(sel.Pos()), leaf, sel.Sel.Name, planExecutorFile)
			}
			return true
		})
	})
}

// findApplyPlanDecl returns the applyPlan declaration, failing the test when the
// executor is missing or is not a plain function.
func findApplyPlanDecl(t *testing.T) *ast.FuncDecl {
	t.Helper()

	var found *ast.FuncDecl
	forEachWorkspaceProductionFile(t, func(t *testing.T, name string, file *ast.File, fset *token.FileSet) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "applyPlan" {
				continue
			}
			if fn.Recv != nil {
				t.Fatalf("%s: applyPlan is a method; the executor is a package-level function",
					fset.Position(fn.Pos()))
			}
			if found != nil {
				t.Fatalf("%s: applyPlan declared more than once", fset.Position(fn.Pos()))
			}
			if name != planExecutorFile {
				t.Errorf("applyPlan lives in %s, want %s", name, planExecutorFile)
			}
			found = fn
		}
	})
	if found == nil {
		t.Fatal("no applyPlan declaration in internal/workspace")
	}
	return found
}

// forEachWorkspaceProductionFile parses the package's non-test files and hands each to
// check. Test files are excluded on purpose: a test may build a plan by hand,
// which is exactly what production code may not do.
func forEachWorkspaceProductionFile(t *testing.T, check func(t *testing.T, name string, file *ast.File, fset *token.FileSet)) {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing internal/workspace: %v", err)
	}
	pkg, ok := pkgs["workspace"]
	if !ok {
		t.Fatal("package workspace not found in .")
	}
	for name, file := range pkg.Files {
		check(t, name, file, fset)
	}
}

// planLeafImportName returns the local name the file uses for the leaf package, or
// "" when the file does not import it.
func planLeafImportName(file *ast.File) string {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != planLeafPackage {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "agentplan"
	}
	return ""
}

// planTypeString renders the type expressions this file compares against, which are
// only identifiers, selectors, pointers, and slices.
func planTypeString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return planTypeString(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + planTypeString(t.X)
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + planTypeString(t.Elt)
		}
	}
	return "?"
}
