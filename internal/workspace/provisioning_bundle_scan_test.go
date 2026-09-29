package workspace

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"testing"
)

// TestProvisioningBundlesAreBuiltThroughTheWrappingHelper holds the
// store fallback's opt-in sites together. apply.go may call
// resolve.BuildBundle only inside provisioningBundle, which wraps the
// providers, and the three provisioning layers must each be built
// through it. Credential sync opens its provider through
// openCredentialSyncProvider, which must never wrap it. The scan walks
// the syntax tree, so comments never count.
func TestProvisioningBundlesAreBuiltThroughTheWrappingHelper(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "apply.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing apply.go: %v", err)
	}

	var buildCalls []string
	var helperLabels []string
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
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "resolve" && fun.Sel.Name == "BuildBundle" {
					buildCalls = append(buildCalls, fn.Name.Name)
				}
			case *ast.Ident:
				if fun.Name == "provisioningBundle" && len(call.Args) == 5 {
					if lit, ok := call.Args[4].(*ast.BasicLit); ok {
						label, _ := strconv.Unquote(lit.Value)
						helperLabels = append(helperLabels, label)
					}
				}
			}
			return true
		})
	}

	if len(buildCalls) != 1 || buildCalls[0] != "provisioningBundle" {
		t.Errorf("resolve.BuildBundle is called from %v; apply.go may call it only inside provisioningBundle", buildCalls)
	}
	sort.Strings(helperLabels)
	want := []string{"global overlay", "workspace config", "workspace-overlay.toml"}
	if len(helperLabels) != len(want) {
		t.Fatalf("provisioningBundle builds %v, want the overlay, team and personal bundles %v", helperLabels, want)
	}
	for i := range want {
		if helperLabels[i] != want[i] {
			t.Fatalf("provisioningBundle builds %v, want %v", helperLabels, want)
		}
	}

	// Credential sync's provider build stays unwrapped.
	sync, err := parser.ParseFile(fset, "credentialsync.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing credentialsync.go: %v", err)
	}
	found := false
	for _, decl := range sync.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "openCredentialSyncProvider" {
			continue
		}
		found = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if x.Sel.Name == "Wrap" {
					t.Error("openCredentialSyncProvider wraps its bundle; credential sync must never use the store")
				}
			case *ast.Ident:
				if x.Name == "provisioningBundle" {
					t.Error("openCredentialSyncProvider builds through provisioningBundle")
				}
			}
			return true
		})
	}
	if !found {
		t.Error("credentialsync.go has no openCredentialSyncProvider; update this scan to the function that builds the credential-sync provider")
	}
}
