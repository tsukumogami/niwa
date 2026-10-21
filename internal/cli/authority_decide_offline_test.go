package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The offline check proves `niwa authority decide` constructs no network
// client and starts no process by reading the code its handler can reach.
//
// A package-level import check can't answer the question: internal/cli and
// internal/workspace both link net/http (dispatch and the GitHub client live
// beside decide), so "the package imports net" is true and says nothing about
// this command. Instead the check type-checks every niwa package internal/cli
// depends on from source and builds a call graph from runAuthorityDecide:
//
//   - every niwa function or method a reached body names, called or passed
//     as a value, is followed;
//   - a method called through an interface is followed to every niwa method
//     of that name whose type satisfies the interface, which
//     over-approximates what can run.
//
// Every reached body is scanned for any reference to a forbidden package:
// net, net/http, crypto/tls, os/exec and niwa's GitHub client. Calls into
// the standard library and third-party modules other than those (os, fmt,
// encoding/json, the TOML decoder, cobra's flag accessors) are allowed and
// not followed; none of them reaches the network on decide's inputs.
// Over-approximating means the check can fail on a path decide never takes,
// never that it passes over one it does.
//
// TestOfflineWalk_CatchesANetworkCall shows the walk is not vacuous: given a
// small package whose handler reaches an HTTP call through a helper and a
// method, it reports it, and it reports nothing for a function that doesn't.

const niwaModule = "github.com/tsukumogami/niwa"

// offlineForbidden are the packages decide's code path may not reference.
var offlineForbidden = map[string]bool{
	"net":                           true,
	"net/http":                      true,
	"net/http/httptrace":            true,
	"net/rpc":                       true,
	"net/smtp":                      true,
	"crypto/tls":                    true,
	"os/exec":                       true,
	niwaModule + "/internal/github": true,
}

// offlinePkgDir is one package to read from source: its import path and
// directory.
type offlinePkgDir struct{ path, dir string }

// listPackages runs `go list -export -deps` in dir for patterns. It returns
// the packages of module, in dependency order, to read from source, and an
// importer lookup that finds compiled export data for everything else. The
// split is safe because no package outside module imports one inside it, so
// each type has exactly one identity.
func listPackages(t *testing.T, dir, module string, patterns ...string) ([]offlinePkgDir, func(string) (io.ReadCloser, error)) {
	t.Helper()
	args := append([]string{"list", "-export", "-deps", "-f", "{{.ImportPath}}\t{{.Dir}}\t{{.Export}}"}, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -export: %v\n%s", err, stderr.String())
	}
	var own []offlinePkgDir
	exports := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) != 3 {
			continue
		}
		path, pkgDir, export := fields[0], fields[1], fields[2]
		if path == module || strings.HasPrefix(path, module+"/") {
			own = append(own, offlinePkgDir{path, pkgDir})
			continue
		}
		if export != "" {
			exports[path] = export
		}
	}
	return own, func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(file)
	}
}

// offlineProgram is the packages read from source, type-checked, with every
// function and method declaration indexed by key.
type offlineProgram struct {
	decls   map[string]*offlineDecl
	methods map[string][]*types.Func // method name -> methods, for interface calls
}

type offlineDecl struct {
	body *ast.BlockStmt
	info *types.Info
}

// sourceFirstImporter hands out the packages already type-checked from
// source and falls back to compiled export data for the rest.
type sourceFirstImporter struct {
	base    types.Importer
	checked map[string]*types.Package
}

func (i *sourceFirstImporter) Import(path string) (*types.Package, error) {
	if p, ok := i.checked[path]; ok {
		return p, nil
	}
	return i.base.Import(path)
}

func funcKey(pkg, recv, name string) string {
	if recv != "" {
		return pkg + ".(" + recv + ")." + name
	}
	return pkg + "." + name
}

// recvTypeName is the receiver's named type, without pointer or type
// arguments.
func recvTypeName(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok {
		return n.Origin().Obj().Name()
	}
	return ""
}

func methodKey(m *types.Func) string {
	return funcKey(m.Pkg().Path(), recvTypeName(m.Type().(*types.Signature).Recv().Type()), m.Name())
}

// loadOfflineProgram parses and type-checks each package from its non-test
// sources for the running platform. pkgs is in dependency order.
func loadOfflineProgram(t *testing.T, pkgs []offlinePkgDir, lookup func(string) (io.ReadCloser, error)) *offlineProgram {
	t.Helper()
	fset := token.NewFileSet()
	imp := &sourceFirstImporter{base: importer.ForCompiler(fset, "gc", lookup), checked: map[string]*types.Package{}}
	prog := &offlineProgram{decls: map[string]*offlineDecl{}, methods: map[string][]*types.Func{}}
	for _, pd := range pkgs {
		entries, err := os.ReadDir(pd.dir)
		if err != nil {
			t.Fatalf("reading %s: %v", pd.dir, err)
		}
		var files []*ast.File
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			if ok, err := build.Default.MatchFile(pd.dir, name); err != nil || !ok {
				continue
			}
			f, err := parser.ParseFile(fset, filepath.Join(pd.dir, name), nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parsing %s: %v", name, err)
			}
			files = append(files, f)
		}
		info := &types.Info{
			Uses: map[*ast.Ident]types.Object{},
			Defs: map[*ast.Ident]types.Object{},
		}
		conf := types.Config{Importer: imp}
		checked, err := conf.Check(pd.path, fset, files, info)
		if err != nil {
			t.Fatalf("type-checking %s: %v", pd.path, err)
		}
		imp.checked[pd.path] = checked
		for _, f := range files {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				fn, ok := info.Defs[fd.Name].(*types.Func)
				if !ok {
					continue
				}
				key := funcKey(pd.path, "", fn.Name())
				if fn.Type().(*types.Signature).Recv() != nil {
					key = methodKey(fn)
					prog.methods[fn.Name()] = append(prog.methods[fn.Name()], fn)
				}
				prog.decls[key] = &offlineDecl{body: fd.Body, info: info}
			}
		}
	}
	return prog
}

