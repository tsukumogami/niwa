package functional

// Fixture git calls all funnel through this file, and they have to.
//
// Neither `git -C <dir>` nor cmd.Dir is a boundary. Both only say where git
// should *start* looking for a repository; if that directory has vanished, or
// was never a repository, git keeps walking up until it finds one. A fixture
// that walks up out of its sandbox lands in the real checkout, where `git add
// -A` and `git commit` mean something very different from what the test
// author had in mind. That's not hypothetical -- it's how a scenario once
// committed a developer's working tree onto main and pushed it.
//
// So every git invocation under test/functional/ goes through a helper below.
// Each one checks its target against the process sandbox before running, and
// tells git where the sandbox ends. If you're writing a step that shells out
// to git, use one of these; a plain exec.Command("git", ...) reopens the hole.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// fixtureGitIdentity is who the suite's own commits are by. Fixture history
// should be attributable to the suite, not to whoever happened to run it.
var fixtureGitIdentity = []string{
	"GIT_AUTHOR_NAME=niwa-test",
	"GIT_AUTHOR_EMAIL=niwa-test@example.com",
	"GIT_COMMITTER_NAME=niwa-test",
	"GIT_COMMITTER_EMAIL=niwa-test@example.com",
}

// sandboxPath resolves dir and rejects anything outside the process sandbox.
// It uses filepath.Rel rather than a string prefix on purpose: with a plain
// prefix test, "/tmp/niwa-func-12-elsewhere" looks like it's inside
// "/tmp/niwa-func-12", and it isn't.
func sandboxPath(dir string) (string, error) {
	if processSandboxRoot == "" {
		return "", fmt.Errorf("fixture git: no process sandbox allocated; TestMain did not run")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("fixture git: resolving %q: %w", dir, err)
	}
	rel, err := filepath.Rel(processSandboxRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("fixture git: refusing to run in %q -- it is outside the test sandbox %q", abs, processSandboxRoot)
	}
	return abs, nil
}

// fixtureGitEnv is the environment every fixture git call inherits: a ceiling
// that stops upward repository discovery at the sandbox, and a global/system
// config pointed at /dev/null so a developer's ~/.gitconfig can't change what
// the suite does.
func fixtureGitEnv() []string {
	// The ceiling has to be absolute. Git silently ignores a relative entry,
	// which would leave the guard off with nothing anywhere saying so.
	ceiling := filepath.Dir(processSandboxRoot)
	return append(os.Environ(),
		"GIT_CEILING_DIRECTORIES="+ceiling,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
}

// runFixtureGit is the single exec.Command("git", ...) in the suite. pinEnv,
// when non-nil, adds the GIT_DIR/GIT_WORK_TREE (and identity) vars that the
// scoped variants need; it receives the already-resolved directory.
func runFixtureGit(dir string, pinEnv func(absDir string) []string, args ...string) ([]byte, error) {
	absDir, err := sandboxPath(dir)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = absDir
	cmd.Env = fixtureGitEnv()
	if pinEnv != nil {
		cmd.Env = append(cmd.Env, pinEnv(absDir)...)
	}
	return cmd.CombinedOutput()
}

// fixtureGit runs git with dir as its working directory and nothing pinned.
// Use it for commands that name their target outright (`git init --bare
// <path>`, `git clone <url> <dest>`) and for queries against whatever
// repository encloses dir -- the ceiling keeps that search inside the sandbox,
// so a directory that isn't a repository any more fails with git's own "not a
// git repository" instead of quietly finding the one above it.
func fixtureGit(dir string, args ...string) ([]byte, error) {
	return runFixtureGit(dir, nil, args...)
}

// fixtureGitWorkTree runs git against the working tree at dir with GIT_DIR and
// GIT_WORK_TREE pinned, so there's nothing left to discover at all.
func fixtureGitWorkTree(dir string, args ...string) ([]byte, error) {
	return runFixtureGit(dir, workTreeEnv, args...)
}

// fixtureGitCommit is fixtureGitWorkTree plus the suite's author/committer
// identity -- the add/commit/push sequence that builds fixture repos.
func fixtureGitCommit(dir string, args ...string) ([]byte, error) {
	return runFixtureGit(dir, func(absDir string) []string {
		return append(workTreeEnv(absDir), fixtureGitIdentity...)
	}, args...)
}

// fixtureGitBare runs git against the bare repo at repoPath with GIT_DIR
// pinned to it. Bare repos have no work tree, so pinning one would break the
// command outright.
func fixtureGitBare(repoPath string, args ...string) ([]byte, error) {
	return runFixtureGit(repoPath, func(absDir string) []string {
		return []string{"GIT_DIR=" + absDir}
	}, args...)
}

func workTreeEnv(absDir string) []string {
	return []string{
		"GIT_DIR=" + filepath.Join(absDir, ".git"),
		"GIT_WORK_TREE=" + absDir,
	}
}
