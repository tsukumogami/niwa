package agentplan

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The layout scan reads the two packages as source and asserts the properties
// that make the contract structural rather than aspirational: internal/workspace
// does not know which agent it is preparing for, and internal/agentplan does not
// write. Both are checked over the parsed syntax tree rather than by grep, so a
// mention in a comment is not a violation and a mention in code cannot hide
// behind formatting.

// The filename half of the workspace scan ran behind a conversion window until
// this change. While the eight context-writer sites still named their targets
// inline -- three in content.go, one in worktree_content.go, three in
// workspace_context.go, and the dead rootClaudeFile constant in
// root_materializer.go -- a literal in one of those four files was skipped
// rather than failed, and a literal anywhere else failed immediately.
//
// The window was built to close itself: the skip was reached only while a known
// site remained, so converting the writers made the scan green without anyone
// flipping a flag. What happened instead is that the conversion landed in the
// same change as the scan, so the skip was never reached at all and the window
// was closed before it was committed.
//
// That leaves it as something other than a pending conversion. It is an
// exemption, and the four files it exempts are the context writers -- the ones
// a change would reintroduce a filename literal into. A literal in any of them
// skips; the same literal anywhere else in the package fails. So the half of
// this scan that guards filenames has never been enforced on the code most
// likely to break it. Deleting the window is what starts enforcing it, and the
// list is deleted rather than emptied because a named exemption is an
// invitation to add a fifth entry to it.
//
// The window was also meant to prove the detector fires, by sitting red over
// eight real violations. It never got to. TestContextFilenameScanReportsALiteral
// supplies that instead, running the same detector over source written to break
// the rule. To see the real scan red, name one of the filenames below in any
// non-test file in internal/workspace; the failure reports the file and line.

// agentContextFilenames is the set of agent context filenames no code in
// internal/workspace may name. Each one is a filename only one agent reads, so
// naming it is the same decision as naming the agent, taken where it cannot be
// reviewed as one.
var agentContextFilenames = []string{
	"CLAUDE.md",
	"CLAUDE.local.md",
	"AGENTS.md",
	"AGENTS.override.md",
}

// agentConstants is the set of agent discriminator constants no code in
// internal/workspace may name. Reaching for one is how a delivery decision gets
// made inside a writer instead of in the declaration table.
var agentConstants = []string{"AgentClaude", "AgentCodex"}

// forbiddenOSNames is the set of os package members internal/agentplan may not
// reference. The calls are the writes themselves; the O_ flags are what would
// otherwise leave os.OpenFile as an unguarded hole. Read-only access stays
// legal: os.ReadFile, os.Stat, and an O_NOFOLLOW read-only open are all absent
// from this list on purpose. The boundary is "reads inputs, declares outputs",
// not "pure".
var forbiddenOSNames = []string{
	"Chmod",
	"Create",
	"Link",
	"MkdirAll",
	"Mkdir",
	"O_APPEND",
	"O_CREATE",
	"O_RDWR",
	"O_TRUNC",
	"O_WRONLY",
	"Remove",
	"RemoveAll",
	"Rename",
	"Symlink",
	"Truncate",
	"WriteFile",
}

const (
	workspaceDir = "../workspace"
	leafDir      = "."
)

// violation is one place the scan found what it forbids.
type violation struct {
	file   string
	line   int
	detail string
}

// render formats violations one per line, sorted, for a failure message that
// names every site rather than the first one.
func render(vs []violation) string {
	slices.SortFunc(vs, func(a, b violation) int {
		if a.file != b.file {
			return strings.Compare(a.file, b.file)
		}
		return a.line - b.line
	})
	var sb strings.Builder
	for _, v := range vs {
		fmt.Fprintf(&sb, "  %s:%d: %s\n", v.file, v.line, v.detail)
	}
	return sb.String()
}

// parsedFile pairs a parsed syntax tree with the base name it came from.
type parsedFile struct {
	name string
	fset *token.FileSet
	file *ast.File
}

// parsePackageFiles parses every non-test Go file directly inside dir. Comments
// are deliberately not parsed: the scan asks what the code says, and the agent
// constants are named in several comments in internal/workspace that are
// documentation rather than behavior.
func parsePackageFiles(t *testing.T, dir string) []parsedFile {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var out []parsedFile
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", filepath.Join(dir, name), err)
		}
		out = append(out, parsedFile{name: name, fset: fset, file: f})
	}
	if len(out) == 0 {
		t.Fatalf("no non-test Go files found in %s; the scan would pass vacuously", dir)
	}
	return out
}

// parseSource parses one in-memory file under the given base name. It is what
// lets the detector below be exercised against source that is not in the tree,
// so the scan can be shown to fail without a file in internal/workspace that
// breaks the rule it enforces.
func parseSource(t *testing.T, name, src string) parsedFile {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	return parsedFile{name: name, fset: fset, file: f}
}

// position renders a node's line within its file.
func (p parsedFile) line(n ast.Node) int {
	return p.fset.Position(n.Pos()).Line
}