// mayImplement reports whether m's receiver type, or a pointer to it, can
// satisfy iface. A generic receiver, or an interface the checker could not
// describe, counts as a yes.
func mayImplement(m *types.Func, iface *types.Interface) bool {
	if iface == nil {
		return true
	}
	t := m.Type().(*types.Signature).Recv().Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok || n.TypeParams().Len() > 0 {
		return true
	}
	return types.Implements(n, iface) || types.Implements(types.NewPointer(n), iface)
}

// walk returns, sorted, every forbidden reference reachable from entry, each
// naming the function it was found in and the chain of calls that reached
// it.
func (p *offlineProgram) walk(entry string) []string {
	if p.decls[entry] == nil {
		return []string{"entry " + entry + " not found"}
	}
	via := map[string]string{entry: ""}
	queue := []string{entry}
	var violations []string
	trail := func(k string) string {
		var chain []string
		for ; k != ""; k = via[k] {
			chain = append([]string{k}, chain...)
		}
		return strings.Join(chain, " -> ")
	}
	enqueue := func(from, to string) {
		if _, seen := via[to]; seen || p.decls[to] == nil {
			return
		}
		via[to] = from
		queue = append(queue, to)
	}

	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		d := p.decls[k]
		ast.Inspect(d.body, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			obj := d.info.Uses[id]
			if obj == nil {
				return true
			}
			pkgPath := ""
			if obj.Pkg() != nil {
				pkgPath = obj.Pkg().Path()
			}
			if offlineForbidden[pkgPath] {
				violations = append(violations, fmt.Sprintf("%s references %s.%s (via %s)", k, pkgPath, obj.Name(), trail(k)))
				return true
			}
			fn, ok := obj.(*types.Func)
			if !ok {
				return true
			}
			fn = fn.Origin()
			sig := fn.Type().(*types.Signature)
			if sig.Recv() == nil {
				enqueue(k, funcKey(pkgPath, "", fn.Name()))
				return true
			}
			if types.IsInterface(sig.Recv().Type()) {
				// Dynamic dispatch: any method of this name whose type
				// satisfies the interface may run.
				iface, _ := sig.Recv().Type().Underlying().(*types.Interface)
				for _, m := range p.methods[fn.Name()] {
					if mayImplement(m, iface) {
						enqueue(k, methodKey(m))
					}
				}
				return true
			}
			enqueue(k, methodKey(fn))
			return true
		})
	}
	sort.Strings(violations)
	return violations
}

// TestAuthorityDecide_ConstructsNoNetworkClient walks decide's handler
// through every niwa package internal/cli depends on and fails on any
// reference to a network, TLS or subprocess package.
func TestAuthorityDecide_ConstructsNoNetworkClient(t *testing.T) {
	pkgs, lookup := listPackages(t, ".", niwaModule, ".")
	prog := loadOfflineProgram(t, pkgs, lookup)
	entry := funcKey(niwaModule+"/internal/cli", "", "runAuthorityDecide")
	if v := prog.walk(entry); len(v) > 0 {
		t.Fatalf("niwa authority decide can reach code outside its offline boundary:\n%s", strings.Join(v, "\n"))
	}
}

// TestOfflineWalk_CatchesANetworkCall feeds the walk a package whose handler
// reaches http.Get through a helper and a method, and one that reaches it
// through an interface, and expects both reported. A function that reaches
// neither is not reported.
func TestOfflineWalk_CatchesANetworkCall(t *testing.T) {
	dir := t.TempDir()
	src := `package fake

import "net/http"

type fetcher struct{}

func (fetcher) Fetch() { _, _ = http.Get("http://example.invalid") }

type doer interface{ Fetch() }

type quiet struct{}

func (quiet) Run() {}

func helper() { var f fetcher; f.Fetch() }

func Handler() { helper() }

func ThroughInterface(d doer) { d.Fetch() }

func Clean() { var q quiet; q.Run() }

func Unrelated() { _, _ = http.Get("http://example.invalid") }
`
	if err := os.WriteFile(filepath.Join(dir, "fake.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, lookup := listPackages(t, ".", niwaModule, "net/http")
	const pkg = "example.com/fake"
	prog := loadOfflineProgram(t, []offlinePkgDir{{pkg, dir}}, lookup)

	for _, entry := range []string{"Handler", "ThroughInterface"} {
		v := prog.walk(funcKey(pkg, "", entry))
		if len(v) != 1 || !strings.Contains(v[0], "net/http.Get") || !strings.Contains(v[0], "(fetcher).Fetch") {
			t.Errorf("walk from %s reported %q; want one net/http.Get violation in fetcher.Fetch", entry, v)
		}
	}
	if v := prog.walk(funcKey(pkg, "", "Clean")); len(v) != 0 {
		t.Errorf("walk from Clean reported %q; want nothing", v)
	}
}
