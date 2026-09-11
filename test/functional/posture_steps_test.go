package functional

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// posture_steps_test.go holds the steps that set up and check a workspace's
// declared permission posture from outside the process: the host-config step
// the remote-control dispatch scenario needs, and the steps that read what each
// generated Claude Code settings document says. The argv assertions live with
// the other dispatch steps in dispatch_steps_test.go. The scenarios take their
// posture from a config repo or a personal overlay run through the real binary,
// never from a hand-written settings or state file.

// theHostConfigDeclaresGlobalSettings puts the docstring's keys under [global]
// in the sandboxed host config ($XDG_CONFIG_HOME/niwa/config.toml). It edits
// the file in place rather than replacing it, because `niwa init` writes the
// workspace registry into the same file: when a [global] header is already
// there the keys go directly under it, otherwise a [global] table is appended.
func theHostConfigDeclaresGlobalSettings(ctx context.Context, body *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.homeDir, ".config", "niwa")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating host config dir: %w", err)
	}
	path := filepath.Join(dir, "config.toml")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return ctx, fmt.Errorf("reading host config %s: %w", path, err)
	}
	keys := strings.TrimSpace(body.Content)

	lines := strings.Split(string(existing), "\n")
	inserted := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "[global]" {
			rest := append([]string{keys}, lines[i+1:]...)
			lines = append(lines[:i+1], rest...)
			inserted = true
			break
		}
	}
	var out string
	if inserted {
		out = strings.Join(lines, "\n")
	} else {
		out = strings.TrimRight(string(existing), "\n")
		if out != "" {
			out += "\n\n"
		}
		out += "[global]\n" + keys + "\n"
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		return ctx, fmt.Errorf("writing host config %s: %w", path, err)
	}
	return ctx, nil
}

// validDefaultModes are the permissions.defaultMode values a project settings
// document may carry. bypassPermissions and auto no longer take effect from a
// project or local settings file, and askPermissions isn't a mode at all: it
// makes Claude Code discard the whole file. The set follows the modes Claude
// Code honors from a project or local settings file, not niwa's posture
// vocabulary: a new posture value changes the Examples in
// permission-posture-documents.feature, and this set changes only when that
// list of honored modes does.
var validDefaultModes = map[string]bool{
	"default":     true,
	"acceptEdits": true,
	"plan":        true,
	"dontAsk":     true,
}

// assertSettingsDefaultMode checks the document's permissions.defaultMode
// against want, where "none" means the key must be absent. Whatever the
// expectation, a present value outside validDefaultModes fails. The lookup is
// lookupJSONKey, so a missing file, a file that isn't JSON, or a "permissions"
// that isn't an object fails rather than reading as no mode.
func assertSettingsDefaultMode(path, want string) error {
	mode, present, err := lookupJSONKey(path, "permissions.defaultMode")
	if err != nil {
		return err
	}
	if present {
		str, ok := mode.(string)
		if !ok || !validDefaultModes[str] {
			return fmt.Errorf("%s carries permissions.defaultMode %v, which Claude Code doesn't honor from a project settings file", path, mode)
		}
	}
	if want == "none" {
		if present {
			return fmt.Errorf("%s carries permissions.defaultMode %v, want none", path, mode)
		}
		return nil
	}
	if !present {
		return fmt.Errorf("%s carries no permissions.defaultMode, want %q", path, want)
	}
	if mode != want {
		return fmt.Errorf("%s carries permissions.defaultMode %v, want %q", path, mode, want)
	}
	return nil
}

// The four settings documents a posture reaches. The workspace root and the
// instance root carry .claude/settings.json; a repo and a worktree carry
// .claude/settings.local.json.
func workspaceRootSettingsPath(s *testState) string {
	return filepath.Join(s.workspaceRoot, ".claude", "settings.json")
}

func instanceRootSettingsPath(s *testState, instance string) string {
	return filepath.Join(s.workspaceRoot, instance, ".claude", "settings.json")
}

func repoSettingsPath(s *testState, groupRepo, instance string) string {
	return filepath.Join(s.workspaceRoot, instance, groupRepo, ".claude", "settings.local.json")
}

