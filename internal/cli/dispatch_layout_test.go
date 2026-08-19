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

// excusedAgentNamingFiles are the files in this package that may name an agent,
// each with the declaration that makes the capability it serves one agent's
// rather than a gap. Membership is a recorded human decision, which is the
// whole reason the map holds a reason string rather than just a name.
var excusedAgentNamingFiles = map[string]string{
	"dispatch_plugins.go": "drives one agent's own plugin subcommand; MarketplaceRegistration (row 6) is declared AgentCannotReceive for the other",
	"job_state.go":        "reads one agent's harness job-state file, for EphemeralSessions (row 17, AgentCannotReceive for the other) and for niwa watch's review continuation",

	// The three below were red on this guard's first run, which is the guard
	// doing its job: each is agent-specific code in this package that no
	// earlier check looked at. Each is excused on a declaration rather than on
	// convenience, and each excusal is a claim a reader can check.
	"instance_from_hook.go": "serves the session-start hook behind EphemeralSessions (row 17), declared AgentCannotReceive for the other agent, and reads the context document only that agent's session would load",
	"repo_resolve.go":       "skips one agent's own directory when enumerating a workspace's repositories; the name is a directory to ignore rather than a delivery to make, and ignoring one that is not there costs nothing",
	"watch.go":              "the review continuation is that agent's harness surface throughout -- RemoteControl (row 20) is declared NoSuchConcept for the other, and the sensitive-location guard reasons about that agent's own home directory",
}

// TestNoUnreviewedAgentNamingInThisPackage is the completeness guard, and it is
// inverted on purpose.
//
// The obvious guard -- enumerate files whose names look like dispatch files and
// check each is scanned or excused -- has a hole the width of a filename. A new
// `codex_launcher.go` matches no name pattern, so it lands in neither list, the
// guard never looks at it, and it is free to hardcode an agent on every line
// while all the other tests stay green. That is exactly the failure this
// contract exists to prevent, sitting precisely where the next change lands,
// and `codex_launcher.go` is the *natural* name for that change: the hole is on
// the path of least resistance rather than at the end of an adversarial one.
// The file list above already disproves the premise -- `session_records.go` is
// on the dispatch path and shares no prefix with it.
//
// So the guard ranges over every non-test file in the package instead, and asks
// a question that needs no name pattern: does this file name an agent, and if
// so, has somebody decided that it may? A file on the dispatch path may not
// (the two scans above fail it). Any other file that does must be excused with
// a reason. A new file naming an agent fails on arrival whatever it is called.
func TestNoUnreviewedAgentNamingInThisPackage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}

	var offenders []dispatchViolation
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		// Files on the dispatch path are covered by the two scans above, which
		// hold them to a stricter rule: they may not name an agent at all.
		if slices.Contains(dispatchPathFiles, name) {
			continue
		}
		if _, excused := excusedAgentNamingFiles[name]; excused {
			continue
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		pf := parsedDispatchFile{name: name, fset: fset, file: f}
		offenders = append(offenders, agentConstantViolations(pf)...)
		offenders = append(offenders, agentLiteralViolations(pf)...)
	}
	if scanned == 0 {
		t.Fatal("no files scanned; the guard would pass vacuously")
	}
	if len(offenders) > 0 {
		t.Fatalf("%d site(s) in this package name an agent in a file that is neither on the dispatch path nor excused:\n%s\nEither route the behavior through the launch declaration and add the file to dispatchPathFiles, or add it to excusedAgentNamingFiles naming the declaration that makes it one agent's.",
			len(offenders), renderDispatchViolations(offenders))
	}
}

// TestDispatchPathFilesAreAllPresent keeps the scanned list from quietly
// shrinking. A file removed from dispatchPathFiles but still on disk would stop
// being held to the strict rule, and nothing else here would notice: the guard
// above would simply find it clean and move on.
func TestDispatchPathFilesAreAllPresent(t *testing.T) {
	for _, name := range dispatchPathFiles {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("scanned file %s is listed but not on disk: %v", name, err)
		}
	}
	for name := range excusedAgentNamingFiles {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("excused file %s is listed but not on disk: %v", name, err)
		}
		if slices.Contains(dispatchPathFiles, name) {
			t.Errorf("%s is both scanned and excused; it cannot be held to two rules", name)
		}
	}
}
