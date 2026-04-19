package functional

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// localGitServer manages a directory of bare repos for one scenario.
// Each call to Repo or ConfigRepo creates a named bare repo under root
// and returns its file:// URL so test steps can reference it without
// any network access.
type localGitServer struct {
	root string // absolute path, e.g. <sandbox>/gitserver/
}

// newLocalGitServer creates an empty server rooted under dir.
func newLocalGitServer(dir string) (*localGitServer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating git server root %q: %w", dir, err)
	}
	return &localGitServer{root: dir}, nil
}

// Repo creates a bare repo named <name>.git and returns its file:// URL.
func (s *localGitServer) Repo(name string) (string, error) {
	repoPath := filepath.Join(s.root, name+".git")
	out, err := exec.Command("git", "init", "--bare", repoPath).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git init --bare %q: %w\n%s", repoPath, err, out)
	}
	return "file://" + repoPath, nil
}

// ConfigRepo creates a bare repo named <name>.git, commits workspace.toml
// with the given TOML body, and returns its file:// URL.
func (s *localGitServer) ConfigRepo(name, toml string) (string, error) {
	repoPath := filepath.Join(s.root, name+".git")
	out, err := exec.Command("git", "init", "--bare", repoPath).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git init --bare %q: %w\n%s", repoPath, err, out)
	}
	fileURL := "file://" + repoPath

	// Clone into a temp working directory inside the server root.
	workDir, err := os.MkdirTemp(s.root, "clone-*")
	if err != nil {
		return "", fmt.Errorf("creating work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	if out, err = exec.Command("git", "clone", fileURL, workDir).CombinedOutput(); err != nil {
		return "", fmt.Errorf("git clone %q: %w\n%s", fileURL, err, out)
	}

	if err = os.WriteFile(filepath.Join(workDir, "workspace.toml"), []byte(toml), 0o644); err != nil {
		return "", fmt.Errorf("writing workspace.toml: %w", err)
	}

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=niwa-test",
		"GIT_AUTHOR_EMAIL=niwa-test@example.com",
		"GIT_COMMITTER_NAME=niwa-test",
		"GIT_COMMITTER_EMAIL=niwa-test@example.com",
	)

	addCmd := exec.Command("git", "add", "workspace.toml")
	addCmd.Dir = workDir
	addCmd.Env = gitEnv
	if out, err = addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add: %w\n%s", err, out)
	}

	commitCmd := exec.Command("git", "commit", "-m", "initial")
	commitCmd.Dir = workDir
	commitCmd.Env = gitEnv
	if out, err = commitCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git commit: %w\n%s", err, out)
	}

	pushCmd := exec.Command("git", "push", "-u", "origin", "HEAD")
	pushCmd.Dir = workDir
	pushCmd.Env = gitEnv
	if out, err = pushCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git push: %w\n%s", err, out)
	}

	return fileURL, nil
}
