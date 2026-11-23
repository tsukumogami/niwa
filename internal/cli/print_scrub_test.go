package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// printScrubFiles are the files on the reap and pass path whose print sites
// TestReapPathPrintsScrubUnitText covers: the pass, reap and its sweeps, the
// removal gate, the on-reap command, the lock's kept line, the verdict
// printers, the line writer that scrubs what niwa hands on, the hook's
// teardown (realDestroyInstance) and the rest of the hook's file, and the
// session record listing the worktree sweep reads. Paths are relative to
// this package.
var printScrubFiles = []string{
	"pass.go",
	"reap.go",
	"removal_gate.go",
	"on_reap.go",
	"instance_lock.go",
	"verdicts.go",
	"launch_records.go",
	"reap_worktrees.go",
	"scrub_writer.go",
	"instance_from_hook.go",
	filepath.Join("..", "worktree", "session_lifecycle.go"),
}

// printScrubWrappers are the named wrappers an argument may pass through
// instead of calling scrub.ScrubEvidence itself. The check confirms each
// one's body calls scrub.ScrubEvidence, from the leaf package internal/scrub.
// It also accepts removal.ScrubEvidence, the forwarder to it, but code under
// internal/worktree must use the leaf scrub package and never the removal
// forwarder: internal/worktree must not import internal/removal.
var printScrubWrappers = []string{"scrubGateText", "scrubOnReapText", "scrubJoined"}

// printAllow allows the named arguments of the print calls in one function
// of one file: each is niwa-made, so it needs no scrubbing. Entries are
// keyed by file, function and what, never by line, and every entry must
// still match a call, so one left behind by a change fails the check.
type printAllow struct {
	file, fn, what string
	// args are the arguments as written in the source.
	args []string
	why  string
}

var printScrubAllow = []printAllow{
	{
		file: "pass.go", fn: "reportKeptUnit", what: "the kept line's check name",
		args: []string{"k.Check.Name"},
		why:  "a removal.CheckName constant the rule sets; the name and evidence beside it are scrubbed",
	},
	{
		file: "reap.go", fn: "runReap", what: "the reaped and removed counts",
		args: []string{"n", "m"},
		why:  "counts niwa computed",
	},
	{
		file: "reap.go", fn: "reportSparedInstances", what: "the spared line's count and overflow",
		args: []string{"len(names)", "suffix"},
		why:  `a count, and " and N more" built from one`,
	},
	{
		file: "reap.go", fn: "reapOpportunistically", what: "the reaped count",
		args: []string{"n"},
		why:  "a count niwa computed",
	},
	{
		file: "removal_gate.go", fn: "removalGate.ask", what: "the spare line's reason",
		args: []string{"reason"},
		why: "check builds every reason from niwa's own words, and each part that comes from the command " +
			"or the configuration (its last output line, its start error, the configuration error and path) " +
			"passes scrubGateText where check builds it; scrubbing the whole reason again would reach into the quoted line",
	},
	{
		file: "verdicts.go", fn: "printVerdictsText", what: "the verdict label, check names, classes and notices",
		args: []string{"u.verdictLabel()", "c.Name", "pc.Class", "notice"},
		why:  "each is one of removal's own Kind, CheckName, Class or Notice values",
	},
	{
		file: "verdicts.go", fn: "printVerdictsJSON", what: "the niwa.verdicts/v1 document",
		args: []string{"doc"},
		why:  "the document holds niwa's own values and unit-sourced fields each passed through removal.ScrubEvidence as it is built above the encode",
	},
	{
		file: "instance_from_hook.go", fn: "runInstanceHookStart", what: "the SessionStart reply",
		args: []string{"out"},
		why: "the JSON reply niwa marshals from its own sessionStartInjection for Claude Code to read, not a terminal line; " +
			"encoding/json escapes every control character in it",
	},
	{
		file: "instance_from_hook.go", fn: "runInstanceHookStart", what: "the reap key warning's session id and error",
		args: []string{"payload.SessionID", "err"},
		why: "the session id passed workspace.ValidSessionID at the top of the function, so it is a canonical UUID; " +
			"the error is writeReapKeyEnvFile's own, from opening or writing the env file whose path Claude Code sets, " +
			"not anything an instance holds",
	},
	{
		file: "instance_from_hook.go", fn: "writeHookMirrorNotices", what: "the mirror notices reply",
		args: []string{"append(encoded, '\\n')"},
		why: "the JSON reply niwa marshals from its own sessionStartInjection for Claude Code to read, not a terminal line; " +
			"encoding/json escapes every control character in it",
	},
	{
		file: "instance_from_hook.go", fn: "writeReapKeyEnvFile", what: "the env file's export line",
		args: []string{"config.ReapKeyEnv", "key"},
		why:  "a constant name, and a key validReapKey checked holds only letters, digits, '_' and '-'",
	},
}