func lastWorktreeSettingsPath(s *testState) (string, error) {
	if s.lastSessionWorktreePath == "" {
		return "", fmt.Errorf("no worktree recorded; create one first")
	}
	return filepath.Join(s.lastSessionWorktreePath, ".claude", "settings.local.json"), nil
}

func theWorkspaceRootSettingsDocumentHasDefaultMode(ctx context.Context, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	return assertSettingsDefaultMode(workspaceRootSettingsPath(s), want)
}

func theInstanceSettingsDocumentHasDefaultMode(ctx context.Context, instance, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	return assertSettingsDefaultMode(instanceRootSettingsPath(s, instance), want)
}

func theRepoSettingsDocumentHasDefaultMode(ctx context.Context, groupRepo, instance, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	return assertSettingsDefaultMode(repoSettingsPath(s, groupRepo, instance), want)
}

func theLastWorktreeSettingsDocumentHasDefaultMode(ctx context.Context, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path, err := lastWorktreeSettingsPath(s)
	if err != nil {
		return err
	}
	return assertSettingsDefaultMode(path, want)
}

// theSettingsDocumentsExistAndParse checks, before any value is compared, that
// every document the matrix reads is on disk and parses: the workspace root,
// the instance root, each named repo (comma-separated "<group>/<repo>"), and
// the last worktree. Each value step would also fail on a missing or broken
// document; this step makes that check up front and in one place, and it is
// the only check of the worktree document as niwa worktree create left it,
// since S9's row applies the instance before its worktree value is read.
func theSettingsDocumentsExistAndParse(ctx context.Context, instance, repos string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	paths := []string{workspaceRootSettingsPath(s), instanceRootSettingsPath(s, instance)}
	for _, r := range strings.Split(repos, ",") {
		paths = append(paths, repoSettingsPath(s, strings.TrimSpace(r), instance))
	}
	wt, err := lastWorktreeSettingsPath(s)
	if err != nil {
		return err
	}
	paths = append(paths, wt)
	for _, p := range paths {
		if _, _, err := lookupJSONKey(p, "permissions.defaultMode"); err != nil {
			return err
		}
	}
	return nil
}

// iRunNiwaApplyTimesInInstance runs `niwa apply` from the instance directory
// the given number of times, failing on a non-zero exit. Zero is a valid
// count: a Scenario Outline row that must not apply between two steps says so
// in its own column rather than in a separate scenario.
func iRunNiwaApplyTimesInInstance(ctx context.Context, times int, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.workspaceRoot, instance)
	for i := 0; i < times; i++ {
		if err := runNiwa(s, dir, "niwa apply"); err != nil {
			return ctx, err
		}
		if s.exitCode != 0 {
			return ctx, fmt.Errorf("niwa apply in instance %q exit=%d\nstdout:\n%s\nstderr:\n%s", instance, s.exitCode, s.stdout, s.stderr)
		}
	}
	return ctx, nil
}

// registerPostureSteps wires the permission-posture steps into the scenario
// context. Called from initializeScenario.
func registerPostureSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the host config declares global settings:$`, theHostConfigDeclaresGlobalSettings)
	ctx.Step(`^the workspace root settings document has permissions\.defaultMode "([^"]*)"$`, theWorkspaceRootSettingsDocumentHasDefaultMode)
	ctx.Step(`^the instance "([^"]*)" settings document has permissions\.defaultMode "([^"]*)"$`, theInstanceSettingsDocumentHasDefaultMode)
	ctx.Step(`^the repo "([^"]*)" settings document in instance "([^"]*)" has permissions\.defaultMode "([^"]*)"$`, theRepoSettingsDocumentHasDefaultMode)
	ctx.Step(`^the last worktree settings document has permissions\.defaultMode "([^"]*)"$`, theLastWorktreeSettingsDocumentHasDefaultMode)
	ctx.Step(`^the workspace root, instance "([^"]*)", repos "([^"]*)", and last worktree settings documents exist and parse as JSON$`, theSettingsDocumentsExistAndParse)
	ctx.Step(`^I run niwa apply (\d+) times? in instance "([^"]*)"$`, iRunNiwaApplyTimesInInstance)
}
