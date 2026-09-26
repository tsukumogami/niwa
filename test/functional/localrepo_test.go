package functional

import (
	"fmt"
	"os"
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
	out, err := fixtureGit(s.root, "init", "--bare", repoPath)
	if err != nil {
		return "", fmt.Errorf("git init --bare %q: %w\n%s", repoPath, err, out)
	}
	// Pin default branch to "main" so clones get "main" regardless of the
	// system git init.defaultBranch setting (which defaults to "master" on
	// older git versions used by some CI runners).
	if out, err = fixtureGitBare(repoPath, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
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
	return s.createRepoWithSpec(name, files, nil)
}

// createRepoWithSpec is createRepoWithFiles plus committed symlinks (relative
// path → link target). Git reproduces a committed symlink verbatim in every
// clone, so this is how a fixture repository ships a context file that is a
// symlink rather than a regular file — the hostile shape the composer's
// O_NOFOLLOW read exists to refuse.
func (s *localGitServer) createRepoWithSpec(name string, files, symlinks map[string]string) (string, error) {
	repoPath := filepath.Join(s.root, name+".git")
	out, err := fixtureGit(s.root, "init", "--bare", repoPath)
	if err != nil {
		return "", fmt.Errorf("git init --bare %q: %w\n%s", repoPath, err, out)
	}
	if out, err = fixtureGitBare(repoPath, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		return "", fmt.Errorf("setting default branch for %q: %w\n%s", repoPath, err, out)
	}
	fileURL := "file://" + repoPath

	// Clone into a temp working directory inside the server root.
	workDir, err := os.MkdirTemp(s.root, "clone-*")
	if err != nil {
		return "", fmt.Errorf("creating work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	if out, err = fixtureGit(s.root, "clone", fileURL, workDir); err != nil {
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

	for linkName, target := range symlinks {
		linkPath := filepath.Join(workDir, linkName)
		if err = os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
			return "", fmt.Errorf("creating parent dir for symlink %s: %w", linkName, err)
		}
		if err = os.Symlink(target, linkPath); err != nil {
			return "", fmt.Errorf("creating symlink %s -> %s: %w", linkName, target, err)
		}
	}

	// This is the sequence that once escaped: workDir had been deleted out
	// from under a concurrent run, so git walked up and found the real
	// checkout. fixtureGitCommit pins GIT_DIR/GIT_WORK_TREE, so a missing
	// workDir now fails here instead of committing somewhere else.
	if out, err = fixtureGitCommit(workDir, "add", "-A"); err != nil {
		return "", fmt.Errorf("git add: %w\n%s", err, out)
	}

	if out, err = fixtureGitCommit(workDir, "commit", "-m", "initial"); err != nil {
		return "", fmt.Errorf("git commit: %w\n%s", err, out)
	}

	if out, err = fixtureGitCommit(workDir, "push", "-u", "origin", "HEAD"); err != nil {
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

// SourceRepoSpec creates a bare repo named <name>.git carrying committed files
// and committed symlinks, and returns its file:// URL. Use it for fixtures that
// must ship real content a clone reproduces — a repository's own AGENTS.md, a
// context file in an intermediate directory, a marketplace tree, or one of the
// hostile shapes at a name niwa writes.
func (s *localGitServer) SourceRepoSpec(name string, files, symlinks map[string]string) (string, error) {
	return s.createRepoWithSpec(name, files, symlinks)
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
