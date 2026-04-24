package functional

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// theConfigRepoIsForcePushedTo replaces the config repo's main branch
// with completely new history (different commit, same branch name).
// Simulates the upstream maintainer running `git push --force` after
// rewriting history — which is exactly the scenario in PRD #72 that
// today's `git pull --ff-only` workflow can't recover from. The new
// content overwrites the old workspace.toml.
//
// Implementation: the bare repo at <gitServer>/<name>.git already
// exists. We create a fresh working clone, build a brand-new history
// (orphan branch, single commit), and push --force to the bare repo.
func theConfigRepoIsForcePushedTo(ctx context.Context, name string, body string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, ok := s.repoURLs[name]
	if !ok {
		return ctx, fmt.Errorf("no URL stored for config repo %q", name)
	}
	// url is "file:///path/to/<name>.git"
	bareDir := strings.TrimPrefix(url, "file://")

	// Substitute {repo:<name>} placeholders.
	for repoName, repoURL := range s.repoURLs {
		body = strings.ReplaceAll(body, "{repo:"+repoName+"}", repoURL)
	}

	work, err := os.MkdirTemp(s.tmpDir, "force-push-*")
	if err != nil {
		return ctx, fmt.Errorf("creating force-push work dir: %w", err)
	}
	defer os.RemoveAll(work)

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=niwa-test",
		"GIT_AUTHOR_EMAIL=niwa-test@example.com",
		"GIT_COMMITTER_NAME=niwa-test",
		"GIT_COMMITTER_EMAIL=niwa-test@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)

	// Initialize a fresh repo with no shared history.
	for _, args := range [][]string{
		{"init", "--initial-branch=main", work},
		{"-C", work, "config", "user.email", "test@test.com"},
		{"-C", work, "config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			return ctx, fmt.Errorf("git %v: %w\n%s", args, err, out)
		}
	}

	if err := os.WriteFile(filepath.Join(work, "workspace.toml"), []byte(body), 0o644); err != nil {
		return ctx, fmt.Errorf("writing rewritten workspace.toml: %w", err)
	}

	for _, args := range [][]string{
		{"-C", work, "add", "workspace.toml"},
		{"-C", work, "commit", "-m", "force-pushed history"},
		{"-C", work, "push", "--force", "file://" + bareDir, "main"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			return ctx, fmt.Errorf("git %v: %w\n%s", args, err, out)
		}
	}
	return ctx, nil
}

// theProvenanceMarkerExistsInWorkspaceRoot asserts that
// .niwa-snapshot.toml exists at <workspaceRoot>/.niwa/. The
// `init from config repo` scenarios put the snapshot at workspaceRoot
// itself (the workspace root IS the niwa-managed dir), not at a named
// subdirectory.
func theProvenanceMarkerExistsInWorkspaceRoot(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", ".niwa-snapshot.toml")
	if _, err := os.Stat(path); err != nil {
		return ctx, fmt.Errorf("expected provenance marker at %s: %w", path, err)
	}
	return ctx, nil
}

// theConfigDirIsAGitWorkingTree converts the snapshot at
// <workspaceRoot>/.niwa/ back to a legacy git working tree by:
//  1. removing the provenance marker
//  2. cloning the named config repo into a temp dir
//  3. moving the .git/ dir into the existing .niwa/
//
// Used by the same-URL lazy conversion scenario to set up a workspace
// in the pre-snapshot model.
func theConfigDirIsAGitWorkingTree(ctx context.Context, configRepoName string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, ok := s.repoURLs[configRepoName]
	if !ok {
		return ctx, fmt.Errorf("no URL stored for config repo %q", configRepoName)
	}

	niwaDir := filepath.Join(s.workspaceRoot, ".niwa")
	if _, err := os.Stat(niwaDir); err != nil {
		return ctx, fmt.Errorf("expected niwa dir at %s: %w", niwaDir, err)
	}
	if err := os.Remove(filepath.Join(niwaDir, ".niwa-snapshot.toml")); err != nil && !os.IsNotExist(err) {
		return ctx, fmt.Errorf("remove marker: %w", err)
	}

	clone, err := os.MkdirTemp(s.tmpDir, "wt-*")
	if err != nil {
		return ctx, fmt.Errorf("temp clone dir: %w", err)
	}
	// We move .git out of clone, so don't defer RemoveAll until after the move.

	gitEnv := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	// Clone needs an empty target — MkdirTemp creates one but git clone
	// rejects non-empty dirs. Remove it first.
	_ = os.Remove(clone)
	cmd := exec.Command("git", "clone", url, clone)
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		return ctx, fmt.Errorf("git clone for working-tree setup: %w\n%s", err, out)
	}

	// Move clone/.git into niwaDir so niwa sees a working tree.
	src := filepath.Join(clone, ".git")
	dst := filepath.Join(niwaDir, ".git")
	if err := os.Rename(src, dst); err != nil {
		return ctx, fmt.Errorf("move .git into niwa dir: %w", err)
	}
	_ = os.RemoveAll(clone)
	return ctx, nil
}