// printViolation is one print site that hands on an argument the check
// can't show was scrubbed or niwa-made.
type printViolation struct {
	file, fn, call, arg string
}

func (v printViolation) String() string {
	return fmt.Sprintf("%s: %s: %s prints %s unscrubbed", v.file, v.fn, v.call, v.arg)
}

// printScrubResult is what checkPrintScrub found.
type printScrubResult struct {
	violations []printViolation
	// badWrappers are wrappers that are missing or don't call
	// scrub.ScrubEvidence (or removal.ScrubEvidence, its forwarder).
	badWrappers []string
	// unused are allow-list entries no call matched.
	unused []printAllow
}

// checkPrintScrub checks every print call in files (by base name) against
// the rule: each argument it prints is a literal, a call to
// scrub.ScrubEvidence (or removal.ScrubEvidence, its forwarder) or to one of
// wrappers that calls it, or an argument allow lists for that file and
// function. A format string must be a literal.
//
// A print call is fmt's Fprint, Fprintf, Fprintln, Print, Printf and
// Println, log's Print, Printf and Println, io.WriteString, and any Write,
// WriteString, WriteByte, WriteRune or Encode method call.
func checkPrintScrub(files map[string]*ast.File, allow []printAllow, wrappers []string) printScrubResult {
	var res printScrubResult
	decls := map[string]*ast.FuncDecl{}
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
				decls[fd.Name.Name] = fd
			}
		}
	}
	scrubbing := map[string]bool{}
	for _, name := range wrappers {
		fd, ok := decls[name]
		if !ok || fd.Body == nil || !callsScrubEvidence(fd.Body) {
			res.badWrappers = append(res.badWrappers, name)
			continue
		}
		scrubbing[name] = true
	}
	isScrubbed := func(e ast.Expr) bool {
		call, ok := ast.Unparen(e).(*ast.CallExpr)
		if !ok {
			return false
		}
		if isScrubEvidence(call) {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		return ok && scrubbing[id.Name]
	}
	used := make([]bool, len(allow))
	allowed := func(file, fn, arg string) bool {
		for i, a := range allow {
			if a.file == file && a.fn == fn && slices.Contains(a.args, arg) {
				used[i] = true
				return true
			}
		}
		return false
	}

	inspect := func(file, fn string, body ast.Node) {
		ast.Inspect(body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			label, format, values, ok := printCall(call)
			if !ok {
				return true
			}
			if format != nil && !isLiteral(format) {
				res.violations = append(res.violations, printViolation{file, fn, label, "a format that is not a literal"})
			}
			for _, v := range values {
				if isLiteral(v) || isScrubbed(v) {
					continue
				}
				arg := types.ExprString(v)
				if !allowed(file, fn, arg) {
					res.violations = append(res.violations, printViolation{file, fn, label, arg})
				}
			}
			return true
		})
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, file := range names {
		for _, d := range files[file].Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Body != nil {
					inspect(file, funcName(d), d.Body)
				}
			case *ast.GenDecl:
				// A package variable's value, a function literal among them,
				// is named by the variable.
				for _, spec := range d.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok && len(vs.Names) > 0 {
						inspect(file, vs.Names[0].Name, vs)
					}
				}
			}
		}
	}
	for i, a := range allow {
		if !used[i] {
			res.unused = append(res.unused, a)
		}
	}
	return res
}

// printCall reports whether call prints, and if so a label for it, its
// format string when it has one, and the values it prints.
func printCall(call *ast.CallExpr) (label string, format ast.Expr, values []ast.Expr, ok bool) {
	sel, isSel := call.Fun.(*ast.SelectorExpr)
	if !isSel {
		return "", nil, nil, false
	}
	args := call.Args
	if pkg, isIdent := sel.X.(*ast.Ident); isIdent {
		label = pkg.Name + "." + sel.Sel.Name
		switch pkg.Name + "." + sel.Sel.Name {
		case "fmt.Fprintf":
			if len(args) < 2 {
				return label, nil, nil, true
			}
			return label, args[1], args[2:], true
		case "fmt.Fprint", "fmt.Fprintln", "io.WriteString":
			if len(args) < 1 {
				return label, nil, nil, true
			}
			return label, nil, args[1:], true
		case "fmt.Printf", "log.Printf":
			if len(args) < 1 {
				return label, nil, nil, true
			}
			return label, args[0], args[1:], true
		case "fmt.Print", "fmt.Println", "log.Print", "log.Println":
			return label, nil, args, true
		}
	}
	switch sel.Sel.Name {
	case "Write", "WriteString", "WriteByte", "WriteRune", "Encode":
		return types.ExprString(sel), nil, args, true
	}
	return "", nil, nil, false
}

