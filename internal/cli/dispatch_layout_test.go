package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
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
//	                     agent for this file to be neutral about. It also holds
//	                     claudeLaunchSpec, which the other Claude-only paths
//	                     (job_state.go's callers, `niwa watch`) read Claude
//	                     Code's binary and management verbs through -- one
//	                     place naming that agent, reading the same table the
//	                     neutral path reads, rather than five.
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
// contain. Each one names a specific agent -- its binary, the directory it
// keeps its own state in, or a field of the session record it writes there --
// so writing one is the same decision as naming the agent, taken where it
// cannot be reviewed as one. They are the launch path's equivalent of the agent
// context filenames the workspace scan forbids.
//
// The second group is the record schema, and it is here because the capture
// path's agent-specific knowledge is not the word "codex". It is where the
// records live, what the files are called, and which key inside one holds the
// session id -- every part of which agentplan's SessionRecords declares
// (HomeEnv, HomePath, FileName, FileGlob, IDPath) and the capture path is
// supposed to read from there. A capture that spells any of them out has
// re-derived the schema at the call site while still passing a scan that only
// looks for the agent's name, which is exactly what a reviewer demonstrated by
// planting a table of them in dispatch_capture.go and watching both scans come
// back clean.
var dispatchAgentLiterals = []string{
	"claude",
	"codex",
	".claude",
	".codex",
	"CLAUDE.md",
	"AGENTS.md",

	// The session-record schema, one entry per declared field.
	"CODEX_HOME",      // SessionRecords.HomeEnv
	"jobs",            // SessionRecords.HomePath, one agent's store
	"state.json",      // SessionRecords.FileName
	"rollout-*.jsonl", // SessionRecords.FileGlob
	"sessionId",       // SessionRecords.IDPath
}

// "sessions" -- the other agent's HomePath tail, and the obvious companion to
// "jobs" above -- is deliberately absent. It is not this agent's word: niwa has
// its own sessions and keeps them in `.niwa/sessions`, which apply.go,
// completion.go, and session_from_hook_cmd.go each build from that same
// literal. Forbidding it would fail the package guard at three sites that have
// nothing to do with any agent, and the only way to keep them green would be to
// excuse three unrelated files with reasons that are not true. A scan that has
// to be lied to in order to pass is worse than a scan with one fewer string in
// it. "jobs" carries no such collision -- nothing in niwa is called a job --
// which is why the pair splits.

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

// agentPackagePath is the import path the discriminator constants live at.
const agentPackagePath = "github.com/tsukumogami/niwa/internal/agent"

// agentPackageBinding is how one file refers to the agent package: the
// identifier it is bound to, or dot-imported so its constants appear bare.
type agentPackageBinding struct {
	ident string
	dot   bool
}

