package functional

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Step definitions for the niwa-init-creates-workspace-dir feature.
//
// These steps cover the new `niwa init <name> --from <fixture>` flow:
// the directory creation, the registry-collision rejection without
// `--rebind`, the rebind happy path, and the end-to-end name-override
// propagation through `niwa go` and `niwa status`.

// iRunNiwaInitNamedFromConfigRepo runs `niwa init <name> --from <url>`
// from the workspace sandbox, where <url> is the file:// URL of a
// localGitServer config repo previously declared via
// `a config repo "<repo>" exists with body:`.
func iRunNiwaInitNamedFromConfigRepo(ctx context.Context, name, repo string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, ok := s.repoURLs[repo]
	if !ok {
		return ctx, fmt.Errorf("no URL stored for config repo %q", repo)
	}
	return ctx, runNiwa(s, s.workspaceRoot, fmt.Sprintf("niwa init %s --from %s", name, url))
}

// iSourceWrapperAndRunNiwaInitNamed runs the same `niwa init <name>
// --from <url>` flow as iRunNiwaInitNamedFromConfigRepo, but inside a
// wrapped bash subshell so the wrapper's NIWA_RESPONSE_FILE protocol
// is exercised end-to-end. This is the only way to verify that
// `builtin cd` fires in the caller's shell after `niwa init <name>`
// returns (PRD R10a / AC-21a). Unit tests can assert that
// writeLandingPath was invoked; only this scenario can assert the
// shell actually changed directory.
func iSourceWrapperAndRunNiwaInitNamed(ctx context.Context, name, repo string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, ok := s.repoURLs[repo]
	if !ok {
		return ctx, fmt.Errorf("no URL stored for config repo %q", repo)
	}
	command := fmt.Sprintf("niwa init %s --from %s", name, url)
	return ctx, runWrappedShell(s, s.workspaceRoot, command)
}

// iRunNiwaInitNamedFromConfigRepoWithRebind runs `niwa init <name>
// --from <url> --rebind` from the workspace sandbox.
func iRunNiwaInitNamedFromConfigRepoWithRebind(ctx context.Context, name, repo string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, ok := s.repoURLs[repo]
	if !ok {
		return ctx, fmt.Errorf("no URL stored for config repo %q", repo)
	}
	return ctx, runNiwa(s, s.workspaceRoot, fmt.Sprintf("niwa init %s --from %s --rebind", name, url))
}

// iPreCreateDirectory pre-creates `<workspaceRoot>/<name>` as a regular
// directory before init runs, used to exercise the target-exists
// rejection (PRD AC-10).
func iPreCreateDirectory(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if err := os.MkdirAll(filepath.Join(s.workspaceRoot, name), 0o755); err != nil {
		return ctx, fmt.Errorf("pre-creating %s: %w", name, err)
	}
	return ctx, nil
}

// iRegisterWorkspaceAt writes a registry entry for `<name>` pointing at
// `<root>` directly into the global config TOML, simulating the state
// after a prior `niwa init <name>` ran in a different directory.
func iRegisterWorkspaceAt(ctx context.Context, name, root string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	xdgHome := filepath.Join(s.homeDir, ".config")
	cfgPath := filepath.Join(xdgHome, "niwa", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return ctx, err
	}
	// Resolve the supplied root: relative paths anchor under workspaceRoot.
	if !filepath.IsAbs(root) {
		root = filepath.Join(s.workspaceRoot, root)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return ctx, fmt.Errorf("creating registered root %s: %w", root, err)
	}
	body := fmt.Sprintf("[registry.%s]\nroot = %q\nsource = %q\n", name, root, filepath.Join(root, ".niwa", "workspace.toml"))
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		return ctx, fmt.Errorf("writing registry: %w", err)
	}
	return ctx, nil
}

// theWorkspaceRootHasWorkspaceTOML asserts that
// `<workspaceRoot>/<name>/.niwa/workspace.toml` exists. This is the
// post-condition of `niwa init <name>` after the rewire (PRD AC-1).
func theWorkspaceRootHasWorkspaceTOML(ctx context.Context, name string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, name, ".niwa", "workspace.toml")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("workspace.toml missing at %s: %w", path, err)
	}
	return nil
}

// theFileExistsUnderWorkspaceRoot asserts that
// `<workspaceRoot>/<name>/<relPath>` exists. Used to verify root-altitude
// materialization (e.g. the `.claude/skills/dispatch/SKILL.md` project skill
// MaterializeWorkspaceRoot installs at the workspace root during `niwa init`).
func theFileExistsUnderWorkspaceRoot(ctx context.Context, relPath, name string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, name, filepath.FromSlash(relPath))
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("expected file %q under workspace root %q: %w", relPath, name, err)
	}
	return nil
}

