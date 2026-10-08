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

func TestSetupTerminalAvailable_NeedsStdinAndStderr(t *testing.T) {
	for _, tc := range []struct {
		stdin, stderr, want bool
	}{
		{false, false, false},
		{true, false, false},
		{false, true, false},
		{true, true, true},
	} {
		stubCaptureTTY(t, tc.stdin, tc.stderr)
		if got := setupTerminalAvailable(); got != tc.want {
			t.Errorf("stdin=%v stderr=%v: setupTerminalAvailable() = %v, want %v",
				tc.stdin, tc.stderr, got, tc.want)
		}
	}
}

// TestNewInteractiveApplier_SignalsTerminalFromDescriptors pins the apply and
// create constructor: SetupTerminal follows the stdin/stderr terminal check.
func TestNewInteractiveApplier_SignalsTerminalFromDescriptors(t *testing.T) {
	for _, tc := range []struct {
		stdin, stderr, want bool
	}{
		{true, true, true},
		{true, false, false},
		{false, true, false},
		{false, false, false},
	} {
		stubCaptureTTY(t, tc.stdin, tc.stderr)
		a := newInteractiveApplier(nil)
		if a.SetupTerminal != tc.want {
			t.Errorf("stdin=%v stderr=%v: SetupTerminal = %v, want %v",
				tc.stdin, tc.stderr, a.SetupTerminal, tc.want)
		}
		if a.Reporter == nil {
			t.Fatal("newInteractiveApplier left Reporter nil")
		}
	}
}

// TestNewProvisionApplier_NeverSignalsTerminal pins the provisioning
// constructor: even with both descriptors on a terminal, the hook, dispatch,
// watch and reap paths do not tell setup scripts a terminal is there.
func TestNewProvisionApplier_NeverSignalsTerminal(t *testing.T) {
	stubCaptureTTY(t, true, true)
	a := newProvisionApplier(nil)
	if a.SetupTerminal {
		t.Error("newProvisionApplier set SetupTerminal; provisioning paths must leave it false")
	}
	if a.Reporter == nil {
		t.Fatal("newProvisionApplier left Reporter nil")
	}
}

// TestSetupTerminalWiring checks which entry points use which constructor, by
// reading this package's source. The constructor tests above prove what each
// builder does; this proves the commands use the right builder, and that no
// other code path turns the signal on behind them.
//
// Dispatch, watch and reap reach instance creation through
// provisionInstanceFunc, whose production value is realProvisionInstance, so
// pinning realProvisionInstance covers them along with the SessionStart hook.
func TestSetupTerminalWiring(t *testing.T) {
	fset := token.NewFileSet()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	callers := map[string][]string{} // constructor -> enclosing funcs
	var assigners []string           // funcs assigning a SetupTerminal field
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, p, src, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", p, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					if id, ok := n.Fun.(*ast.Ident); ok &&
						(id.Name == "newInteractiveApplier" || id.Name == "newProvisionApplier") {
						callers[id.Name] = append(callers[id.Name], name)
					}
				case *ast.AssignStmt:
					for _, lhs := range n.Lhs {
						if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "SetupTerminal" {
							assigners = append(assigners, name)
						}
					}
				case *ast.KeyValueExpr:
					if id, ok := n.Key.(*ast.Ident); ok && id.Name == "SetupTerminal" {
						assigners = append(assigners, name)
					}
				}
				return true
			})
		}
	}

	interactive := append([]string(nil), callers["newInteractiveApplier"]...)
	sort.Strings(interactive)
	if strings.Join(interactive, ",") != "runApply,runCreate" {
		t.Errorf("newInteractiveApplier is called from %v, want exactly runApply and runCreate", interactive)
	}

	provision := callers["newProvisionApplier"]
	if len(provision) != 1 || provision[0] != "realProvisionInstance" {
		t.Errorf("newProvisionApplier is called from %v, want exactly realProvisionInstance", provision)
	}

	if len(assigners) != 1 || assigners[0] != "newInteractiveApplier" {
		t.Errorf("SetupTerminal is assigned in %v, want only newInteractiveApplier", assigners)
	}
}
