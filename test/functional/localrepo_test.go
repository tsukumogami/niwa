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
	// Pin default branch to "main" so clones get "main" regardless of the
	// system git init.defaultBranch setting (which defaults to "master" on
	// older git versions used by some CI runners).
	if out, err = exec.Command("git", "-C", repoPath, "symbolic-ref", "HEAD", "refs/heads/main").CombinedOutput(); err != nil {
		return "", fmt.Errorf("setting default branch for %q: %w\n%s", repoPath, err, out)
	}
	return "file://" + repoPath, nil
}

// createRepoWithFile creates a bare repo named <name>.git, commits a single
// file with the given content, and returns its file:// URL. It is the shared
// implementation behind ConfigRepo and OverlayRepo.
func (s *localGitServer) createRepoWithFile(name, filename, content string) (string, error) {
	return s.createRepoWithFiles(name, map[string]string{filename: content})
}

// createRepoWithFiles creates a bare repo named <name>.git, commits every file
// in files (relative path → content), and returns its file:// URL.
func (s *localGitServer) createRepoWithFiles(name string, files map[string]string) (string, error) {
	repoPath := filepath.Join(s.root, name+".git")
	out, err := exec.Command("git", "init", "--bare", repoPath).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git init --bare %q: %w\n%s", repoPath, err, out)
	}
	if out, err = exec.Command("git", "-C", repoPath, "symbolic-ref", "HEAD", "refs/heads/main").CombinedOutput(); err != nil {
		return "", fmt.Errorf("setting default branch for %q: %w\n%s", repoPath, err, out)
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

	for filename, content := range files {
		targetPath := filepath.Join(workDir, filename)
		if err = os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return "", fmt.Errorf("creating parent dir for %s: %w", filename, err)
		}
		if err = os.WriteFile(targetPath, []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("writing %s: %w", filename, err)
		}
	}

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=niwa-test",
		"GIT_AUTHOR_EMAIL=niwa-test@example.com",
		"GIT_COMMITTER_NAME=niwa-test",
		"GIT_COMMITTER_EMAIL=niwa-test@example.com",
	)

	addCmd := exec.Command("git", "add", "-A")
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

// SourceRepo creates a bare repo named <name>.git with an initial commit
// (a .gitkeep placeholder) and returns its file:// URL. Unlike Repo, it
// produces a non-empty HEAD so git worktree add -b works without --orphan.
func (s *localGitServer) SourceRepo(name string) (string, error) {
	return s.createRepoWithFile(name, ".gitkeep", "")
}

// ConfigRepo creates a bare repo named <name>.git, commits
// .niwa/workspace.toml with the given TOML body (the rank-1 layout
// per PRD R3), and returns its file:// URL. This is the canonical
// fixture for tests that don't specifically exercise rank-2
// deprecation behavior.
func (s *localGitServer) ConfigRepo(name, toml string) (string, error) {
	return s.createRepoWithFile(name, ".niwa/workspace.toml", toml)
}

// ConfigRepoRank2 creates a bare repo with workspace.toml at the
// source repo root (the rank-2 layout), exercising the deprecation
// notice path. Use ConfigRepo for any test that doesn't specifically
// target rank-2 behavior.
func (s *localGitServer) ConfigRepoRank2(name, toml string) (string, error) {
	return s.createRepoWithFile(name, "workspace.toml", toml)
}

// ConfigRepoFiles creates a bare repo named <name>.git committing every
// file in files (relative path → content), then returns its file:// URL.
// Use this when a config repo must ship more than workspace.toml — for
// example, the rank-1 .niwa/workspace.toml plus a content markdown file
// referenced via [claude.content.repos.*].source.
func (s *localGitServer) ConfigRepoFiles(name string, files map[string]string) (string, error) {
	return s.createRepoWithFiles(name, files)
}

// OverlayRepo creates a bare repo named <name>.git, commits
// .niwa/workspace-overlay.toml with the given TOML body (the
// rank-1 overlay layout), and returns its file:// URL.
func (s *localGitServer) OverlayRepo(name, toml string) (string, error) {
	return s.createRepoWithFile(name, ".niwa/workspace-overlay.toml", toml)
}

// OverlayRepoRank2 creates a bare overlay repo with workspace-overlay.toml
// at the root (rank-2). Use OverlayRepo for non-deprecation tests.
func (s *localGitServer) OverlayRepoRank2(name, toml string) (string, error) {
	return s.createRepoWithFile(name, "workspace-overlay.toml", toml)
}
