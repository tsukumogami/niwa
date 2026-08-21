package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
)

// TestEveryLaunchRequestFieldIsRead is the dead-field guard for launchRequest.
// TestEveryLaunchSpecFieldIsRead in internal/agentplan reflects over that
// package's own types and cannot reach this one, and launchRequest is where a
// launch-shaping decision now lands.
//
// It scans the ONE function that takes a launchRequest, and counts only
// selectors on that parameter, rather than grepping the package for each field
// name. Four of the nine names -- Spec, Mode, Env, Body -- collide with
// selectors that already live in this package (cmd.Env, os.Stdout), so a
// name-based scan reports them read whatever the launcher does with them.
func TestEveryLaunchRequestFieldIsRead(t *testing.T) {
	typ := reflect.TypeOf(launchRequest{})
	fields := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		fields[typ.Field(i).Name] = false
	}
	if len(fields) == 0 {
		t.Fatal("launchRequest has no fields; the guard would pass vacuously")
	}

	// A missing file lands here as a parse error, so the guard cannot pass by
	// failing to find what it scans.
	const launcherFile = "dispatch_launcher.go"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, launcherFile, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", launcherFile, err)
	}

	scanned := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Type.Params == nil {
			continue
		}
		for _, param := range fn.Type.Params.List {
			// By type, not by name, so renaming the parameter cannot blind it.
			if id, isIdent := param.Type.(*ast.Ident); !isIdent || id.Name != "launchRequest" || len(param.Names) == 0 {
				continue
			}
			scanned = true
			req := param.Names[0].Name
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, isSel := n.(*ast.SelectorExpr)
				if !isSel {
					return true
				}
				if base, isBase := sel.X.(*ast.Ident); isBase && base.Name == req {
					if _, tracked := fields[sel.Sel.Name]; tracked {
						fields[sel.Sel.Name] = true
					}
				}
				return true
			})
		}
	}
	if !scanned {
		t.Fatalf("no function in %s takes a launchRequest parameter; the guard would pass by not looking", launcherFile)
	}

	var unread []string
	for name, seen := range fields {
		if !seen {
			unread = append(unread, name)
		}
	}
	if len(unread) > 0 {
		t.Errorf("launchRequest fields never read in %s: %s.\nA field the launcher does not consult is either a decision that was not wired up or one nobody needed; neither should merge quietly.",
			launcherFile, strings.Join(unread, ", "))
	}
}