// isScrubEvidence reports whether call is scrub.ScrubEvidence(...), the leaf
// package's function, or removal.ScrubEvidence(...), the forwarder to it.
func isScrubEvidence(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && (pkg.Name == "scrub" || pkg.Name == "removal") && sel.Sel.Name == "ScrubEvidence"
}

// callsScrubEvidence reports whether n holds a call to scrub.ScrubEvidence or
// removal.ScrubEvidence.
func callsScrubEvidence(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && isScrubEvidence(call) {
			found = true
		}
		return !found
	})
	return found
}

// isLiteral reports whether e is a basic literal, or literals joined with +.
func isLiteral(e ast.Expr) bool {
	switch e := ast.Unparen(e).(type) {
	case *ast.BasicLit:
		return true
	case *ast.BinaryExpr:
		return e.Op == token.ADD && isLiteral(e.X) && isLiteral(e.Y)
	}
	return false
}

// funcName names a function declaration: its name, or for a method the
// receiver's type and its name.
func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	t := fd.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	return types.ExprString(t) + "." + fd.Name.Name
}

// parsePrintScrubFiles parses printScrubFiles, keyed by base name.
func parsePrintScrubFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, p := range printScrubFiles {
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Base(p)] = f
	}
	return files
}

// TestReapPathPrintsScrubUnitText is the check that keeps every print on the
// reap and pass path going through scrub.ScrubEvidence, from the leaf
// package internal/scrub: a new print that hands on a unit-sourced string
// unscrubbed fails it, and so does an allow-list entry nothing matches any
// more. The check also accepts removal.ScrubEvidence, the forwarder, but code
// under internal/worktree must use the leaf scrub package and never the
// removal forwarder: internal/worktree must not import internal/removal.
func TestReapPathPrintsScrubUnitText(t *testing.T) {
	res := checkPrintScrub(parsePrintScrubFiles(t), printScrubAllow, printScrubWrappers)
	for _, w := range res.badWrappers {
		t.Errorf("wrapper %s is missing or does not call scrub.ScrubEvidence", w)
	}
	for _, v := range res.violations {
		t.Errorf("%s: scrub it with scrub.ScrubEvidence from internal/scrub, or allow-list it if it is niwa-made; "+
			"under internal/worktree use the scrub package, never the removal forwarder, since internal/worktree "+
			"must not import internal/removal", v)
	}
	for _, a := range res.unused {
		t.Errorf("allow-list entry %s %s (%s) matches no print; remove it", a.file, a.fn, a.what)
	}
}

