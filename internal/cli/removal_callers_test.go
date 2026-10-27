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

// removalCaller is one entry of removalCallers: whether the caller asks the
// pre-removal gate before it removes anything, and why.
type removalCaller struct {
	gated bool
	why   string
}

// removalCallers names every non-test function in this package that calls a
// removal isRemovalCall matches, as "<file>:<function>".
//
// A new removal path has to be added here on purpose, which is the moment to
// decide whether it asks the pre-removal gate. Each entry says which way it
// went and why.
var removalCallers = map[string]removalCaller{
	// Gated: niwa removes these on its own judgment that nothing needs them,
	// so the workspace's command gets the last word.
	"reap.go:reapWorkspace": {true, "the mapped reap asks with step reap and the instance's mapped session ids"},
	"reap.go:reapBackstop":  {true, "the backstop for unmapped dispatch instances asks with step reap-backstop and no session ids"},
	"watch.go:discardStagedRecord": {true,
		"niwa watch's discard of a staged review asks with step watch-discard; a kept review keeps its instance and its record"},
	"destroy.go:runDestroyInstance": {true,
		"niwa destroy asks through destroyPreflight with step destroy before any prompt; --force overrides a refusal and names it"},
	"destroy.go:runDestroyWorkspace": {true,
		"niwa destroy --workspace asks through destroyPreflight with step destroy-workspace for every instance before the confirmation"},

	// Not gated yet.
	"reap_worktrees.go:sweepHookWorktreesIn": {false,
		"the worktree sweep removes only an unchanged hook-made worktree whose Claude Code session has ended; it does not ask the gate yet, and step worktree-sweep is reserved for it"},

	// Deliberately ungated.
	"destroy.go:runDestroyEmptyWorkspace": {false,
		"niwa destroy --workspace on a workspace with no instances: there is no instance path to ask about"},
	"dispatch.go:runDispatch": {false,
		"the rollback after a failed dispatch removes the instance this same call created, which no session has used"},
	"watch.go:stageReview": {false,
		"the rollback after a failed watch launch removes the review instance this same call created"},
	"reset.go:runReset": {false,
		"niwa reset is run by a person on a named instance, which it rebuilds in place"},
	"session_lifecycle_cmd.go:destroyOneWorktree": {false,
		"niwa session destroy / niwa worktree destroy on a named worktree: a person names the target"},
	"worktree_destroy_resolve.go:destroySessionWorktrees": {false,
		"niwa session destroy / niwa worktree destroy on a named session: a person names the target, and each worktree keeps its own dirty and lock guards"},
	"session_from_hook_cmd.go:teardownHookSession": {false,
		"WorktreeRemove and SubagentStop: the agent that owns the worktree is leaving it, and the dirty guard stays armed"},
	"session_from_hook_cmd.go:reconcileFailedHookCreate": {false,
		"the rollback after a failed hook worktree create, under the same dirty guard"},
	"instance_from_hook.go:destroyInstanceWarningTo": {false,
		"the production body of destroyInstanceFunc; whether to ask is its callers' decision, each listed here"},
}

// TestRemovalCallers_EveryCallerIsAccountedFor fails when a non-test function
// in this package calls a removal isRemovalCall matches and isn't named in
// removalCallers, when a named one no longer does, or when one marked gated
// no longer reaches the gate.
func TestRemovalCallers_EveryCallerIsAccountedFor(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := map[string]bool{}
	asks := map[string]bool{}
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
				key := name + ":" + fn.Name.Name
				if isRemovalCall(call.Fun) {
					found[key] = true
				}
				if isGateCall(call.Fun) {
					asks[key] = true
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
	var ungated []string
	for k, c := range removalCallers {
		if c.gated && found[k] && !asks[k] {
			ungated = append(ungated, k)
		}
	}
	sort.Strings(unlisted)
	sort.Strings(stale)
	sort.Strings(ungated)
	if len(unlisted) > 0 {
		t.Errorf("removal callers not in removalCallers (decide whether each asks the pre-removal gate, then list it): %v", unlisted)
	}
	if len(stale) > 0 {
		t.Errorf("removalCallers names functions that no longer remove anything: %v", stale)
	}
	if len(ungated) > 0 {
		t.Errorf("removalCallers marks these gated, but they no longer ask the gate: %v", ungated)
	}
}

// isGateCall reports whether fun reaches the pre-removal gate: building one
// (newRemovalGate), destroy's preflight over it (destroyPreflight), or asking
// one that was passed in (its ask or check method).
func isGateCall(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name == "newRemovalGate" || f.Name == "destroyPreflight"
	case *ast.SelectorExpr:
		return f.Sel.Name == "ask" || f.Sel.Name == "check"
	}
	return false
}

// isRemovalCall reports whether fun is destroyInstanceFunc,
// worktree.DestroySession, or one of the workspace removals they and niwa
// destroy reach: workspace.DestroyInstance and workspace.DestroyWorkspace.
func isRemovalCall(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name == "destroyInstanceFunc"
	case *ast.SelectorExpr:
		pkg, ok := f.X.(*ast.Ident)
		if !ok {
			return false
		}
		switch pkg.Name {
		case "worktree":
			return f.Sel.Name == "DestroySession"
		case "workspace":
			return f.Sel.Name == "DestroyInstance" || f.Sel.Name == "DestroyWorkspace"
		}
	}
	return false
}
