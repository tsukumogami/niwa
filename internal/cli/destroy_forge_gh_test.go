package cli

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// destroyGhFixture is a workspace root with a bin directory inside it and
// one outside, each holding a fake gh that records how it was started (its
// path and PATH) in its own marker file and prints a dummy token.
type destroyGhFixture struct {
	root, inside, outside       string
	insideMarker, outsideMarker string
}

func newDestroyGhFixture(t *testing.T) destroyGhFixture {
	t.Helper()
	base := canonicalTempDir(t)
	f := destroyGhFixture{
		root:          filepath.Join(base, "ws"),
		outside:       filepath.Join(base, "bin"),
		insideMarker:  filepath.Join(base, "inside-gh-ran"),
		outsideMarker: filepath.Join(base, "outside-gh-ran"),
	}
	f.inside = filepath.Join(f.root, "bin")
	for _, d := range []string{f.inside, f.outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for dir, marker := range map[string]string{f.inside: f.insideMarker, f.outside: f.outsideMarker} {
		script := "#!/bin/sh\nprintf '%s\\n' \"$0\" \"$PATH\" > '" + marker + "'\nprintf 'gh-dummy-token\\n'\n"
		if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// headPRAuth makes one pull request lookup through the forge destroy builds,
// with its token from a removal environment resolved the way destroy
// resolves its scan's, against a fake GitHub that records the Authorization
// header it gets. PATH is pathEnv for the whole lookup.
func headPRAuth(t *testing.T, root, pathEnv string) string {
	t.Helper()
	var mu sync.Mutex
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		switch r.URL.Path {
		case "/repos/acme/widget":
			_, _ = io.WriteString(w, `{"fork": false}`)
		case "/repos/acme/widget/pulls":
			_, _ = io.WriteString(w, `[]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("NIWA_GITHUB_API_URL", srv.URL)
	t.Setenv("PATH", pathEnv)

	scanEnv := newRemovalEnv(root, canonicalTempDir(t), os.Getenv("PATH"))
	defer scanEnv.Close()
	ctx := context.Background()
	forge := newDestroyForge(func() string { return scanEnv.GitHubToken(ctx) })
	pr, err := forge.HeadPR(ctx, "acme", "widget", "feat")
	if err != nil {
		t.Fatalf("HeadPR: %v", err)
	}
	if pr.State != workspace.HeadPRNone {
		t.Fatalf("HeadPR = %+v, want no PR", pr)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(auth) == 0 {
		t.Fatal("the forge made no request")
	}
	for _, a := range auth[1:] {
		if a != auth[0] {
			t.Fatalf("the forge's requests carried different Authorization headers: %q", auth)
		}
	}
	return auth[0]
}

func readGhMarker(t *testing.T, marker string) []string {
	t.Helper()
	data, err := os.ReadFile(marker)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestDestroyForge_GhInsideTheWorkspaceRootNeverRuns(t *testing.T) {
	f := newDestroyGhFixture(t)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	if got := headPRAuth(t, f.root, f.inside); got != "" {
		t.Errorf("the lookup was authenticated as %q with no gh outside the workspace root", got)
	}
	if rec := readGhMarker(t, f.insideMarker); rec != nil {
		t.Errorf("the gh inside the workspace root ran: %q", rec)
	}
}

func TestDestroyForge_GhOutsideTheWorkspaceRootRunsByItsPath(t *testing.T) {
	f := newDestroyGhFixture(t)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	got := headPRAuth(t, f.root, f.inside+string(os.PathListSeparator)+f.outside)
	if got != "Bearer gh-dummy-token" {
		t.Errorf("Authorization = %q, want the token the gh outside the workspace root printed", got)
	}
	if rec := readGhMarker(t, f.insideMarker); rec != nil {
		t.Errorf("the gh inside the workspace root ran, although it comes first on PATH: %q", rec)
	}
	want := []string{filepath.Join(f.outside, "gh"), f.outside}
	if rec := readGhMarker(t, f.outsideMarker); strings.Join(rec, "\n") != strings.Join(want, "\n") {
		t.Errorf("the gh outside the workspace root recorded its path and PATH as %q, want %q", rec, want)
	}
}

func TestDestroyForge_GitHubTokenStillWins(t *testing.T) {
	f := newDestroyGhFixture(t)
	t.Setenv("GITHUB_TOKEN", "env-github-token")
	t.Setenv("GH_TOKEN", "env-gh-token")

	if got := headPRAuth(t, f.root, f.outside); got != "Bearer env-github-token" {
		t.Errorf("Authorization = %q, want GITHUB_TOKEN's value", got)
	}
	for _, m := range []string{f.insideMarker, f.outsideMarker} {
		if rec := readGhMarker(t, m); rec != nil {
			t.Errorf("a gh ran although GITHUB_TOKEN was set: %q", rec)
		}
	}
}

// Both destroy scans build their forge with the token from the scan's own
// removal environment, never from resolveGitHubToken, which runs gh from
// the raw PATH.
func TestDestroyForge_BothScansTakeTheTokenFromTheScanEnv(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "destroy.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "newDestroyForge" {
			return true
		}
		calls++
		if len(call.Args) != 1 || !callsScanEnvGitHubToken(call.Args[0]) {
			t.Errorf("newDestroyForge at %s isn't given a func returning scanEnv.GitHubToken", fset.Position(call.Pos()))
		}
		return true
	})
	if calls != 2 {
		t.Errorf("destroy.go calls newDestroyForge %d times, want 2", calls)
	}

	for _, name := range []string{"destroy.go", "destroy_forge.go"} {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == "resolveGitHubToken" {
				t.Errorf("%s uses resolveGitHubToken at %s", name, fset.Position(id.Pos()))
			}
			return true
		})
	}
}

// callsScanEnvGitHubToken reports whether e is a func literal whose body is
// return scanEnv.GitHubToken(...).
func callsScanEnvGitHubToken(e ast.Expr) bool {
	lit, ok := e.(*ast.FuncLit)
	if !ok || len(lit.Body.List) != 1 {
		return false
	}
	ret, ok := lit.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	call, ok := ret.Results[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "GitHubToken" {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == "scanEnv"
}
