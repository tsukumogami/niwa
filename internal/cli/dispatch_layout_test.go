package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This scan is the dispatch path's half of the structural discipline
// internal/agentplan's layout_scan_test.go applies to internal/workspace. It
// reads the files below as source and asserts over the syntax tree -- so a
// mention in a comment is not a violation and a mention in code cannot hide
// behind formatting -- that none of them names an agent.
//
// It is a new scan rather than an extension of the existing one because the
// existing one cannot be extended: its scope is two hardcoded directories, and
// internal/cli is neither. Nothing in this package inherited any structural
// discipline before this file existed, which is why dispatch.go's agent gate
// could be a bare comparison against a constant for as long as it was.
//
// It went red before it went green. Against the tree this scan arrived on, it
// failed at four sites -- the refusal's comparison and the hardcoded model
// resolution in dispatch.go, the per-agent model tables in dispatch_model.go,
// and the binary lookup in dispatch_launcher.go -- and turning those green by
// routing each through the launch declaration is the change this scan ships
// with. A scan that has never failed is a scan nobody has shown to work.

// dispatchPathFiles are the non-test files this scan covers: the ones that
// launch a background worker, recover which session it became, ask later
// whether that session still exists, and step back into it. Between them they
// are the whole of the DispatchLaunch capability's delivery.
var dispatchPathFiles = []string{
	"dispatch.go",
	"dispatch_capture.go",
	"dispatch_keepalive.go",
	"dispatch_launcher.go",
	"dispatch_model.go",
	"dispatch_remotecontrol.go",
	"dispatch_spill.go",
	"session_records.go",
}

// The two files that sit beside the dispatch path and are deliberately not
// scanned, each because the capability it delivers exists for one agent by
// declaration rather than by omission:
//
//	dispatch_plugins.go  registers plugins with Claude Code's own plugin
//	                     system. MarketplaceRegistration (row 6) is declared
//	                     AgentCannotReceive for Codex, so there is no second
//	                     agent for this file to be neutral about.
//	job_state.go         reads Claude Code's harness job-state file, for the
//	                     SessionStart guard behind EphemeralSessions (row 17,
//	                     AgentCannotReceive for Codex) and for `niwa watch`'s
//	                     review continuation, which is Claude Code harness
//	                     surface throughout.
//
// Naming them here rather than leaving them silently uncovered is the point: an
// exclusion a reader can check against a declaration is a different thing from
// an exclusion that exists because a file happened to fail.

// dispatchAgentConstants is the set of agent discriminator constants no scanned
// file may name. Reaching for one is how a delivery decision gets made at a
// call site instead of in the declaration table.
var dispatchAgentConstants = []string{"AgentClaude", "AgentCodex"}

// dispatchAgentLiterals is the set of string literals no scanned file may
// contain. Each one names a specific agent -- its binary, or the directory it
// keeps its own state in -- so writing one is the same decision as naming the
// agent, taken where it cannot be reviewed as one. They are the launch path's
// equivalent of the agent context filenames the workspace scan forbids.
var dispatchAgentLiterals = []string{
	"claude",
	"codex",
	".claude",
	".codex",
	"CLAUDE.md",
	"AGENTS.md",
}

// dispatchViolation is one place the scan found what it forbids.
type dispatchViolation struct {
	file   string
	line   int
	detail string
}

// renderDispatchViolations formats violations one per line, sorted, so a
// failure names every site rather than the first one.
func renderDispatchViolations(vs []dispatchViolation) string {
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].file != vs[j].file {
			return vs[i].file < vs[j].file
		}
		return vs[i].line < vs[j].line
	})
	var sb strings.Builder
	for _, v := range vs {
		fmt.Fprintf(&sb, "  %s:%d: %s\n", v.file, v.line, v.detail)
	}
	return sb.String()
}

// parseDispatchPathFiles parses each scanned file. Comments are deliberately not
// parsed: the scan asks what the code says, and several of these files explain
// in prose which agent a mechanism came from, which is documentation rather than
// behavior.
func parseDispatchPathFiles(t *testing.T) []parsedDispatchFile {
	t.Helper()
	var out []parsedDispatchFile
	for _, name := range dispatchPathFiles {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("scanned file %s is missing; the scan would pass by not looking: %v", name, err)
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		out = append(out, parsedDispatchFile{name: name, fset: fset, file: f})
	}
	if len(out) == 0 {
		t.Fatal("no files scanned; the scan would pass vacuously")
	}
	return out
}

type parsedDispatchFile struct {
	name string
	fset *token.FileSet
	file *ast.File
}

func (p parsedDispatchFile) line(n ast.Node) int {
	return p.fset.Position(n.Pos()).Line
}

// dispatchSelectorName returns the "pkg.Name" form of a selector on a plain
// package identifier, and false for anything else.
func dispatchSelectorName(n ast.Node) (pkg, name string, ok bool) {
	sel, isSel := n.(*ast.SelectorExpr)
	if !isSel {
		return "", "", false
	}
	ident, isIdent := sel.X.(*ast.Ident)
	if !isIdent {
		return "", "", false
	}
	return ident.Name, sel.Sel.Name, true
}

