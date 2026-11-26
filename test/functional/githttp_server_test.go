package functional

import (
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// gitHTTPServer is an https git server for the removal rule's scenarios:
// git http-backend behind net/http/cgi on an httptest TLS server, serving
// bare repositories under one directory and counting every request it
// receives by path, so a scenario can show the rule never contacted a remote
// it doesn't trust. Its CA reaches niwa's git calls only through the test
// build's NIWA_TEST_REMOVAL_CA_FILE seam.
type gitHTTPServer struct {
	srv *httptest.Server
	// root holds the served repositories, a URL path mapping to the
	// directory of the same relative path.
	root string
	// caFile is the server's certificate, PEM-encoded, for GIT_SSL_CAINFO.
	caFile string

	mu    sync.Mutex
	paths []string
}

// startGitHTTPServer starts a server whose repositories and CA file live
// under dir.
func startGitHTTPServer(dir string) (*gitHTTPServer, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	g := &gitHTTPServer{root: filepath.Join(dir, "served")}
	if err := os.MkdirAll(g.root, 0o755); err != nil {
		return nil, err
	}
	backend := &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Dir:  g.root,
		Env: []string{
			"GIT_PROJECT_ROOT=" + g.root,
			"GIT_HTTP_EXPORT_ALL=1",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=/dev/null",
			"HOME=" + dir,
		},
		InheritEnv: []string{"PATH"},
	}
	g.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.paths = append(g.paths, r.URL.Path)
		g.mu.Unlock()
		backend.ServeHTTP(w, r)
	}))
	g.caFile = filepath.Join(dir, "git-server-ca.pem")
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: g.srv.Certificate().Raw})
	if err := os.WriteFile(g.caFile, block, 0o644); err != nil {
		g.srv.Close()
		return nil, err
	}
	return g, nil
}

// close stops the server.
func (g *gitHTTPServer) close() {
	if g != nil && g.srv != nil {
		g.srv.Close()
	}
}

// hostPort is the server's host and port, as a URL names them.
func (g *gitHTTPServer) hostPort() string {
	return strings.TrimPrefix(g.srv.URL, "https://")
}

// otherPort is the server's host with a port nothing listens on.
func (g *gitHTTPServer) otherPort() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		return "", err
	}
	return fmt.Sprintf("127.0.0.1:%d", port), nil
}

// url is the https URL of a served path.
func (g *gitHTTPServer) url(path string) string {
	return g.srv.URL + "/" + strings.TrimPrefix(path, "/")
}

// repoDir makes a bare repository at the served path and returns its
// directory.
func (g *gitHTTPServer) repoDir(path string) (string, error) {
	dir := filepath.Join(g.root, filepath.FromSlash(strings.TrimPrefix(path, "/")))
	if !strings.HasPrefix(dir, g.root+string(filepath.Separator)) {
		return "", fmt.Errorf("served path %q leaves the server's root", path)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command("git", "init", "-q", "--bare", dir)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git init --bare %s: %v\n%s", dir, err, out)
	}
	return dir, nil
}

// requests is every request path the server received.
func (g *gitHTTPServer) requests() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.paths...)
}
