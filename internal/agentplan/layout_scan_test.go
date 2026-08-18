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

// contextWritersPendingConversion gates the filename half of the workspace scan
// while the eight context-writer sites still name their targets inline:
//
//	internal/workspace/content.go            (three sites)
//	internal/workspace/worktree_content.go   (one site)
//	internal/workspace/workspace_context.go  (three sites)
//	internal/workspace/root_materializer.go  (the dead rootClaudeFile constant)
//
// Those sites are red on purpose. A scan that has never failed is a scan nobody
// has shown to work, and this one is the answer to a prior attempt whose
// structure compiled cleanly while doing the wrong thing. Issue 5 converts the
// context writers to plan producers and deletes the dead constant, issue 6
// finishes the conversion window, and the scan goes green on its own -- the
// skip below is reached only while a known site remains, so no one has to
// remember to flip anything.
//
// Set this to false to see the red: the known sites become a failure naming
// each one. Deleting the constant, and the branch it guards, is issue 5's to
// do once the sites are gone. A filename literal appearing in any other file is
// a failure now, not a skip.
const contextWritersPendingConversion = true

// knownRedContextWriters names the files whose filename literals are expected
// until the conversion lands. Membership is by file rather than by line, so an
// unrelated edit that shifts a line does not turn the window into a false
// failure -- while a literal in a fifth file still fails immediately.
var knownRedContextWriters = map[string]bool{
	"content.go":           true,
	"worktree_content.go":  true,
	"workspace_context.go": true,
	"root_materializer.go": true,
}

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

// position renders a node's line within its file.
func (p parsedFile) line(n ast.Node) int {
	return p.fset.Position(n.Pos()).Line
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
// package names a file only one agent reads. See contextWritersPendingConversion
// for why this is expected to skip until the context writers are converted.
func TestWorkspaceNamesNoAgentContextFilename(t *testing.T) {
	var found []violation
	for _, pf := range parsePackageFiles(t, workspaceDir) {
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

	var known, unexpected []violation
	for _, v := range found {
		if knownRedContextWriters[v.file] {
			known = append(known, v)
		} else {
			unexpected = append(unexpected, v)
		}
	}
	if len(unexpected) > 0 {
		t.Fatalf("internal/workspace names an agent context filename at %d new site(s), outside the sites issue 5 converts:\n%s", len(unexpected), render(unexpected))
	}
	if len(known) == 0 {
		return
	}
	if !contextWritersPendingConversion {
		t.Fatalf("internal/workspace names an agent context filename at %d site(s); the filename belongs to the agent's producer, not to the writer:\n%s", len(known), render(known))
	}
	t.Skipf("expected red until issue 5 converts the context writers and issue 6 closes the window: %d site(s) still name an agent context filename:\n%s", len(known), render(known))
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