// agentConstantViolations returns every agent discriminator constant named in
// one parsed file.
func agentConstantViolations(pf parsedDispatchFile) []dispatchViolation {
	var found []dispatchViolation
	ast.Inspect(pf.file, func(n ast.Node) bool {
		pkg, name, ok := dispatchSelectorName(n)
		if !ok || pkg != "agent" || !slices.Contains(dispatchAgentConstants, name) {
			return true
		}
		found = append(found, dispatchViolation{pf.name, pf.line(n), "names agent." + name})
		return true
	})
	return found
}

// agentLiteralViolations returns every agent-naming string literal in one
// parsed file.
func agentLiteralViolations(pf parsedDispatchFile) []dispatchViolation {
	var found []dispatchViolation
	ast.Inspect(pf.file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil || !slices.Contains(dispatchAgentLiterals, value) {
			return true
		}
		found = append(found, dispatchViolation{pf.name, pf.line(n), "names " + value})
		return true
	})
	return found
}

// TestDispatchPathNamesNoAgentConstant asserts no scanned file reaches for an
// agent discriminator constant. The agent reaches the dispatch path as a value
// resolved from the workspace and the environment; a file that can name the
// constants can branch on them, which is how a launch decision escapes the
// declaration table.
func TestDispatchPathNamesNoAgentConstant(t *testing.T) {
	var found []dispatchViolation
	for _, pf := range parseDispatchPathFiles(t) {
		found = append(found, agentConstantViolations(pf)...)
	}
	if len(found) > 0 {
		t.Fatalf("the dispatch path names an agent constant at %d site(s); which agent is launched is data resolved once and threaded, not a branch:\n%s",
			len(found), renderDispatchViolations(found))
	}
}

// TestDispatchPathNamesNoAgentLiteral asserts no scanned file spells out an
// agent's binary or its own state directory. A launch that knows the binary's
// name knows which agent it is launching, whatever the surrounding code says
// about being neutral -- and a capture that knows where to look knows the same.
// Both come from the launch declaration instead.
func TestDispatchPathNamesNoAgentLiteral(t *testing.T) {
	var found []dispatchViolation
	for _, pf := range parseDispatchPathFiles(t) {
		found = append(found, agentLiteralViolations(pf)...)
	}
	if len(found) > 0 {
		t.Fatalf("the dispatch path names an agent's own binary or state directory at %d site(s); both belong to the agent's launch declaration:\n%s",
			len(found), renderDispatchViolations(found))
	}
}

// TestDispatchScanDetectsWhatItForbids runs the scan against source written to
// contain exactly what it looks for, and fails if it comes back clean.
//
// The two tests above pass on this tree, and a passing scan proves nothing on
// its own -- a detector that matched nothing would pass them too, forever, and
// the first person to reintroduce a hardcoded agent would get a green run. This
// one is the control. It also pins where the detector must NOT fire: on a
// comment, which is why the scan reads the syntax tree rather than the bytes,
// and on a literal that merely contains an agent's name as a substring.
func TestDispatchScanDetectsWhatItForbids(t *testing.T) {
	const src = `package cli

// This comment names agent.AgentClaude and "claude" and must not be a
// violation: prose about which agent a mechanism came from is documentation.
func offender(ag agent.Agent) string {
	if ag == agent.AgentClaude {
		return "claude"
	}
	if ag == agent.AgentCodex {
		return ".codex"
	}
	// A literal that contains an agent's name without being it. The scan
	// matches whole values, so this is legal.
	return "claude-code-is-not-the-binary-name"
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "offender.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	pf := parsedDispatchFile{name: "offender.go", fset: fset, file: f}

	constants := agentConstantViolations(pf)
	if len(constants) != 2 {
		t.Errorf("the constant scan found %d violation(s) in source with 2:\n%s", len(constants), renderDispatchViolations(constants))
	}
	literals := agentLiteralViolations(pf)
	if len(literals) != 2 {
		t.Errorf("the literal scan found %d violation(s) in source with 2:\n%s", len(literals), renderDispatchViolations(literals))
	}
	// The comment mentions both and sits on lines 3 and 4; nothing found may
	// point at them.
	for _, v := range append(slices.Clone(constants), literals...) {
		if v.line <= 4 {
			t.Errorf("the scan flagged a comment at %s:%d (%s); it must read code, not prose", v.file, v.line, v.detail)
		}
	}
}

// TestDispatchPathScanCoversTheLaunchSurface guards the scan's own scope. A
// scan is only worth its failure message if the set it ranges over is the set
// that matters, and the cheapest way for this one to stop mattering is for a
// new dispatch file to be added and not listed. Every non-test file whose name
// begins with "dispatch" is either scanned or explicitly excused above.
func TestDispatchPathScanCoversTheLaunchSurface(t *testing.T) {
	// The excused set, kept here rather than in a comment so adding a file
	// without a decision fails instead of passing quietly.
	excused := map[string]string{
		"dispatch_plugins.go": "registers plugins with one agent's own plugin system; row 6 is declared AgentCannotReceive for the other",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}
	var unlisted []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "dispatch") || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if slices.Contains(dispatchPathFiles, name) {
			continue
		}
		if _, ok := excused[name]; ok {
			continue
		}
		unlisted = append(unlisted, name)
	}
	if len(unlisted) > 0 {
		sort.Strings(unlisted)
		t.Fatalf("these dispatch files are neither scanned nor excused: %s\nAdd each to dispatchPathFiles, or excuse it here naming the declaration that makes it one agent's", strings.Join(unlisted, ", "))
	}
}