// TestPrintScrubCheckCatchesAnUnscrubbedPrint feeds the same check source it
// must refuse: an unscrubbed print added to a real file on the path, an
// argument passed through a wrapper that doesn't scrub, and a format string
// that isn't a literal.
func TestPrintScrubCheckCatchesAnUnscrubbedPrint(t *testing.T) {
	parse := func(t *testing.T, name, src string) *ast.File {
		t.Helper()
		f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	t.Run("a new unscrubbed print in pass.go", func(t *testing.T) {
		src, err := os.ReadFile("pass.go")
		if err != nil {
			t.Fatal(err)
		}
		added := string(src) + `
func reportSomethingNew(w io.Writer, t passUnit) {
	fmt.Fprintf(w, "niwa: kept %s: %s\n", removal.ScrubEvidence(reapTargetName(t.reapTarget), 80), t.InstancePath)
}
`
		files := parsePrintScrubFiles(t)
		files["pass.go"] = parse(t, "pass.go", added)
		res := checkPrintScrub(files, printScrubAllow, printScrubWrappers)
		want := []printViolation{{"pass.go", "reportSomethingNew", "fmt.Fprintf", "t.InstancePath"}}
		if !slices.Equal(res.violations, want) || len(res.badWrappers) != 0 || len(res.unused) != 0 {
			t.Fatalf("check found %+v; want exactly %+v", res, want)
		}
	})

	t.Run("snippets", func(t *testing.T) {
		const src = `package cli

import (
	"fmt"
	"io"
	"log"

	"github.com/tsukumogami/niwa/internal/removal"
)

func scrubbedLine(w io.Writer, name string) {
	fmt.Fprintf(w, "niwa: kept %s\n", removal.ScrubEvidence(name, 80))
}

func unscrubbedLine(w io.Writer, name string) {
	fmt.Fprintf(w, "niwa: kept %s\n", name)
}

func scrubLookalike(s string, max int) string { return s }

func throughLookalike(w io.Writer, name string) {
	fmt.Fprintln(w, scrubLookalike(name, 80))
}

func formatFromAUnit(name string) {
	log.Printf(name)
}

func rawWrite(w io.Writer, name string) {
	w.Write([]byte(name))
}

func allowedCount(w io.Writer, n int) {
	fmt.Fprintf(w, "niwa: reaped %d\n", n)
}
`
		files := map[string]*ast.File{"snippet.go": parse(t, "snippet.go", src)}
		allow := []printAllow{
			{file: "snippet.go", fn: "allowedCount", what: "a count", args: []string{"n"}, why: "a count"},
			{file: "snippet.go", fn: "scrubbedLine", what: "stale", args: []string{"gone"}, why: "matches nothing"},
		}
		res := checkPrintScrub(files, allow, []string{"scrubLookalike"})
		want := []printViolation{
			{"snippet.go", "unscrubbedLine", "fmt.Fprintf", "name"},
			{"snippet.go", "throughLookalike", "fmt.Fprintln", "scrubLookalike(name, 80)"},
			{"snippet.go", "formatFromAUnit", "log.Printf", "a format that is not a literal"},
			{"snippet.go", "rawWrite", "w.Write", "[]byte(name)"},
		}
		if !slices.Equal(res.violations, want) {
			t.Errorf("violations %v; want %v", res.violations, want)
		}
		if !slices.Equal(res.badWrappers, []string{"scrubLookalike"}) {
			t.Errorf("bad wrappers %v; want scrubLookalike, which doesn't call removal.ScrubEvidence", res.badWrappers)
		}
		if len(res.unused) != 1 || res.unused[0].what != "stale" {
			t.Errorf("unused entries %+v; want the stale one", res.unused)
		}
	})
}

// TestScrubLineWriterScrubsEachLine: what the on-reap command and a teardown
// hand on is printed a line at a time, each scrubbed and cut, with a last
// line that has no newline printed on Flush.
func TestScrubLineWriterScrubsEachLine(t *testing.T) {
	var out strings.Builder
	w := newScrubLineWriter(&out, 40)
	for _, chunk := range []string{"first ghp_", "abcdefghijklmnopqrstuvwxyz0123\n", "\x1b[31mred\x1b[0m\nlast token=hunter2"} {
		if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if got, want := out.String(), "first [redacted]\nred\n"; got != want {
		t.Fatalf("before Flush: %q; want %q", got, want)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "first [redacted]\nred\nlast token=[redacted]\n"; got != want {
		t.Fatalf("after Flush: %q; want %q", got, want)
	}

	out.Reset()
	long := newScrubLineWriter(&out, 40)
	long.Write([]byte(strings.Repeat("x", 3*maxScrubLineBytes) + "\n"))
	if got := out.String(); got != strings.Repeat("x", 37)+"...\n" {
		t.Errorf("a long line printed %q; want it cut to 40 characters", got)
	}
}

// TestScrubLineWriterScrubsBeforeItCutsALongLine: a token placed across the
// writer's line limit is scrubbed whole, so no front part of it is printed,
// whether the text before it collapses to nothing (spaces, escapes) and
// however the line is split across writes. A token across the far end of
// what the writer holds is dropped whole.
func TestScrubLineWriterScrubsBeforeItCutsALongLine(t *testing.T) {
	body := strings.Repeat("A1b2", 9)
	token := "ghp_" + body
	leaks := func(got string) bool {
		return strings.Contains(got, "ghp") || strings.Contains(got, body[:4])
	}
	escapes := strings.Repeat("\x1b[0m", maxScrubLineBytes/4)
	for _, tc := range []struct {
		name, before string
		chunk        int
	}{
		{"spaces, one write", strings.Repeat(" ", maxScrubLineBytes-6), 0},
		{"spaces, small writes", strings.Repeat(" ", maxScrubLineBytes-6), 1000},
		{"spaces, a byte at a time", strings.Repeat(" ", maxScrubLineBytes-2), 1},
		{"escapes, one write", escapes[:maxScrubLineBytes-4], 0},
		{"escapes, small writes", escapes[:maxScrubLineBytes-4], 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := tc.before + token + " tail\n"
			if start := len(tc.before); start >= maxScrubLineBytes || start+len(token) <= maxScrubLineBytes {
				t.Fatalf("token at %d..%d does not cross the %d-byte limit", start, start+len(token), maxScrubLineBytes)
			}
			var out strings.Builder
			w := newScrubLineWriter(&out, 400)
			writeInChunks(t, w, line, tc.chunk)
			got := out.String()
			if want := "[redacted] tail\n"; leaks(got) || got != want {
				t.Fatalf("printed %q; want %q", got, want)
			}
		})
	}

	t.Run("across the end of what is held", func(t *testing.T) {
		held := maxScrubLineBytes + scrubLineSlack
		for _, before := range []int{held - 6, held - 2} {
			line := strings.Repeat(" ", before) + token + " tail\n"
			var out strings.Builder
			w := newScrubLineWriter(&out, 400)
			writeInChunks(t, w, line, 0)
			if got := out.String(); leaks(got) || got != "\n" {
				t.Errorf("token at %d: printed %q; want an empty line", before, got)
			}
		}
		// A word that is not a token is cut back the same way, and the line
		// keeps what came before it.
		var out strings.Builder
		w := newScrubLineWriter(&out, 400)
		writeInChunks(t, w, "kept"+strings.Repeat(" ", held-10)+"dropped-word\n", 1000)
		if got := out.String(); got != "kept\n" {
			t.Errorf("printed %q; want %q", got, "kept\n")
		}
	})
}

// TestScrubLineWriterDropsAUserinfoCutAtTheEndOfWhatIsHeld: a URL whose
// userinfo the end of what the writer holds cuts, in the user name or in a
// password holding characters no token has ($ and .), leaves nothing of the
// userinfo behind. The line is cut back to the last space before it, and with
// no space before it the whole held text is dropped.
func TestScrubLineWriterDropsAUserinfoCutAtTheEndOfWhatIsHeld(t *testing.T) {
	const pass = "Pa$$.w0rd9xyz"
	url := "https://bob:" + pass + "@host/r"
	at := strings.IndexByte(url, '@')
	held := maxScrubLineBytes + scrubLineSlack
	leak := func(got string) string {
		for _, s := range []string{"bob", "bo", "://", "https"} {
			if strings.Contains(got, s) {
				return s
			}
		}
		for i := 2; i <= len(pass); i++ {
			if strings.Contains(got, pass[:i]) {
				return pass[:i]
			}
		}
		return ""
	}
	for _, pad := range []struct {
		name string
		// before is what comes before the URL when k bytes of it are held.
		before func(k int) string
		want   string
	}{
		{"after a space", func(k int) string { return "kept" + strings.Repeat(" ", held-4-k) }, "kept\n"},
		{"with no space before it", func(k int) string {
			n := held - k
			return strings.Repeat("\x1b[0m", n/4) + strings.Repeat(" ", n%4)
		}, "\n"},
	} {
		for _, chunk := range []int{0, 1000, 1} {
			for k := len("https://b"); k <= at; k++ {
				before := pad.before(k)
				if len(before)+k != held {
					t.Fatalf("%s: %d bytes of the URL held, want %d", pad.name, held-len(before), k)
				}
				var out strings.Builder
				w := newScrubLineWriter(&out, 400)
				writeInChunks(t, w, before+url+" tail\n", chunk)
				if got := out.String(); got != pad.want || leak(got) != "" {
					t.Errorf("%s, writes of %d, cut after %q: printed %q; want %q", pad.name, chunk, url[:k], got, pad.want)
				}
			}
		}
	}
}

// writeInChunks writes s to w in pieces of n bytes, or whole when n is 0.
func writeInChunks(t *testing.T, w *scrubLineWriter, s string, n int) {
	t.Helper()
	if n <= 0 {
		n = len(s)
	}
	for len(s) > 0 {
		k := min(n, len(s))
		if got, err := w.Write([]byte(s[:k])); err != nil || got != k {
			t.Fatalf("Write of %d bytes = %d, %v", k, got, err)
		}
		s = s[k:]
	}
}
