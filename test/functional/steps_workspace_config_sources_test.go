package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

	// Initialize a fresh repo with no shared history.
	if out, err := fixtureGit(work, "init", "--initial-branch=main", work); err != nil {
		return ctx, fmt.Errorf("git init in %s: %w\n%s", work, err, out)
	}
	for _, args := range [][]string{
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		if out, err := fixtureGitWorkTree(work, args...); err != nil {
			return ctx, fmt.Errorf("git %v: %w\n%s", args, err, out)
		}
	}

	// Force-push uses the rank-1 layout (.niwa/workspace.toml) to
	// match the canonical ConfigRepo helper. The original ConfigRepo
	// also writes to .niwa/workspace.toml since Issue 5/7 ship.
	niwaDir := filepath.Join(work, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating .niwa dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(body), 0o644); err != nil {
		return ctx, fmt.Errorf("writing rewritten .niwa/workspace.toml: %w", err)
	}

	for _, args := range [][]string{
		{"add", ".niwa/workspace.toml"},
		{"commit", "-m", "force-pushed history"},
		{"push", "--force", "file://" + bareDir, "main"},
	} {
		if out, err := fixtureGitCommit(work, args...); err != nil {
			return ctx, fmt.Errorf("git %v: %w\n%s", args, err, out)
		}
	}
	return ctx, nil
}

// aDispatchBriefExistsInWorkspaceRoot writes a task brief to
// <workspaceRoot>/.niwa/dispatch-briefs/<file>, exactly where the niwa-owned
// /dispatch skill drops it immediately before invoking `niwa dispatch`. This
// is niwa-local runtime state living inside the config dir; the next config
// snapshot refresh (drift swap) must carry it across rather than clobber it.
func aDispatchBriefExistsInWorkspaceRoot(ctx context.Context, file string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	briefsDir := filepath.Join(s.workspaceRoot, ".niwa", "dispatch-briefs")
	if err := os.MkdirAll(briefsDir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating dispatch-briefs dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(briefsDir, file), []byte("# task brief\n"), 0o644); err != nil {
		return ctx, fmt.Errorf("writing dispatch brief %q: %w", file, err)
	}
	return ctx, nil
}

// theDispatchBriefStillExistsInWorkspaceRoot asserts the brief written by
// aDispatchBriefExistsInWorkspaceRoot survived a config snapshot refresh.
// Before the fix, the atomic swap that replaces <workspaceRoot>/.niwa/ with
// freshly fetched upstream content took dispatch-briefs/ down with it.
func theDispatchBriefStillExistsInWorkspaceRoot(ctx context.Context, file string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", "dispatch-briefs", file)
	if _, err := os.Stat(path); err != nil {
		return ctx, fmt.Errorf("dispatch brief %q was clobbered by the config snapshot refresh (expected it at %s): %w", file, path, err)
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

// theMaterializedFileAtWorkspaceRootContains asserts that the niwa-materialized
// file at <workspaceRoot>/<relPath> exists and contains want. Unlike
// theFileUnderWorkspaceRootContains (which anchors under a named subdir), this
// targets the workspace root directly, as the `init from config repo` flow
// makes the root itself the niwa-managed dir. Materialized files are written
// from the loaded config, so this proves a source-side config change took
// effect on the SAME apply run (issue #214): a stale config would leave the old
// content in place and require a second apply.
func theMaterializedFileAtWorkspaceRootContains(ctx context.Context, relPath, want string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, relPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return ctx, fmt.Errorf("reading %s: %w", path, err)
	}
	if !strings.Contains(string(data), want) {
		return ctx, fmt.Errorf("expected %s to contain %q, got:\n%s", path, want, string(data))
	}
	return ctx, nil
}

// lookupJSONKey parses the JSON file at path and walks a dotted key path
// ("permissions.defaultMode"). It returns the value and whether every segment
// was present. A file that is missing or doesn't parse is an error, so a "no
// key" assertion can't pass on an unreadable document. So is a segment that
// is present but isn't an object: "permissions": "bypassPermissions" is a
// malformed document, not one without a mode. The path splits on ".", so a key
// that itself contains a dot can't be addressed.
func lookupJSONKey(path, dottedKey string) (value any, found bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false, fmt.Errorf("parsing %s as JSON: %w\n%s", path, err, data)
	}
	cur := doc
	walked := ""
	for _, seg := range strings.Split(dottedKey, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%s: %q is %v, not an object, so %q can't be looked up", path, walked, cur, dottedKey)
		}
		cur, ok = obj[seg]
		if !ok {
			return nil, false, nil
		}
		walked = strings.TrimPrefix(walked+"."+seg, ".")
	}
	return cur, true, nil
}

// lookupJSONKeyAtWorkspaceRoot is lookupJSONKey for a path relative to the
// workspace root.
func lookupJSONKeyAtWorkspaceRoot(ctx context.Context, relPath, dottedKey string) (value any, found bool, path string, err error) {
	s := getState(ctx)
	if s == nil {
		return nil, false, "", fmt.Errorf("no test state")
	}
	path = filepath.Join(s.workspaceRoot, relPath)
	value, found, err = lookupJSONKey(path, dottedKey)
	return value, found, path, err
}

// theJSONFileAtWorkspaceRootHasNoKey asserts the JSON file at relPath under
// the workspace root parses and carries no value at the dotted key path.
func theJSONFileAtWorkspaceRootHasNoKey(ctx context.Context, relPath, dottedKey string) (context.Context, error) {
	value, found, path, err := lookupJSONKeyAtWorkspaceRoot(ctx, relPath, dottedKey)
	if err != nil {
		return ctx, err
	}
	if found {
		return ctx, fmt.Errorf("expected %s to have no %q, got %v", path, dottedKey, value)
	}
	return ctx, nil
}

// theJSONFileAtWorkspaceRootHasKeyEqualTo asserts the JSON file at relPath
// under the workspace root parses and carries the string want at the dotted
// key path.
func theJSONFileAtWorkspaceRootHasKeyEqualTo(ctx context.Context, relPath, dottedKey, want string) (context.Context, error) {
	value, found, path, err := lookupJSONKeyAtWorkspaceRoot(ctx, relPath, dottedKey)
	if err != nil {
		return ctx, err
	}
	if !found {
		return ctx, fmt.Errorf("expected %s to have %q, but it is absent", path, dottedKey)
	}
	if got, ok := value.(string); !ok || got != want {
		return ctx, fmt.Errorf("expected %s %q = %q, got %v", path, dottedKey, want, value)
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

	// Clone needs an empty target — MkdirTemp creates one but git clone
	// rejects non-empty dirs. Remove it first.
	_ = os.Remove(clone)
	if out, err := fixtureGit(s.tmpDir, "clone", url, clone); err != nil {
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

// theConfigRepoIsUnreachable renames the bare repo backing the named config
// source out of the way, so the next fetch against its recorded URL fails the
// way a briefly-unreachable remote does. Used to prove `niwa reset` reconciles
// before it destroys: the destroy must not have happened when the refetch
// fails, or the user is left with nothing where their instance was.
func theConfigRepoIsUnreachable(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, ok := s.repoURLs[name]
	if !ok {
		return ctx, fmt.Errorf("no URL stored for config repo %q", name)
	}
	bareDir := strings.TrimPrefix(url, "file://")
	if err := os.Rename(bareDir, bareDir+".moved"); err != nil {
		return ctx, fmt.Errorf("making config repo %q unreachable: %w", name, err)
	}
	return ctx, nil
}
