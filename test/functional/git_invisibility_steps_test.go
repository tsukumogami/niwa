package functional

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// registerGitInvisibilitySteps wires the steps that assert niwa stays invisible
// to the git status of the repositories it manages.
func registerGitInvisibilitySteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the git status of repo "([^"]*)" in instance "([^"]*)" is clean$`, theGitStatusOfRepoIsClean)
	ctx.Step(`^the git status of repo "([^"]*)" in instance "([^"]*)" is not clean$`, theGitStatusOfRepoIsNotClean)
	ctx.Step(`^the git exclude file of repo "([^"]*)" in instance "([^"]*)" contains "([^"]*)"$`, theGitExcludeContains)
	ctx.Step(`^the git exclude file of repo "([^"]*)" in instance "([^"]*)" contains "([^"]*)" exactly once$`, theGitExcludeContainsExactlyOnce)
	ctx.Step(`^I add line "([^"]*)" to the git exclude file of repo "([^"]*)" in instance "([^"]*)"$`, iAddLineToGitExclude)
	ctx.Step(`^I create file "([^"]*)" in the working tree of repo "([^"]*)" in instance "([^"]*)"$`, iCreateFileInRepoWorkingTree)
}

// iCreateFileInRepoWorkingTree writes a file (creating any parent directories)
// into a managed repo's working tree, simulating niwa-authored output landing
// there. Unlike the shared repo-write step, it creates intermediate dirs so a
// nested path such as ".niwa/state.json" works.
func iCreateFileInRepoWorkingTree(ctx context.Context, relPath, repoName, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	repoPath, err := findRepoPathInInstance(instRoot, repoName)
	if err != nil {
		return ctx, err
	}
	dst := filepath.Join(repoPath, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return ctx, fmt.Errorf("creating parent dir for %s: %w", dst, err)
	}
	if err := os.WriteFile(dst, []byte("x"), 0o644); err != nil {
		return ctx, fmt.Errorf("writing %s: %w", dst, err)
	}
	return ctx, nil
}

// repoExcludePath resolves the .git/info/exclude path for a managed repo by
// asking git for the repo's common git directory (so the resolution is correct
// for both a primary clone and any worktree of it).
func repoExcludePath(s *testState, instance, repoName string) (string, error) {
	instRoot := filepath.Join(s.workspaceRoot, instance)
	repoPath, err := findRepoPathInInstance(instRoot, repoName)
	if err != nil {
		return "", err
	}
	out, err := runGitInDir(repoPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("resolving git common dir for %s: %w\n%s", repoPath, err, out)
	}
	common := strings.TrimSpace(out)
	if !filepath.IsAbs(common) {
		common = filepath.Join(repoPath, common)
	}
	return filepath.Join(common, "info", "exclude"), nil
}

func theGitStatusOfRepoIsClean(ctx context.Context, repoName, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	repoPath, err := findRepoPathInInstance(instRoot, repoName)
	if err != nil {
		return err
	}
	out, err := runGitInDir(repoPath, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status --porcelain in %s: %w\n%s", repoPath, err, out)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("expected clean git status in repo %q, got:\n%s", repoName, out)
	}
	return nil
}

func theGitStatusOfRepoIsNotClean(ctx context.Context, repoName, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	repoPath, err := findRepoPathInInstance(instRoot, repoName)
	if err != nil {
		return err
	}
	out, err := runGitInDir(repoPath, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status --porcelain in %s: %w\n%s", repoPath, err, out)
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("expected a dirty git status in repo %q (the assertion should catch an uncovered file), but it was clean", repoName)
	}
	return nil
}

func theGitExcludeContains(ctx context.Context, repoName, instance, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path, err := repoExcludePath(s, instance, repoName)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if !strings.Contains(string(data), want) {
		return fmt.Errorf("expected %q in %s, got:\n%s", want, path, data)
	}
	return nil
}

func theGitExcludeContainsExactlyOnce(ctx context.Context, repoName, instance, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path, err := repoExcludePath(s, instance, repoName)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if n := strings.Count(string(data), want); n != 1 {
		return fmt.Errorf("expected exactly one %q in %s, found %d:\n%s", want, path, n, data)
	}
	return nil
}

func iAddLineToGitExclude(ctx context.Context, line, repoName, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	path, err := repoExcludePath(s, instance, repoName)
	if err != nil {
		return ctx, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ctx, fmt.Errorf("creating exclude dir: %w", err)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return ctx, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		existing = append(existing, '\n')
	}
	existing = append(existing, []byte(line+"\n")...)
	if err := os.WriteFile(path, existing, 0o644); err != nil {
		return ctx, fmt.Errorf("writing %s: %w", path, err)
	}
	return ctx, nil
}