// contextFilenameViolations reports every string literal in files whose value is
// one of the agent context filenames. It is separate from the test that calls it
// over internal/workspace so the same detector can be run over source written to
// break the rule -- see TestContextFilenameScanReportsALiteral.
func contextFilenameViolations(files []parsedFile) []violation {
	var found []violation
	for _, pf := range files {
		ast.Inspect(pf.file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil || !slices.Contains(agentContextFilenames, value) {
				return true
			}
			found = append(found, violation{pf.name, pf.line(n), "names " + value})
			return true
		})
	}
	return found
}

// selectorName returns the "pkg.Name" form of a selector on a plain package
// identifier, and false for anything else (a method call on a value, a
// selector on an index expression, and so on).
func selectorName(n ast.Node) (pkg, name string, ok bool) {
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

// TestWorkspaceNamesNoAgentConstant asserts the workspace package never reaches
// for an agent discriminator constant. The agent reaches internal/workspace as
// a value threaded from the session; a package that can name the constants can
// branch on them, which is how a delivery decision escapes the declaration
// table.
func TestWorkspaceNamesNoAgentConstant(t *testing.T) {
	var found []violation
	for _, pf := range parsePackageFiles(t, workspaceDir) {
		ast.Inspect(pf.file, func(n ast.Node) bool {
			pkg, name, ok := selectorName(n)
			if !ok || pkg != "agent" || !slices.Contains(agentConstants, name) {
				return true
			}
			found = append(found, violation{pf.name, pf.line(n), "names agent." + name})
			return true
		})
	}
	if len(found) > 0 {
		t.Fatalf("internal/workspace names an agent constant at %d site(s); the agent is data threaded through the plan, not a branch:\n%s", len(found), render(found))
	}
}

// TestWorkspaceNamesNoAgentContextFilename asserts no code in the workspace
// package names a file only one agent reads. The filename belongs to the
// agent's producer in internal/agentplan; a writer that names one has taken a
// delivery decision where the declaration table cannot review it.
func TestWorkspaceNamesNoAgentContextFilename(t *testing.T) {
	found := contextFilenameViolations(parsePackageFiles(t, workspaceDir))
	if len(found) > 0 {
		t.Fatalf("internal/workspace names an agent context filename at %d site(s); the filename belongs to the agent's producer, not to the writer:\n%s", len(found), render(found))
	}
}

// TestContextFilenameScanReportsALiteral runs the same detector the scan above
// runs, over source written to break the rule, and asserts it reports every
// forbidden name at the line that names it.
//
// This is what the conversion window used to provide. While four files were
// legitimately red, every run exercised the detector against real violations;
// now that the tree is clean, the scan is green and would stay green if it
// stopped detecting anything at all. The synthetic file keeps the demonstration
// without keeping an exemption.
//
// The negative half matters as much as the positive one. The scan reads code
// rather than text on purpose -- internal/workspace names these files in several
// comments that are documentation, not behavior -- so a detector that flagged a
// comment would be reported as a violation the writer cannot fix.
func TestContextFilenameScanReportsALiteral(t *testing.T) {
	const src = `package workspace

// This comment names CLAUDE.md and AGENTS.md and is not a violation.
const claudeRoot = "CLAUDE.md"

func paths() []string {
	return []string{"CLAUDE.local.md", "AGENTS.md", "AGENTS.override.md", "workspace-context.md"}
}
`
	found := contextFilenameViolations([]parsedFile{parseSource(t, "writer.go", src)})

	got := make(map[string]int, len(found))
	for _, v := range found {
		got[v.detail] = v.line
	}
	want := map[string]int{
		"names CLAUDE.md":          4,
		"names CLAUDE.local.md":    7,
		"names AGENTS.md":          7,
		"names AGENTS.override.md": 7,
	}
	if len(got) != len(want) {
		t.Fatalf("scan reported %d violation(s), want %d:\n%s", len(got), len(want), render(found))
	}
	for detail, line := range want {
		if got[detail] != line {
			t.Fatalf("scan reported %q at line %d, want line %d:\n%s", detail, got[detail], line, render(found))
		}
	}
}

// TestLeafNeverWrites asserts internal/agentplan declares writes without
// performing them. The package's whole value is that its output is data a test
// can read, so a write here would be a delivery no assertion can see -- exactly
// the blind spot this contract exists to close.
func TestLeafNeverWrites(t *testing.T) {
	var found []violation
	for _, pf := range parsePackageFiles(t, leafDir) {
		ast.Inspect(pf.file, func(n ast.Node) bool {
			pkg, name, ok := selectorName(n)
			if !ok {
				return true
			}
			switch {
			case pkg == "exec":
				found = append(found, violation{pf.name, pf.line(n), "references exec." + name})
			case pkg == "os" && slices.Contains(forbiddenOSNames, name):
				found = append(found, violation{pf.name, pf.line(n), "references os." + name})
			}
			return true
		})
	}
	if len(found) > 0 {
		t.Fatalf("internal/agentplan writes or executes at %d site(s); it declares outputs and internal/workspace does the doing:\n%s", len(found), render(found))
	}
}