// resolveAgentPackageBinding reads the file's own import declarations rather
// than assuming the package identifier is "agent".
//
// Assuming it is a hole the width of one keyword: `import ag ".../agent"`
// followed by `ag.AgentCodex` is the same hardcoded branch the constant scan
// exists to catch, written by anyone who runs goimports next to a variable
// already called agent -- and a scan comparing against the literal "agent"
// never sees it. A dot import is the same evasion one step further, putting
// AgentCodex in scope as a bare identifier with no selector at all.
//
// A file that imports nothing under the name "agent" falls back to it anyway,
// so the scan does not go quiet on a fixture or a file whose import is written
// in a form this cannot resolve. The fallback yields only where some other
// import has already claimed the identifier, because there `agent.X` provably
// is not this package.
func resolveAgentPackageBinding(f *ast.File) agentPackageBinding {
	claimedByAnother := false
	for _, spec := range f.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		local := path.Base(importPath)
		if spec.Name != nil {
			local = spec.Name.Name
		}
		if importPath != agentPackagePath {
			if local == "agent" {
				claimedByAnother = true
			}
			continue
		}
		switch local {
		case ".":
			return agentPackageBinding{dot: true}
		case "_":
			// Imported for side effects only; nothing can be named through it.
			return agentPackageBinding{}
		default:
			return agentPackageBinding{ident: local}
		}
	}
	if claimedByAnother {
		return agentPackageBinding{}
	}
	return agentPackageBinding{ident: "agent"}
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
// one parsed file, under whatever identifier that file binds the agent package
// to.
func agentConstantViolations(pf parsedDispatchFile) []dispatchViolation {
	binding := resolveAgentPackageBinding(pf.file)
	var found []dispatchViolation
	record := func(n ast.Node, qualifier, name string) {
		found = append(found, dispatchViolation{pf.name, pf.line(n), "names " + qualifier + name})
	}
	ast.Inspect(pf.file, func(n ast.Node) bool {
		if binding.dot {
			// Dot-imported: the constants are in scope unqualified, and a
			// selector on them cannot occur, so bare identifiers are the whole
			// surface.
			if ident, isIdent := n.(*ast.Ident); isIdent && slices.Contains(dispatchAgentConstants, ident.Name) {
				record(n, "", ident.Name)
			}
			return true
		}
		if binding.ident == "" {
			return false
		}
		pkg, name, ok := dispatchSelectorName(n)
		if !ok || pkg != binding.ident || !slices.Contains(dispatchAgentConstants, name) {
			return true
		}
		record(n, binding.ident+".", name)
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

// parseDispatchFixture parses in-memory source as if it were a scanned file.
func parseDispatchFixture(t *testing.T, name, src string) parsedDispatchFile {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	return parsedDispatchFile{name: name, fset: fset, file: f}
}

// TestDispatchScanDetectsARecordSchemaTable replants, verbatim, the table a
// reviewer put in dispatch_capture.go to show the literal scan was looking for
// the wrong thing. It named no agent -- neither "codex" nor ".codex" appears in
// it -- and yet it hardcoded the entire Codex record schema: the environment
// variable holding the store root, the filename pattern, the other agent's
// record filename, and the key inside it. Both scans passed.
//
// The strings are the whole test. If someone trims dispatchAgentLiterals back
// to agent names, this goes red with the same table that got through before.
func TestDispatchScanDetectsARecordSchemaTable(t *testing.T) {
	const src = `package cli

var mutationProbeHome = map[string]string{
	"CODEX_HOME": "rollout-*.jsonl",
	"state.json": "sessionId",
}
`
	pf := parseDispatchFixture(t, "dispatch_capture.go", src)

	found := agentLiteralViolations(pf)
	if len(found) != 4 {
		t.Fatalf("the literal scan found %d violation(s) in a table that hardcodes four schema fields; the record schema belongs to the launch declaration:\n%s",
			len(found), renderDispatchViolations(found))
	}
	for _, want := range []string{"CODEX_HOME", "rollout-*.jsonl", "state.json", "sessionId"} {
		if !violationsName(found, want) {
			t.Errorf("the scan did not flag %q", want)
		}
	}
}

// TestDispatchScanFollowsAnImportAlias pins the other half of the same evasion.
// The constant scan used to compare the selector's package identifier against
// the literal string "agent", so renaming the import defeated it completely --
// `import ag ".../internal/agent"` and then `ag.AgentCodex` is the same
// hardcoded branch under a name the scan was not told to look for. The
// identifier now comes from the file's own import declarations.
func TestDispatchScanFollowsAnImportAlias(t *testing.T) {
	t.Run("alias", func(t *testing.T) {
		src := `package cli

import ag "` + agentPackagePath + `"

func offender(a ag.Agent) bool {
	return a == ag.AgentCodex || a == ag.AgentClaude
}
`
		found := agentConstantViolations(parseDispatchFixture(t, "offender.go", src))
		if len(found) != 2 {
			t.Fatalf("the constant scan found %d violation(s) behind an import alias; want 2:\n%s",
				len(found), renderDispatchViolations(found))
		}
	})

	t.Run("dot import", func(t *testing.T) {
		src := `package cli

import . "` + agentPackagePath + `"

func offender(a Agent) bool {
	return a == AgentCodex
}
`
		found := agentConstantViolations(parseDispatchFixture(t, "offender.go", src))
		if len(found) != 1 {
			t.Fatalf("the constant scan found %d violation(s) behind a dot import; want 1:\n%s",
				len(found), renderDispatchViolations(found))
		}
	})

	t.Run("an unrelated package called agent is not the agent package", func(t *testing.T) {
		// The fallback must not turn any selector spelled `agent.X` into a
		// violation when the file imports something else under that name.
		src := `package cli

import "example.com/other/agent"

func fine() string { return agent.AgentCodex }
`
		found := agentConstantViolations(parseDispatchFixture(t, "fine.go", src))
		if len(found) != 0 {
			t.Errorf("the scan flagged a selector on a different package named agent:\n%s", renderDispatchViolations(found))
		}
	})
}

// violationsName reports whether any violation's detail names the given value.
func violationsName(vs []dispatchViolation, value string) bool {
	for _, v := range vs {
		if strings.Contains(v.detail, value) {
			return true
		}
	}
	return false
}

// excusedAgentNamingFiles are the files in this package that may name an agent,
// each with the declaration that makes the capability it serves one agent's
// rather than a gap. Membership is a recorded human decision, which is the
// whole reason the map holds a reason string rather than just a name.
var excusedAgentNamingFiles = map[string]string{
	"dispatch_plugins.go": "drives one agent's own plugin subcommand and, for the same reason, hosts the accessor the other agent-specific paths read that agent's launch description through; MarketplaceRegistration (row 6) is declared AgentCannotReceive for the other agent",
	// This justification named row 17 for the whole file and left its largest
	// consumer out, which is the same conflation row 17's own reason carried:
	// the hook guard is row 17's, but the reaper reads this file about a
	// session niwa launched, which is row 22's. Naming both is what keeps the
	// excusal a claim a reader can check.
	"job_state.go": "reads one agent's harness job-state file for three callers: the session-start hook's background-worker guard (EphemeralSessions, row 17, AgentCannotReceive for the other agent), the reaper's liveness rule for a session that agent launched (DispatchLaunch, row 22, where the other agent's records are read agent-neutrally through session_records.go), and niwa watch's review continuation",

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