// theFileUnderWorkspaceRootContains asserts that
// `<workspaceRoot>/<name>/<relPath>` exists and its content contains `want`.
// Pairs with theFileExistsUnderWorkspaceRoot to confirm the materialized file
// is the real artifact (e.g. the dispatch SKILL.md frontmatter and heading)
// rather than an empty placeholder.
func theFileUnderWorkspaceRootContains(ctx context.Context, relPath, name, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, name, filepath.FromSlash(relPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %q under workspace root %q: %w", relPath, name, err)
	}
	if !strings.Contains(string(data), want) {
		return fmt.Errorf("file %q under workspace root %q does not contain %q; got:\n%s", relPath, name, want, string(data))
	}
	return nil
}

// theRegistryHasWorkspaceRootedAt asserts that the global registry has
// an entry for `<name>` whose `root` matches `<wantRoot>` (relative
// paths anchor under workspaceRoot). Reads the registry TOML directly
// to avoid mutating process-global env (XDG_CONFIG_HOME) in a way that
// would break parallel test execution.
func theRegistryHasWorkspaceRootedAt(ctx context.Context, name, wantRoot string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if !filepath.IsAbs(wantRoot) {
		wantRoot = filepath.Join(s.workspaceRoot, wantRoot)
	}
	cfgPath := filepath.Join(s.homeDir, ".config", "niwa", "config.toml")
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("reading registry %s: %w", cfgPath, err)
	}
	var doc struct {
		Registry map[string]struct {
			Root string `toml:"root"`
		} `toml:"registry"`
	}
	if _, err := toml.Decode(string(body), &doc); err != nil {
		return fmt.Errorf("decoding registry: %w", err)
	}
	entry, ok := doc.Registry[name]
	if !ok {
		return fmt.Errorf("registry has no entry for %q (config.toml: %s)", name, cfgPath)
	}
	if entry.Root != wantRoot {
		return fmt.Errorf("registry %q root: got %q, want %q", name, entry.Root, wantRoot)
	}
	return nil
}


// iRunFromInstance runs `niwa <command>` with cwd set to the instance
// directory `<workspaceRoot>/<workspace>/<instance>`, where the
// `<workspace>` directory is the user-given name from `niwa init <name>`
// (which the rewire creates as `<workspaceRoot>/<name>/`). Used for
// `niwa status` assertions after `niwa create` has materialized an
// instance under the workspace root.
func iRunFromInstance(ctx context.Context, command, instance, workspace string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	cwd := filepath.Join(s.workspaceRoot, workspace, instance)
	return ctx, runNiwa(s, cwd, command)
}


// niwaGoFromOutsideLandsIn runs `niwa go <name>` from a directory other
// than the workspace, with a temp NIWA_RESPONSE_FILE set so the
// resolved landing path is captured to disk. Asserts the file's
// content equals `<workspaceRoot>/<expectedSubpath>` plus a trailing
// newline (the format writeLandingPath produces).
//
// The scenario must have already provisioned a registry entry for
// <name> (typically by running `niwa init <name> --from ...`).
func niwaGoFromOutsideLandsIn(ctx context.Context, name, expectedSubpath string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	from := filepath.Join(s.homeDir, "elsewhere")
	if err := os.MkdirAll(from, 0o755); err != nil {
		return err
	}

	// Capture niwa go's resolved path via NIWA_RESPONSE_FILE per the
	// shell-wrapper contract (writeLandingPath).
	respFile, err := os.CreateTemp(s.tmpDir, "niwa-go-resp-*")
	if err != nil {
		return fmt.Errorf("creating response file: %w", err)
	}
	respFile.Close()
	prevResp, hadPrev := s.envOverrides["NIWA_RESPONSE_FILE"]
	s.envOverrides["NIWA_RESPONSE_FILE"] = respFile.Name()
	defer func() {
		if hadPrev {
			s.envOverrides["NIWA_RESPONSE_FILE"] = prevResp
		} else {
			delete(s.envOverrides, "NIWA_RESPONSE_FILE")
		}
	}()

	if err := runNiwa(s, from, fmt.Sprintf("niwa go %s", name)); err != nil {
		return fmt.Errorf("niwa go failed: %w", err)
	}
	if s.exitCode != 0 {
		return fmt.Errorf("niwa go exit %d, stderr: %s", s.exitCode, s.stderr)
	}

	body, err := os.ReadFile(respFile.Name())
	if err != nil {
		return fmt.Errorf("reading response file: %w", err)
	}
	want := filepath.Join(s.workspaceRoot, expectedSubpath) + "\n"
	if string(body) != want {
		return fmt.Errorf("niwa go landing path: got %q, want %q", string(body), want)
	}
	return nil
}

