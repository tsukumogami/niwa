package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestEveryLaunchRequestFieldIsRead is the dead-field guard for the type this
// branch introduced, and it exists because the existing guard does not reach it.
//
// `TestEveryLaunchSpecFieldIsRead` in internal/agentplan reflects over
// LaunchSpec, SessionRecords and LaunchFlags. launchRequest is a different type
// in a different package, so a field added to it and read by nothing would be
// invisible to that check -- and launchRequest is exactly where a
// launch-shaping decision now lands, which is the shape the whole contract
// exists to catch.
//
// It is deliberately stricter than its sibling in one way. The sibling greps
// the package for a selector matching each field name, so a field whose name
// collides with an unrelated selector counts as read. Four of these nine names
// -- Spec, Mode, Env, Body -- collide with something in this package, so that
// method would report them read whatever the launcher did with them. This scans
// the ONE function that consumes a launchRequest and asks whether the field is
// selected off the request value there, which is the question that matters:
// not "does this word appear" but "does the launcher look at this field".
func TestEveryLaunchRequestFieldIsRead(t *testing.T) {
	typ := reflect.TypeOf(launchRequest{})
	fields := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		fields[typ.Field(i).Name] = false
	}
	if len(fields) == 0 {
		t.Fatal("launchRequest has no fields; the guard would pass vacuously")
	}

	// The launcher is the only consumer, so it is the only place a field can
	// be honestly read. Scanning the whole package would reintroduce the
	// collision problem this test exists to avoid.
	const launcherFile = "dispatch_launcher.go"
	if _, err := os.Stat(launcherFile); err != nil {
		t.Fatalf("scanned file %s is missing; the guard would pass by not looking: %v", launcherFile, err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, launcherFile, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", launcherFile, err)
	}

	// A selector on any identifier counts, because the request travels under
	// more than one name (the parameter, and locals it is copied into). What
	// this rules out is a field nothing in the launcher ever selects.
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, tracked := fields[sel.Sel.Name]; tracked {
			fields[sel.Sel.Name] = true
		}
		return true
	})

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
